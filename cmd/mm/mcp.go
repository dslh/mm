package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/dslh/mm/internal/api"
	"github.com/dslh/mm/internal/ops"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const mcpVersion = "1.0.0"

// MCP-UI (MCP Apps, SEP-1865) product-card template. The `show` tool advertises
// it via _meta.ui.resourceUri; the host fetches this ui:// resource and renders
// it in a sandboxed iframe, feeding it the tool's structuredContent. mimeType
// carries the `;profile=mcp-app` marker the spec requires.
const (
	cardResourceURI  = "ui://mm/product-card.html"
	addedResourceURI = "ui://mm/cart-added.html"
	cardMIME         = "text/html;profile=mcp-app"
	// cardImageOrigin is the Cloudinary origin product thumbnails load from
	// (see api.Product.ThumbnailURL); declared in the resource CSP so the host
	// sandbox permits the images.
	cardImageOrigin = "https://res.cloudinary.com"
)

//go:embed product-card.html
var productCardHTML string

//go:embed cart-added.html
var cartAddedHTML string

// mcpInstructions is the server-level orientation sent once at initialize. It
// carries the cross-cutting context (scope, units, workflow, id handoffs) so the
// individual tool descriptions can stay lean.
const mcpInstructions = `mon-marché is a French online grocery (Paris/Île-de-France delivery). These tools browse the catalog and manage one shopping cart under the signed-in user's account.

Scope: read the catalog, read and modify the cart, choose a delivery slot. That is all. Final review, checkout, and payment are always done by the user in the browser — there is no tool for them, and you must never ask for or handle payment details.

Units: every monetary amount is an integer number of euro cents (424 means 4,24 €). Quantities and stock are plain item counts.

Typical workflow: find products with search or browse; inspect the cart with get_cart; add, change, or remove lines with cart_apply (the only cart-mutation tool); optionally pick a delivery window with list_slots then select_slot. To rebuild a previous order, use reorder.

Identifiers connect the tools: search/browse return a product canonicalId (use it as cart_apply's id) and a slug (use it with get_product); list_orders returns an order id (use it with get_order or reorder); list_slots returns a slot id (use it with select_slot).

Pacing: requests are deliberately rate-limited to stay human-paced. Batch related cart changes into a single cart_apply call rather than many separate ones, and avoid tight polling.`

// cmdMCP runs the MCP server over stdio: a thin wrapper exposing internal/ops
// as tools for Claude. Scope is identical to the CLI — browse/search and
// cart read/mutate — and stops at the cart: no checkout, no payment.
//
// A single api.Client is shared across all tools so the pacing lock actually
// serializes traffic, and every handler is additionally serialized through
// mcpMu: requests stay human-paced (ToS review) and the (un-synchronized)
// session state is never touched concurrently.
func cmdMCP(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return usageError("mm mcp")
	}
	c, err := api.New(statePath())
	if err != nil {
		return err
	}
	defer func() {
		if err := c.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "mm: saving session state:", err)
		}
	}()
	o := &ops.Ops{API: c}

	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "mm",
		Title:   "mon-marché shopping assistant",
		Version: mcpVersion,
	}, &mcp.ServerOptions{Instructions: mcpInstructions})
	registerTools(srv, o)

	// Clean shutdown on Ctrl-C / SIGTERM for manual runs; an MCP client
	// stopping the server just closes stdin, which ends Run on its own.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintln(os.Stderr, "mm mcp: serving on stdio (cart only; checkout stays in the browser)")
	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil &&
		!errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// mcpMu serializes every tool handler: only one API operation runs at a time.
var mcpMu sync.Mutex

// mcpTool registers a tool whose result is the same JSON shape the CLI emits
// with --json. Out is `any`, so the SDK infers a schema for the typed input
// but none for the output (server.go: output schema is skipped when Out==any),
// then mirrors the returned value into both structuredContent and text. The
// handler returns the view-shaped value directly — full control over what
// leaves the process, which matters for the PII-stripping cart view.
func mcpTool[In any](s *mcp.Server, name, desc string, fn func(context.Context, In) (any, error)) {
	mcp.AddTool(s, &mcp.Tool{Name: name, Description: desc},
		func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
			mcpMu.Lock()
			defer mcpMu.Unlock()
			out, err := fn(ctx, in)
			if err != nil {
				return nil, nil, mcpError(err)
			}
			return nil, out, nil
		})
}

