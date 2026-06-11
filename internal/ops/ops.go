// Package ops composes api calls into the task-level operations the CLI (and
// later the MCP server) exposes: resolution by search, quantity clamping,
// batch apply, reorder. Product-level failures (stale id, out of stock) are
// reported as outcomes, not errors: errors are reserved for auth, drift and
// transport problems that abort the whole run.
package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/dslh/mm/internal/api"
)

type Ops struct {
	API *api.Client
}

// ItemOutcome statuses.
const (
	StatusUpdated    = "updated"
	StatusClamped    = "clamped"
	StatusRemoved    = "removed"
	StatusOutOfStock = "out-of-stock"
	StatusStaleID    = "stale-id"
	StatusNotFound   = "not-found"
	StatusError      = "error"
	StatusPlanned    = "planned" // dry-run only
)

type Alt struct {
	CanonicalID string  `json:"canonicalId"`
	Name        string  `json:"name"`
	ItemPrice   float64 `json:"itemPriceCents"`
}

// ItemOutcome reports what happened to one requested item.
type ItemOutcome struct {
	Status      string  `json:"status"`
	Via         string  `json:"via"` // "id" | "search:<query>" | "order"
	CanonicalID string  `json:"canonicalId,omitempty"`
	Name        string  `json:"name,omitempty"`
	UnitPrice   float64 `json:"unitPriceCents,omitempty"`
	Requested   int     `json:"requestedQty"`
	Final       int     `json:"finalQty"`
	Available   int     `json:"availableQty,omitempty"`
	Alternates  []Alt   `json:"alternates,omitempty"`
	Err         string  `json:"error,omitempty"`
}

// ApplyLine is one line of a batch: exactly one of Query/ID, plus N
// (increment, default 1) or Set (absolute quantity; 0 removes).
type ApplyLine struct {
	Query string `json:"query,omitempty"`
	ID    string `json:"id,omitempty"`
	N     *int   `json:"n,omitempty"`
	Set   *int   `json:"set,omitempty"`
}

// GetCart disambiguates the 404 E_08_0005, which means either "anonymous" or
// "no cart yet" (docs/api.md "Cart bootstrap"): probe auth, then treat as empty.
func (o *Ops) GetCart(ctx context.Context) (*api.Cart, error) {
	cart, err := o.API.Cart(ctx)
	var ae *api.APIError
	if errors.As(err, &ae) && ae.IsCartNotFound() {
		if perr := o.API.ProbeAuth(ctx); perr != nil {
			return nil, perr
		}
		return &api.Cart{}, nil
	}
	return cart, err
}

func (o *Ops) AddByID(ctx context.Context, cart *api.Cart, id string, n int) (*api.Cart, ItemOutcome, error) {
	return o.applyOne(ctx, cart, ApplyLine{ID: id, N: &n})
}

func (o *Ops) AddByQuery(ctx context.Context, cart *api.Cart, query string, n int) (*api.Cart, ItemOutcome, error) {
	return o.applyOne(ctx, cart, ApplyLine{Query: query, N: &n})
}

func (o *Ops) SetByID(ctx context.Context, cart *api.Cart, id string, qty int) (*api.Cart, ItemOutcome, error) {
	return o.applyOne(ctx, cart, ApplyLine{ID: id, Set: &qty})
}

// Apply runs a batch of lines sequentially under the client's pacing,
// reading the cart once up front.
func (o *Ops) Apply(ctx context.Context, lines []ApplyLine) (*api.Cart, []ItemOutcome, error) {
	cart, err := o.GetCart(ctx)
	if err != nil {
		return nil, nil, err
	}
	outcomes := make([]ItemOutcome, 0, len(lines))
	for _, ln := range lines {
		var oc ItemOutcome
		cart, oc, err = o.applyOne(ctx, cart, ln)
		if err != nil {
			return cart, outcomes, err
		}
		outcomes = append(outcomes, oc)
	}
	return cart, outcomes, nil
}

func (o *Ops) applyOne(ctx context.Context, cart *api.Cart, ln ApplyLine) (*api.Cart, ItemOutcome, error) {
	var oc ItemOutcome
	id := ln.ID
	avail := -1 // unknown

	switch {
	case (ln.ID == "") == (ln.Query == ""):
		oc.Status = StatusError
		oc.Err = `need exactly one of "id" or "query"`
		return cart, oc, nil
	case ln.ID != "":
		oc.Via = "id"
		oc.CanonicalID = id
		if p := findProduct(cart, id); p != nil {
			avail = p.AvailableQuantity
			oc.Name, oc.UnitPrice = p.Name, p.ItemPrice
		}
	default:
		oc.Via = "search:" + ln.Query
		res, err := o.API.Search(ctx, ln.Query, "")
		if err != nil {
			return cart, oc, err
		}
		var prods []api.Product
		for _, p := range res.Items {
			if p.Type == "" || p.Type == "PRODUCT" {
				prods = append(prods, p)
			}
		}
		if len(prods) == 0 {
			oc.Status = StatusNotFound
			return cart, oc, nil
		}
		pick := prods[0]
		id, avail = pick.CanonicalID, pick.AvailableQuantity
		oc.CanonicalID, oc.Name, oc.UnitPrice = id, pick.Name, pick.ItemPrice
		for _, a := range prods[1:min(3, len(prods))] {
			oc.Alternates = append(oc.Alternates, Alt{a.CanonicalID, a.Name, a.ItemPrice})
		}
	}

	target := 0
	switch {
	case ln.Set != nil:
		target = *ln.Set
	case ln.N != nil:
		target = cart.Quantity(id) + *ln.N
	default:
		target = cart.Quantity(id) + 1
	}
	if target < 0 {
		target = 0
	}
	return o.setQuantity(ctx, cart, oc, id, target, avail)
}

