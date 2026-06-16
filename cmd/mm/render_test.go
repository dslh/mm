package main

import (
	"strings"
	"testing"

	"github.com/dslh/mm/internal/api"
	"github.com/dslh/mm/internal/ops"
)

func TestEuro(t *testing.T) {
	tests := []struct {
		cents float64
		want  string
	}{
		{0, "0,00 €"},
		{424, "4,24 €"},
		{100, "1,00 €"},
		{8000, "80,00 €"},
		{5, "0,05 €"},
		{-250, "-2,50 €"},
		{424.4, "4,24 €"},  // rounds down
		{424.6, "4,25 €"},  // rounds up
		{99999, "999,99 €"},
	}
	for _, tc := range tests {
		if got := euro(tc.cents); got != tc.want {
			t.Errorf("euro(%v) = %q, want %q", tc.cents, got, tc.want)
		}
	}
}

func TestTrunc(t *testing.T) {
	tests := []struct {
		s    string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"truncate me please", 10, "truncate …"},
		{"café crème noisette", 8, "café cr…"}, // multibyte safe
	}
	for _, tc := range tests {
		got := trunc(tc.s, tc.n)
		if got != tc.want {
			t.Errorf("trunc(%q, %d) = %q, want %q", tc.s, tc.n, got, tc.want)
		}
		if len([]rune(got)) > tc.n {
			t.Errorf("trunc(%q, %d) = %q exceeds %d runes", tc.s, tc.n, got, tc.n)
		}
	}
}