// mcpUITool is mcpTool plus an MCP-UI binding: the tool advertises a ui://
// template via _meta.ui.resourceUri (Tool.Meta is copied verbatim by AddTool),
// so a host that supports MCP Apps renders the result as a UI instead of text.
// The returned value still becomes structuredContent — the data the template
// renders against — and the SDK mirrors it into text for non-UI hosts.
func mcpUITool[In any](s *mcp.Server, name, desc, resourceURI string, fn func(context.Context, In) (any, error)) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        name,
		Description: desc,
		Meta:        mcp.Meta{"ui": map[string]any{"resourceUri": resourceURI}},
	},
		func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
			mcpMu.Lock()
			defer mcpMu.Unlock()
			out, err := fn(ctx, in)
			if err != nil {
				return nil, nil, mcpError(err)
			}
			return nil, out, nil
		})
}

// registerUIResource serves one static MCP-UI template at uri. The templates
// are static (data arrives per-call as structuredContent), so the handler
// ignores the request and always returns the embedded bytes. The sandbox CSP
// is deny-by-default, so product thumbnails (Cloudinary) are blocked unless
// their origin is declared here: resourceDomains maps to img-src (per
// SEP-1865), set on the contents object, not the Resource descriptor.
func registerUIResource(s *mcp.Server, name, uri, desc, html string) {
	s.AddResource(&mcp.Resource{
		Name:        name,
		URI:         uri,
		MIMEType:    cardMIME,
		Description: desc,
	}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI:      uri,
			MIMEType: cardMIME,
			Text:     html,
			Meta: mcp.Meta{"ui": map[string]any{
				"csp": map[string]any{"resourceDomains": []string{cardImageOrigin}},
			}},
		}}}, nil
	})
}

// mcpError turns the library's error taxonomy into actionable tool errors.
// The SDK reports these to the model as an error result (isError) with the
// message in the content, so the agent can see what went wrong and recover.
func mcpError(err error) error {
	var ae *api.APIError
	if errors.As(err, &ae) && ae.IsAuth() {
		return fmt.Errorf("session expired — recreate .auth/state.json via a browser login (see `mm auth login`): %w", err)
	}
	var de *api.DriftError
	if errors.As(err, &de) {
		return fmt.Errorf("the private API may have changed — re-verify against fresh browser traffic (docs/api.md): %w", err)
	}
	return err
}

// Tool input types. Descriptions ride along as JSON-schema annotations so the
// model sees them; empty structs are tools that take no arguments.

type searchArgs struct {
	Query string `json:"query" jsonschema:"product search text, e.g. 'tomate cerise'"`
	All   bool   `json:"all,omitempty" jsonschema:"follow all result pages instead of just the first (slower; paced)"`
}

type browseArgs struct {
	Slug string `json:"slug,omitempty" jsonschema:"category slug to list; omit to get the top-level navigation tree"`
}

type productArgs struct {
	Slug string `json:"slug" jsonschema:"product slug (from search/browse results)"`
}

type ordersArgs struct {
	Limit int `json:"limit,omitempty" jsonschema:"max orders to return, most recent first; 0 or omitted returns all"`
}

type orderArgs struct {
	ID string `json:"id" jsonschema:"order id from list_orders"`
}

type selectSlotArgs struct {
	SlotID string `json:"slotId" jsonschema:"delivery slot id from list_slots"`
}

type reorderArgs struct {
	ID     string `json:"id" jsonschema:"past order id to rebuild into the cart"`
	DryRun bool   `json:"dryRun,omitempty" jsonschema:"plan only: report the lines and availability without changing the cart"`
}