// setQuantity PATCHes the absolute quantity, clamping to known availability
// first: the API rejects oversell outright rather than clamping (docs/api.md).
func (o *Ops) setQuantity(ctx context.Context, cart *api.Cart, oc ItemOutcome, id string, target, avail int) (*api.Cart, ItemOutcome, error) {
	oc.Requested = target
	oc.Final = cart.Quantity(id)
	if avail >= 0 {
		oc.Available = avail
	}

	if target > 0 && avail == 0 {
		oc.Status = StatusOutOfStock
		return cart, oc, nil
	}
	clamped := false
	if avail > 0 && target > avail {
		target, clamped = avail, true
	}

	newCart, err := o.API.SetCartProduct(ctx, id, target)
	if err != nil {
		var ae *api.APIError
		if errors.As(err, &ae) && ae.IsProduct() {
			switch ae.Code {
			case api.CodeStaleProduct:
				oc.Status = StatusStaleID
			case api.CodeOutOfStock:
				oc.Status = StatusOutOfStock
			default:
				oc.Status = StatusError
				oc.Err = ae.Error()
			}
			return cart, oc, nil
		}
		return cart, oc, err
	}

	oc.Final = newCart.Quantity(id)
	if p := findProduct(newCart, id); p != nil {
		if oc.Name == "" {
			oc.Name = p.Name
		}
		if oc.UnitPrice == 0 {
			oc.UnitPrice = p.ItemPrice
		}
	}
	switch {
	case target == 0:
		oc.Status = StatusRemoved
	case clamped:
		oc.Status = StatusClamped
	default:
		oc.Status = StatusUpdated
	}
	return newCart, oc, nil
}

// Reorder rebuilds the cart from a past order (incrementally — quantities add
// to whatever is already in the cart). Stale ids and out-of-stock lines are
// reported with search suggestions, never auto-substituted.
func (o *Ops) Reorder(ctx context.Context, orderID string, dryRun bool) (*api.Order, *api.Cart, []ItemOutcome, error) {
	order, err := o.API.Order(ctx, orderID)
	if err != nil {
		return nil, nil, nil, err
	}

	outcomes := make([]ItemOutcome, 0, len(order.Products))
	if dryRun {
		for _, p := range order.Products {
			outcomes = append(outcomes, ItemOutcome{
				Status: StatusPlanned, Via: "order",
				CanonicalID: p.CanonicalID, Name: p.Name,
				UnitPrice: p.ItemPrice, Requested: p.Quotation.Count,
			})
		}
		return order, nil, outcomes, nil
	}

	cart, err := o.GetCart(ctx)
	if err != nil {
		return order, nil, nil, err
	}
	for _, p := range order.Products {
		n := p.Quotation.Count
		if n < 1 {
			n = 1
		}
		var oc ItemOutcome
		cart, oc, err = o.applyOne(ctx, cart, ApplyLine{ID: p.CanonicalID, N: &n})
		if err != nil {
			return order, cart, outcomes, err
		}
		oc.Via = "order"
		if oc.Name == "" {
			oc.Name = p.Name
		}
		if oc.UnitPrice == 0 {
			oc.UnitPrice = p.ItemPrice
		}
		if (oc.Status == StatusStaleID || oc.Status == StatusOutOfStock) && p.Name != "" {
			if res, serr := o.API.Search(ctx, p.Name, ""); serr == nil {
				for _, a := range res.Items[:min(2, len(res.Items))] {
					oc.Alternates = append(oc.Alternates, Alt{a.CanonicalID, a.Name, a.ItemPrice})
				}
			}
		}
		outcomes = append(outcomes, oc)
	}
	return order, cart, outcomes, nil
}

// Slots lists delivery windows for the cart's delivery address. The address
// location is used only as request input and is never returned or printed.
func (o *Ops) Slots(ctx context.Context) (*api.DeliverySlotsResponse, error) {
	cart, err := o.GetCart(ctx)
	if err != nil {
		return nil, err
	}
	addr := cart.Delivery.Address
	if addr.AddressComponents.PostalCode == "" {
		return nil, fmt.Errorf("cart has no delivery address; set one in the browser first")
	}
	return o.API.DeliverySlots(ctx, addr.Location, addr.AddressComponents.PostalCode, addr.AddressComponents.CountryCode)
}

// ParseApplyLines reads JSON lines ({"query":"tomate","n":2} / {"id":"…","set":0}),
// or a single JSON array of the same objects. Blank lines and #-comments are skipped.
func ParseApplyLines(r io.Reader) ([]ApplyLine, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, fmt.Errorf("empty input")
	}
	if strings.HasPrefix(trimmed, "[") {
		var lines []ApplyLine
		if err := json.Unmarshal([]byte(trimmed), &lines); err != nil {
			return nil, fmt.Errorf("parsing JSON array: %w", err)
		}
		return lines, nil
	}
	var lines []ApplyLine
	for i, l := range strings.Split(trimmed, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		var ln ApplyLine
		if err := json.Unmarshal([]byte(l), &ln); err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		lines = append(lines, ln)
	}
	return lines, nil
}

func findProduct(c *api.Cart, canonicalID string) *api.CartProduct {
	for i := range c.Products {
		if c.Products[i].CanonicalID == canonicalID {
			return &c.Products[i]
		}
	}
	return nil
}