func TestOrdinalFr(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{1, "1er"},
		{2, "2ème"},
		{3, "3ème"},
	}
	for _, tc := range tests {
		if got := ordinalFr(tc.n); got != tc.want {
			t.Errorf("ordinalFr(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestPromoTag(t *testing.T) {
	batch := &api.Promo{Mechanism: "BATCH_DISCOUNT"}
	batch.Conditions.NthQuantity = 2
	batch.Conditions.Type = "PERCENT"
	batch.Conditions.Value = 50

	percent := &api.Promo{}
	percent.Conditions.Type = "PERCENT"
	percent.Conditions.Value = 20

	wasPrice := &api.Promo{ItemOriginalPrice: 300}

	tests := []struct {
		name  string
		promo *api.Promo
		want  string
	}{
		{"batch discount", batch, "[2ème à -50%]"},
		{"percent off", percent, "[promo -20%]"},
		{"was price", wasPrice, "[promo, was 3,00 €]"},
		{"bare promo", &api.Promo{}, "[promo]"},
	}
	for _, tc := range tests {
		if got := promoTag(tc.promo); got != tc.want {
			t.Errorf("%s: promoTag = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestSlotLabel(t *testing.T) {
	if got := slotLabel(nil); got != "" {
		t.Errorf("slotLabel(nil) = %q, want empty", got)
	}
	if got := slotLabel(&api.OrderTimeSlot{From: 0}); got != "" {
		t.Errorf("slotLabel(zero) = %q, want empty", got)
	}
	// 2026-06-16 08:00–10:00 Paris time (CEST, UTC+2).
	ts := &api.OrderTimeSlot{
		From: 1_750_053_600_000, // 2025-06-16 08:00 CEST
		To:   1_750_060_800_000, // 2025-06-16 10:00 CEST
	}
	got := slotLabel(ts)
	if !strings.Contains(got, "08:00") || !strings.Contains(got, "10:00") || !strings.Contains(got, "–") {
		t.Errorf("slotLabel = %q, want an 08:00–10:00 window", got)
	}
}

func TestViewCartHidesDelivery(t *testing.T) {
	c := &api.Cart{
		Products: []api.CartProduct{{CanonicalID: "p1"}},
	}
	c.Delivery.DeliveryPrices = []struct {
		MinCartNetPrice float64 `json:"minCartNetPrice"`
		ShippingAmount  float64 `json:"shippingAmount"`
	}{{MinCartNetPrice: 8000, ShippingAmount: 0}}

	v := viewCart(c)
	if v.FreeShippingFrom == nil || *v.FreeShippingFrom != 8000 {
		t.Errorf("FreeShippingFrom = %v, want 8000", v.FreeShippingFrom)
	}
	if len(v.Products) != 1 {
		t.Errorf("products = %d, want 1", len(v.Products))
	}
}

func TestProductCard(t *testing.T) {
	p := &api.Product{
		CanonicalID:      "p1",
		Slug:             "tomate-grappe",
		Name:             "Tomate Grappe",
		ItemPrice:        320,
		Origin:           "France",
		ShortDescription: "Belle tomate",
		AvailableQuantity: 7,
	}
	promo := &api.Promo{}
	promo.Conditions.Type = "PERCENT"
	promo.Conditions.Value = 10
	p.Promo = promo

	card := productCard(p, "best value")
	if card.CanonicalID != "p1" || card.Slug != "tomate-grappe" || card.Name != "Tomate Grappe" {
		t.Errorf("card identity wrong: %+v", card)
	}
	if card.PriceCents != 320 || card.Stock != 7 || card.Note != "best value" {
		t.Errorf("card fields wrong: %+v", card)
	}
	if card.Promo != "[promo -10%]" {
		t.Errorf("card promo = %q", card.Promo)
	}
}

func TestOutcomeLine(t *testing.T) {
	tests := []struct {
		name string
		oc   ops.ItemOutcome
		want []string // substrings that must appear
	}{
		{
			"updated",
			ops.ItemOutcome{Status: ops.StatusUpdated, Name: "Tomate", CanonicalID: "p1", Final: 3, UnitPrice: 200},
			[]string{"✓", "Tomate", "now 3", "2,00 €"},
		},
		{
			"clamped",
			ops.ItemOutcome{Status: ops.StatusClamped, Name: "Tomate", CanonicalID: "p1", Available: 3, Final: 3, Requested: 10},
			[]string{"⚠", "only 3 available", "wanted 10"},
		},
		{
			"removed",
			ops.ItemOutcome{Status: ops.StatusRemoved, Name: "Tomate", CanonicalID: "p1"},
			[]string{"✓", "removed"},
		},
		{
			"out of stock",
			ops.ItemOutcome{Status: ops.StatusOutOfStock, Name: "Tomate", CanonicalID: "p1"},
			[]string{"✗", "out of stock"},
		},
		{
			"stale id",
			ops.ItemOutcome{Status: ops.StatusStaleID, Name: "Tomate", CanonicalID: "p1"},
			[]string{"✗", "stale"},
		},
		{
			"not found names the query",
			ops.ItemOutcome{Status: ops.StatusNotFound, Via: "search:licorne"},
			[]string{"✗", "search:licorne", "no products found"},
		},
		{
			"planned",
			ops.ItemOutcome{Status: ops.StatusPlanned, Name: "Tomate", CanonicalID: "p1", Requested: 2, UnitPrice: 200},
			[]string{"·", "2 ×", "Tomate"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := outcomeLine(tc.oc)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("outcomeLine = %q, missing %q", got, want)
				}
			}
		})
	}
}

// A search-resolved pick annotates with [picked via ...] and lists alternates.
func TestOutcomeLineSearchAnnotation(t *testing.T) {
	oc := ops.ItemOutcome{
		Status: ops.StatusUpdated, Name: "Lait", CanonicalID: "a", Final: 1, UnitPrice: 100,
		Via:        "search:lait",
		Alternates: []ops.Alt{{CanonicalID: "b", Name: "Lait B", ItemPrice: 110}},
	}
	got := outcomeLine(oc)
	if !strings.Contains(got, "[picked via search:lait]") {
		t.Errorf("missing pick annotation: %q", got)
	}
	if !strings.Contains(got, "alt: Lait B") {
		t.Errorf("missing alternate line: %q", got)
	}
}
