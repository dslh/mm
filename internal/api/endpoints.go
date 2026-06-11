package api

import (
	"context"
	"fmt"
	"net/url"
)

func (c *Client) Search(ctx context.Context, text, next string) (*SearchResponse, error) {
	q := url.Values{"type": {"PRODUCT"}, "text": {text}, "modelVersion": {"ordinal_df"}}
	if next != "" {
		q.Set("next", next)
	}
	var out SearchResponse
	if err := c.do(ctx, "GET", "/search2", q, nil, &out); err != nil {
		return nil, err
	}
	if err := validateProducts(out.Items); err != nil {
		return nil, err
	}
	return &out, nil
}

// SearchAll follows the `next` cursor until the last page or maxPages.
// If Next is still set on the returned response, results were truncated.
func (c *Client) SearchAll(ctx context.Context, text string, maxPages int) (*SearchResponse, error) {
	res, err := c.Search(ctx, text, "")
	if err != nil {
		return nil, err
	}
	for page := 1; res.Next != "" && page < maxPages; page++ {
		next, err := c.Search(ctx, text, res.Next)
		if err != nil {
			return nil, err
		}
		res.Items = append(res.Items, next.Items...)
		res.Next = next.Next
	}
	return res, nil
}

func (c *Client) Navigation(ctx context.Context) (*Navigation, error) {
	var out Navigation
	if err := c.do(ctx, "GET", "/navigation", nil, nil, &out); err != nil {
		return nil, err
	}
	if len(out.Families) == 0 {
		return nil, &DriftError{Snippet: "navigation returned no families"}
	}
	return &out, nil
}

func (c *Client) Category(ctx context.Context, slug string) (*Category, error) {
	var out Category
	if err := c.do(ctx, "GET", "/category/"+url.PathEscape(slug), nil, nil, &out); err != nil {
		return nil, err
	}
	if err := validateProducts(out.Items); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ArticleBySlug(ctx context.Context, slug string) (*ArticleDetail, error) {
	var out ArticleDetail
	if err := c.do(ctx, "GET", "/articleDetailBySlug/"+url.PathEscape(slug), nil, nil, &out); err != nil {
		return nil, err
	}
	if out.CanonicalID == "" || out.Name == "" {
		return nil, &DriftError{Snippet: "article detail missing canonicalId/name"}
	}
	return &out, nil
}

func (c *Client) Cart(ctx context.Context) (*Cart, error) {
	var out Cart
	if err := c.do(ctx, "GET", "/cart", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetCartProduct PATCHes the absolute quantity for a product; 0 removes it.
// Returns the full updated cart.
func (c *Client) SetCartProduct(ctx context.Context, canonicalID string, quantity int) (*Cart, error) {
	if canonicalID == "" {
		return nil, fmt.Errorf("empty canonicalId")
	}
	if quantity < 0 {
		return nil, fmt.Errorf("quantity must be >= 0, got %d", quantity)
	}
	body := map[string]any{"product": map[string]any{"id": canonicalID, "quantity": quantity}}
	var out Cart
	if err := c.do(ctx, "PATCH", "/cart/product", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) OrdersPast(ctx context.Context) (*OrdersPast, error) {
	var out OrdersPast
	if err := c.do(ctx, "GET", "/orders/past", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Order(ctx context.Context, id string) (*Order, error) {
	var out Order
	if err := c.do(ctx, "GET", "/orders/"+url.PathEscape(id), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeliverySlots(ctx context.Context, loc Location, postalCode, countryCode string) (*DeliverySlotsResponse, error) {
	body := map[string]any{"location": loc, "postalCode": postalCode, "countryCode": countryCode}
	var out DeliverySlotsResponse
	if err := c.do(ctx, "POST", "/addresses/deliverySlots2", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ProbeAuth hits /orders/past — the documented unambiguous auth probe
// (cart reads 404 for both expired sessions and missing carts).
func (c *Client) ProbeAuth(ctx context.Context) error {
	return c.do(ctx, "GET", "/orders/past", nil, nil, nil)
}

func validateProducts(items []Product) error {
	for i := range items {
		p := &items[i]
		if p.Type != "" && p.Type != "PRODUCT" {
			continue
		}
		if p.CanonicalID == "" || p.Name == "" {
			return &DriftError{Snippet: fmt.Sprintf("product item %d missing canonicalId/name", i)}
		}
	}
	return nil
}
