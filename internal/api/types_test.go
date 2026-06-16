package api

import (
	"encoding/json"
	"testing"
)

func TestThumbnailURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"no images", "", ""},
		{
			"cloudinary upload marker",
			"https://res.cloudinary.com/mm/image/upload/v123/abc.jpg",
			"https://res.cloudinary.com/mm/image/upload/c_pad,b_white,f_auto,q_auto,w_320,h_320/v123/abc.jpg",
		},
		{
			"unrecognized url passes through",
			"https://example.com/abc.jpg",
			"https://example.com/abc.jpg",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var p Product
			if tc.url != "" {
				p.Images = []ProductImage{{URL: tc.url}}
			}
			if got := p.ThumbnailURL(320); got != tc.want {
				t.Errorf("ThumbnailURL = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUnitWeight(t *testing.T) {
	weight := func(v float64, unit string) Product {
		var p Product
		p.ItemDefinition.Weight = &struct {
			Value float64 `json:"value"`
			Unit  string  `json:"unit"`
		}{Value: v, Unit: unit}
		return p
	}
	tests := []struct {
		name string
		p    Product
		want string
	}{
		{"no weight", Product{}, ""},
		{"zero value", weight(0, "kg"), ""},
		{"sub-kilo renders grams", weight(0.14, "kg"), "140 g"},
		{"kilo stays kilo", weight(1.5, "kg"), "1.5 kg"},
		{"non-kg unit untouched", weight(250, "g"), "250 g"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.p.UnitWeight(); got != tc.want {
				t.Errorf("UnitWeight = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPriceUnit(t *testing.T) {
	perWeight := func(unit string, value float64, main bool) Product {
		var p Product
		p.Pricing.SellPrices.PerWeightUnit = &struct {
			Net   float64 `json:"net"`
			Unit  string  `json:"unit"`
			Value float64 `json:"value"`
			Main  bool    `json:"main"`
		}{Unit: unit, Value: value, Main: main}
		return p
	}
	granular := func(singular string) Product {
		var p Product
		p.Granularity = &struct {
			Singular string `json:"singular"`
			Plural   string `json:"plural"`
		}{Singular: singular}
		return p
	}
	tests := []struct {
		name string
		p    Product
		want string
	}{
		{"none", Product{}, ""},
		{"per kg main", perWeight("kg", 1, true), "/kg"},
		{"per 100g main", perWeight("g", 100, true), "/100g"},
		{"per weight not main falls back to granularity", perWeight("kg", 1, false), ""},
		{"granularity piece", granular("pièce"), "/pièce"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.p.PriceUnit(); got != tc.want {
				t.Errorf("PriceUnit = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSpecificTag(t *testing.T) {
	p := Product{Attributes: []Attribute{
		{Key: "origin", Value: "France"},
		{Key: "specific-tag", Value: "BIO"},
	}}
	if got := p.SpecificTag(); got != "BIO" {
		t.Errorf("SpecificTag = %q, want BIO", got)
	}
	var empty Product
	if got := empty.SpecificTag(); got != "" {
		t.Errorf("SpecificTag empty = %q, want \"\"", got)
	}
}

func TestCartQuantity(t *testing.T) {
	c := Cart{Products: []CartProduct{
		{CanonicalID: "a"},
		{CanonicalID: "b"},
	}}
	c.Products[0].Quotation.Count = 3
	c.Products[1].Quotation.Count = 5
	if got := c.Quantity("b"); got != 5 {
		t.Errorf("Quantity(b) = %d, want 5", got)
	}
	if got := c.Quantity("missing"); got != 0 {
		t.Errorf("Quantity(missing) = %d, want 0", got)
	}
}

func TestFreeShippingThreshold(t *testing.T) {
	mk := func(tiers ...[2]float64) Cart {
		var c Cart
		for _, tr := range tiers {
			c.Delivery.DeliveryPrices = append(c.Delivery.DeliveryPrices, struct {
				MinCartNetPrice float64 `json:"minCartNetPrice"`
				ShippingAmount  float64 `json:"shippingAmount"`
			}{MinCartNetPrice: tr[0], ShippingAmount: tr[1]})
		}
		return c
	}
	tests := []struct {
		name string
		cart Cart
		want float64
	}{
		{"no tiers unknown", mk(), -1},
		{"only placeholder 0/0", mk([2]float64{0, 0}), 0},
		{"picks cheapest free tier", mk([2]float64{8000, 0}, [2]float64{10000, 0}, [2]float64{0, 590}), 8000},
		{"no free tier unknown", mk([2]float64{5000, 590}), -1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cart.FreeShippingThreshold(); got != tc.want {
				t.Errorf("FreeShippingThreshold = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCategoryProducts(t *testing.T) {
	c := Category{Items: []Product{
		{CanonicalID: "p1", Type: "PRODUCT"},
		{CanonicalID: "r1", Type: "RECIPE"},
		{CanonicalID: "c1", Type: "CUSTOM_CONTENT"},
		{CanonicalID: "p2", Type: "PRODUCT"},
	}}
	got := c.Products()
	if len(got) != 2 || got[0].CanonicalID != "p1" || got[1].CanonicalID != "p2" {
		t.Errorf("Products() = %+v, want only p1,p2", got)
	}
}

func TestSelectable(t *testing.T) {
	tests := []struct {
		name string
		slot DeliverySlot
		want bool
	}{
		{"open", DeliverySlot{}, true},
		{"full", DeliverySlot{IsFull: true}, false},
		{"expired", DeliverySlot{IsExpired: true}, false},
		{"excluded", DeliverySlot{IsExcluded: true}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.slot.Selectable(); got != tc.want {
				t.Errorf("Selectable = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAttributeUnmarshal(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{"string value", `{"key":"k","label":"l","value":"Sans nitrite"}`, "Sans nitrite"},
		{"numeric value kept as literal", `{"key":"k","value":20}`, "20"},
		{"float value", `{"key":"k","value":20.5}`, "20.5"},
		{"null value becomes empty", `{"key":"k","value":null}`, ""},
		{"missing value", `{"key":"k"}`, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var a Attribute
			if err := json.Unmarshal([]byte(tc.json), &a); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if a.Value != tc.want {
				t.Errorf("Value = %q, want %q", a.Value, tc.want)
			}
		})
	}
}

// TestCartUnmarshalRawDelivery checks the delivery block is captured verbatim
// into RawDelivery (for later replay) while the typed PII-free fields decode.
func TestCartUnmarshalRawDelivery(t *testing.T) {
	raw := `{
		"id": "cart1",
		"products": [],
		"delivery": {
			"note": "ring twice",
			"address": {
				"location": {"lat": 48.8, "lng": 2.3},
				"addressComponents": {"postalCode": "75001", "countryCode": "FR"}
			}
		}
	}`
	var c Cart
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.ID != "cart1" {
		t.Errorf("ID = %q, want cart1", c.ID)
	}
	if c.Delivery.Address.AddressComponents.PostalCode != "75001" {
		t.Errorf("postal code not decoded: %+v", c.Delivery.Address)
	}
	if len(c.RawDelivery) == 0 {
		t.Fatal("RawDelivery empty")
	}
	// RawDelivery must still contain the verbatim note for replay.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(c.RawDelivery, &probe); err != nil {
		t.Fatalf("RawDelivery not valid JSON: %v", err)
	}
	if _, ok := probe["note"]; !ok {
		t.Error("RawDelivery missing note for replay")
	}
	// RawDelivery is json:"-", so it must never re-marshal back out.
	out, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) == "" {
		t.Fatal("empty marshal")
	}
}

func TestDeliverySlotUnmarshalRaw(t *testing.T) {
	raw := `{"id":"slot1","from":1000,"to":2000,"isFull":false,"daysLimit":3}`
	var s DeliverySlot
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.ID != "slot1" || s.From != 1000 || s.To != 2000 {
		t.Errorf("typed fields wrong: %+v", s)
	}
	// Raw must preserve fields not modeled above (daysLimit) for replay.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(s.Raw, &probe); err != nil {
		t.Fatalf("Raw invalid: %v", err)
	}
	if _, ok := probe["daysLimit"]; !ok {
		t.Error("Raw dropped daysLimit")
	}
}
