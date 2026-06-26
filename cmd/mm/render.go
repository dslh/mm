package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/dslh/mm/internal/api"
	"github.com/dslh/mm/internal/ops"
)

var paris = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		return time.Local
	}
	return loc
}()

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func euro(cents float64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
	}
	c := int64(math.Round(math.Abs(cents)))
	return fmt.Sprintf("%s%d,%02d €", sign, c/100, c%100)
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func productLine(p *api.Product) string {
	price := euro(p.ItemPrice) + p.PriceUnit()
	line := fmt.Sprintf("  %-12s %-48s %14s  stock %-4d slug:%s",
		p.CanonicalID, trunc(p.Name, 48), price, p.AvailableQuantity, p.Slug)
	if t := p.SpecificTag(); t != "" {
		line += "  [" + t + "]"
	}
	if p.Promo != nil {
		line += "  " + promoTag(p.Promo)
	}
	return line
}

func promoTag(pr *api.Promo) string {
	// BATCH_DISCOUNT = "le Nème à -X%" (buy several, the Nth is discounted).
	if pr.Mechanism == "BATCH_DISCOUNT" && pr.Conditions.NthQuantity > 0 &&
		pr.Conditions.Type == "PERCENT" && pr.Conditions.Value > 0 {
		return fmt.Sprintf("[%s à -%g%%]", ordinalFr(pr.Conditions.NthQuantity), pr.Conditions.Value)
	}
	if pr.Conditions.Type == "PERCENT" && pr.Conditions.Value > 0 {
		return fmt.Sprintf("[promo -%g%%]", pr.Conditions.Value)
	}
	if pr.ItemOriginalPrice > 0 {
		return fmt.Sprintf("[promo, was %s]", euro(pr.ItemOriginalPrice))
	}
	return "[promo]"
}

// ordinalFr renders a French ordinal as mon-marché labels them ("1er", "2ème").
func ordinalFr(n int) string {
	if n == 1 {
		return "1er"
	}
	return fmt.Sprintf("%dème", n)
}

func slotLabel(ts *api.OrderTimeSlot) string {
	if ts == nil || ts.From == 0 {
		return ""
	}
	from := time.UnixMilli(ts.From).In(paris)
	to := time.UnixMilli(ts.To).In(paris)
	return fmt.Sprintf("%s %s–%s", from.Format("Mon 02 Jan"), from.Format("15:04"), to.Format("15:04"))
}

// cartView is the --json shape for carts: products and price only, never the
// delivery block (it exists in api.Cart solely as input for the slots call).
type cartView struct {
	Products         []api.CartProduct `json:"products"`
	Price            api.CartPrice     `json:"price"`
	FreeShippingFrom *float64          `json:"freeShippingFromCents,omitempty"`
}

func viewCart(c *api.Cart) cartView {
	v := cartView{Products: c.Products, Price: c.Price}
	if thr := c.FreeShippingThreshold(); thr >= 0 {
		v.FreeShippingFrom = &thr
	}
	return v
}

// productCardView is the per-product payload the `show` tool emits as
// structuredContent for the MCP-UI card template (cmd/mm/product-card.html).
// Prices are euro cents (the template formats them); Note carries the agent's
// curation reason for this pick. None of these fields are PII.
type productCardView struct {
	CanonicalID string  `json:"canonicalId"` // for the card's add-to-cart → cart_apply
	Slug        string  `json:"slug"`
	Name        string  `json:"name"`
	Thumbnail   string  `json:"thumbnail,omitempty"`
	PriceCents  float64 `json:"priceCents"`
	PriceUnit   string  `json:"priceUnit,omitempty"` // "/kg", "/pièce"
	UnitWeight  string  `json:"unitWeight,omitempty"`
	Origin      string  `json:"origin,omitempty"`
	Tag         string  `json:"tag,omitempty"`   // the specific-tag pill (BIO, Nouveau, …)
	Promo       string  `json:"promo,omitempty"` // formatted promo label
	Stock       int     `json:"stock"`
	ShortDesc   string  `json:"shortDescription,omitempty"`
	Note        string  `json:"note,omitempty"` // agent's "why this one" annotation
}

func productCard(p *api.Product, note string) productCardView {
	c := productCardView{
		CanonicalID: p.CanonicalID,
		Slug:        p.Slug,
		Name:        p.Name,
		Thumbnail:   p.ThumbnailURL(320),
		PriceCents:  p.ItemPrice,
		PriceUnit:   p.PriceUnit(),
		UnitWeight:  p.UnitWeight(),
		Origin:      p.Origin,
		Tag:         p.SpecificTag(),
		Stock:       p.AvailableQuantity,
		ShortDesc:   p.ShortDescription,
		Note:        note,
	}
	if p.Promo != nil {
		c.Promo = promoTag(p.Promo)
	}
	return c
}

// cartAddedView is one card in the cart_apply confirmation strip
// (cmd/mm/cart-added.html): a product that just landed in the cart, with the
// quantity now held, the line cost, and a thumbnail. Prices are euro cents (the
// template formats them). None of these fields are PII — CanonicalID drives the
// card's remove button (cart_apply set:0).
type cartAddedView struct {
	CanonicalID string  `json:"canonicalId"`
	Name        string  `json:"name"`
	Thumbnail   string  `json:"thumbnail,omitempty"`
	UnitWeight  string  `json:"unitWeight,omitempty"` // per-piece weight label, when sold that way
	Quantity    int     `json:"quantity"`
	LineCents   float64 `json:"lineCents"` // unitPrice × quantity
	UnitCents   float64 `json:"unitCents"`
	Clamped     bool    `json:"clamped,omitempty"` // quantity was capped to available stock
}