// applyLine mirrors ops.ApplyLine with schema annotations for the model.
type applyLine struct {
	Query string `json:"query,omitempty" jsonschema:"search query; the first matching product is used. Provide exactly one of query or id"`
	ID    string `json:"id,omitempty" jsonschema:"exact product canonicalId. Provide exactly one of query or id"`
	N     *int   `json:"n,omitempty" jsonschema:"increment quantity by N relative to what's in the cart (default 1). Use for adding"`
	Set   *int   `json:"set,omitempty" jsonschema:"set absolute quantity; 0 removes the line. Takes precedence over n"`
}

type applyArgs struct {
	Lines []applyLine `json:"lines" jsonschema:"cart changes applied in order, each adding/setting one product"`
}

type showItem struct {
	Slug string `json:"slug" jsonschema:"product slug (from search/browse/get_product) to display"`
	Note string `json:"note,omitempty" jsonschema:"optional one-line reason this product is worth showing — surfaced on the card (e.g. why it fits the request)"`
}

type showArgs struct {
	Items []showItem `json:"items" jsonschema:"the curated products to show as cards, in display order"`
}

func registerTools(s *mcp.Server, o *ops.Ops) {
	registerUIResource(s, "product-card", cardResourceURI,
		"Interactive product cards rendered by the show tool.", productCardHTML)
	registerUIResource(s, "cart-added", addedResourceURI,
		"Confirmation cards rendered by cart_apply for items just added to the cart.", cartAddedHTML)

	mcpUITool(s, "show",
		"Display a curated set of products to the user as visual cards — thumbnail, name, price, weight, origin, any tags/promo, your note, and an add-to-cart button. Use this AFTER inspecting candidates with search/browse/get_product to present the few you actually recommend for their final choice; it is not for dumping raw search results. Identify each product by its slug and include a short `note` saying why it fits. Read-only: showing a product never changes the cart (the card's button does, via cart_apply).",
		cardResourceURI,
		func(ctx context.Context, in showArgs) (any, error) {
			products := make([]productCardView, 0, len(in.Items))
			var errs []map[string]string
			for _, it := range in.Items {
				if it.Slug == "" {
					continue
				}
				d, err := o.API.ArticleBySlug(ctx, it.Slug)
				if err != nil {
					errs = append(errs, map[string]string{"slug": it.Slug, "error": err.Error()})
					continue
				}
				products = append(products, productCard(&d.Product, it.Note))
			}
			out := map[string]any{"products": products}
			if len(errs) > 0 {
				out["errors"] = errs
			}
			return out, nil
		})

	mcpTool(s, "search",
		"Search the catalog by free text — use when you know roughly what you want by name (e.g. 'tomate cerise'). Returns matching products (canonicalId, name, slug, price in euro cents, stock, plus any tags in attributes[] under key 'specific-tag' such as BIO/Nouveau/Sans nitrite, and promo). Use a product's canonicalId with cart_apply to add it.",
		func(ctx context.Context, in searchArgs) (any, error) {
			if in.All {
				return o.API.SearchAll(ctx, in.Query, maxSearchPages)
			}
			return o.API.Search(ctx, in.Query, "")
		})

	mcpTool(s, "browse",
		"Browse the catalog by category — use to explore what's on offer rather than search for a named item. With no slug, returns the navigation tree of families/categories; with a slug, returns that category's subcategories and products.",
		func(ctx context.Context, in browseArgs) (any, error) {
			if in.Slug == "" {
				return o.API.Navigation(ctx)
			}
			return o.API.Category(ctx, in.Slug)
		})

	mcpTool(s, "get_product",
		"Fetch full detail for one known product by slug — more than search/browse return (price in euro cents, stock, origin, description, promo). Use when you already have a slug and need the full record.",
		func(ctx context.Context, in productArgs) (any, error) {
			return o.API.ArticleBySlug(ctx, in.Slug)
		})

	mcpTool(s, "get_cart",
		"Show the current cart: line items with quantities, totals, and distance to free shipping. Monetary amounts are euro cents (424 = 4,24 €). Delivery address is never returned.",
		func(ctx context.Context, _ struct{}) (any, error) {
			cart, err := o.GetCart(ctx)
			if err != nil {
				return nil, err
			}
			return viewCart(cart), nil
		})

	mcpUITool(s, "cart_apply",
		"Add to / set / remove cart items in one call. Each line targets a product by `query` (first search hit) or exact `id` (canonicalId), and either increments by `n` (default 1) or sets an absolute quantity with `set` (`set:0` removes). Quantities over stock are clamped and reported. Returns a per-line outcome plus the updated cart, and shows the user a visual confirmation card (thumbnail, name, quantity, cost, remove button) for each item added. This is the only cart-mutation tool; checkout and payment stay in the browser.",
		addedResourceURI,
		func(ctx context.Context, in applyArgs) (any, error) {
			lines := make([]ops.ApplyLine, len(in.Lines))
			for i, l := range in.Lines {
				lines[i] = ops.ApplyLine{Query: l.Query, ID: l.ID, N: l.N, Set: l.Set}
			}
			cart, outcomes, err := o.Apply(ctx, lines)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"outcomes": outcomes,
				"cart":     viewCart(cart),
				"added":    addedCards(cart, outcomes),
			}, nil
		})

	mcpTool(s, "list_orders",
		"List past orders, most recent first (id, state, total in cents, delivery slot, product names). Use an id with get_order or reorder.",
		func(ctx context.Context, in ordersArgs) (any, error) {
			res, err := o.API.OrdersPast(ctx)
			if err != nil {
				return nil, err
			}
			if in.Limit > 0 && len(res.Items) > in.Limit {
				res.Items = res.Items[:in.Limit]
			}
			return res, nil
		})

	mcpTool(s, "get_order",
		"Fetch one past order's detail: line items with canonicalIds and quantities, totals, delivery slot.",
		func(ctx context.Context, in orderArgs) (any, error) {
			return o.API.Order(ctx, in.ID)
		})

	mcpTool(s, "reorder",
		"Rebuild the cart from a past order, adding each line's quantity on top of what's already in the cart. Stale or out-of-stock lines are reported with search suggestions, never auto-substituted. Use dryRun to preview without changing the cart.",
		func(ctx context.Context, in reorderArgs) (any, error) {
			order, cart, outcomes, err := o.Reorder(ctx, in.ID, in.DryRun)
			if err != nil {
				return nil, err
			}
			out := map[string]any{"orderId": order.ID, "dryRun": in.DryRun, "outcomes": outcomes}
			if cart != nil {
				out["cart"] = viewCart(cart)
			}
			return out, nil
		})

	mcpTool(s, "list_slots",
		"List available delivery windows for the cart's delivery address (slot id, window times, order-by deadlines, any surcharge). Use a slot id with select_slot.",
		func(ctx context.Context, _ struct{}) (any, error) {
			return o.Slots(ctx)
		})

	mcpTool(s, "select_slot",
		"Set the cart's delivery window to the given slot id (from list_slots), reusing the cart's existing delivery address. Refuses slots that are full, expired, or excluded. Returns the chosen window and the updated cart. Final review, checkout, and payment stay in the browser.",
		func(ctx context.Context, in selectSlotArgs) (any, error) {
			cart, slot, err := o.SelectSlot(ctx, in.SlotID)
			if err != nil {
				return nil, err
			}
			return map[string]any{"selected": slot, "cart": viewCart(cart)}, nil
		})

	mcpTool(s, "auth_status",
		"Check the mon-marché session: whether it's valid (live probe) and when the cookie expires. If invalid, the user must re-login (see `mm auth login`).",
		func(ctx context.Context, _ struct{}) (any, error) {
			probeErr := o.API.ProbeAuth(ctx)
			exp := o.API.SessionExpires() // read after the probe: a valid probe rolls it forward
			out := map[string]any{
				"valid":     probeErr == nil,
				"expiresAt": exp.Format(time.RFC3339),
				"daysLeft":  int(time.Until(exp).Hours() / 24),
			}
			if probeErr != nil {
				out["detail"] = probeErr.Error()
			}
			return out, nil
		})
}