// addedCards builds the cart_apply confirmation strip: one card per outcome
// that left a product in the cart (a successful add/update or a stock-clamped
// add with positive quantity). Removals, errors, out-of-stock and not-found
// outcomes stay in the textual outcomes, off the strip. The thumbnail is read
// straight from the post-mutation cart — which carries product images — so no
// extra fetch and no slug is needed, for id- and query-based adds alike.
func addedCards(cart *api.Cart, outcomes []ops.ItemOutcome) []cartAddedView {
	byID := map[string]*api.CartProduct{}
	if cart != nil {
		for i := range cart.Products {
			p := &cart.Products[i]
			byID[p.CanonicalID] = p
		}
	}
	cards := make([]cartAddedView, 0, len(outcomes))
	for _, oc := range outcomes {
		if oc.Final <= 0 {
			continue
		}
		switch oc.Status {
		case ops.StatusUpdated, ops.StatusClamped:
		default:
			continue
		}
		name := oc.Name
		if name == "" {
			name = oc.CanonicalID
		}
		card := cartAddedView{
			CanonicalID: oc.CanonicalID,
			Name:        name,
			Quantity:    oc.Final,
			LineCents:   float64(oc.Final) * oc.UnitPrice,
			UnitCents:   oc.UnitPrice,
			Clamped:     oc.Status == ops.StatusClamped,
		}
		if p := byID[oc.CanonicalID]; p != nil {
			card.Thumbnail, card.UnitWeight = p.ThumbnailURL(160), p.UnitWeight()
		}
		cards = append(cards, card)
	}
	return cards
}

func renderCart(c *api.Cart) {
	if len(c.Products) == 0 {
		fmt.Println("Cart is empty.")
		return
	}
	fmt.Printf("Cart — %d line(s)\n", len(c.Products))
	for _, p := range c.Products {
		fmt.Printf("  %3d × %-46s %10s  (%s, %s each)\n",
			p.Quotation.Count, trunc(p.Name, 46),
			euro(float64(p.Quotation.Count)*p.ItemPrice), p.CanonicalID, euro(p.ItemPrice))
	}
	q := c.Price.Quotation
	fmt.Printf("Total %s net", euro(q.Net))
	var extras []string
	if q.Shipping > 0 {
		extras = append(extras, "shipping "+euro(q.Shipping))
	}
	if q.PreparationFee > 0 {
		extras = append(extras, "preparation "+euro(q.PreparationFee))
	}
	if q.PromoSavings > 0 {
		extras = append(extras, "promo savings "+euro(q.PromoSavings))
	}
	if q.Discount > 0 {
		extras = append(extras, "discount "+euro(q.Discount))
	}
	if len(extras) > 0 {
		fmt.Printf(" (%s)", strings.Join(extras, ", "))
	}
	fmt.Println()
	freeShippingLine(c)
}

func cartSummary(c *api.Cart) {
	fmt.Printf("Cart: %d line(s), total %s net\n", len(c.Products), euro(c.Price.Quotation.Net))
	freeShippingLine(c)
}

func freeShippingLine(c *api.Cart) {
	thr := c.FreeShippingThreshold()
	if thr < 0 || len(c.Products) == 0 {
		return
	}
	q := c.Price.Quotation
	products := q.Net - q.Shipping - q.PreparationFee
	if products >= thr {
		fmt.Println("Free shipping reached.")
	} else {
		fmt.Printf("Free shipping from %s — %s to go\n", euro(thr), euro(thr-products))
	}
}

func renderOutcomes(outcomes []ops.ItemOutcome) {
	for _, oc := range outcomes {
		fmt.Println(outcomeLine(oc))
	}
}

func outcomeLine(oc ops.ItemOutcome) string {
	name := oc.Name
	if name == "" {
		name = oc.CanonicalID
	}
	var b strings.Builder
	switch oc.Status {
	case ops.StatusUpdated:
		fmt.Fprintf(&b, "✓ %s (%s): quantity now %d — %s each", name, oc.CanonicalID, oc.Final, euro(oc.UnitPrice))
	case ops.StatusClamped:
		fmt.Fprintf(&b, "⚠ %s (%s): only %d available — set to %d (wanted %d)", name, oc.CanonicalID, oc.Available, oc.Final, oc.Requested)
	case ops.StatusRemoved:
		fmt.Fprintf(&b, "✓ %s (%s): removed", name, oc.CanonicalID)
	case ops.StatusOutOfStock:
		fmt.Fprintf(&b, "✗ %s (%s): out of stock for the selected slot", name, oc.CanonicalID)
	case ops.StatusStaleID:
		fmt.Fprintf(&b, "✗ %s: id %s is stale (product relisted or gone)", name, oc.CanonicalID)
	case ops.StatusNotFound:
		fmt.Fprintf(&b, "✗ %s: no products found", oc.Via)
	case ops.StatusPlanned:
		fmt.Fprintf(&b, "· %d × %s (%s) — %s each", oc.Requested, name, oc.CanonicalID, euro(oc.UnitPrice))
	default:
		fmt.Fprintf(&b, "✗ %s (%s): %s", name, oc.CanonicalID, oc.Err)
	}
	if strings.HasPrefix(oc.Via, "search:") && oc.Status != ops.StatusNotFound {
		fmt.Fprintf(&b, " [picked via %s]", oc.Via)
	}
	for _, a := range oc.Alternates {
		fmt.Fprintf(&b, "\n      alt: %s (%s) %s", a.Name, a.CanonicalID, euro(a.ItemPrice))
	}
	return b.String()
}
