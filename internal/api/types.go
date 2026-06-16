package api

import (
	"encoding/json"
	"fmt"
	"strings"
)

// All prices are cents, occasionally fractional — always float64 (docs/api.md).
// Epoch timestamps are milliseconds.

type Location struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// Product is the shape shared by /search2 items, /category items and
// /articleDetailBySlug. Category items omit granularity, hence the pointer.
type Product struct {
	Type              string  `json:"type"` // PRODUCT | RECIPE | CUSTOM_CONTENT in mixed feeds
	ID                string  `json:"id"`   // compound "<canonicalId>$<shop>" — display only
	CanonicalID       string  `json:"canonicalId"`
	SKU               string  `json:"sku"`
	Name              string  `json:"name"`
	Slug              string  `json:"slug"`
	Origin            string  `json:"origin"`
	AvailableQuantity int     `json:"availableQuantity"` // session/zone-scoped; never cache
	ItemPrice         float64 `json:"itemPrice"`         // cents; may be fractional
	Granularity       *struct {
		Singular string `json:"singular"`
		Plural   string `json:"plural"`
	} `json:"granularity"`
	ItemDefinition struct {
		Type   string `json:"type"` // arbitraryQuantity | pieceWeight
		Weight *struct {
			Value float64 `json:"value"`
			Unit  string  `json:"unit"`
		} `json:"weight"`
	} `json:"itemDefinition"`
	Pricing struct {
		SellPrices struct {
			PerWeightUnit *struct {
				Net   float64 `json:"net"` // can be fractional cents (e.g. 824.25 per kg)
				Unit  string  `json:"unit"`
				Value float64 `json:"value"`
				Main  bool    `json:"main"`
			} `json:"perWeightUnit"`
		} `json:"sellPrices"`
	} `json:"pricing"`
	Promo            *Promo      `json:"promo"`
	ShortDescription string      `json:"shortDescription"`
	Attributes       []Attribute `json:"attributes"`
	// Images is only populated by /articleDetailBySlug; the search2/category
	// feed omits it (verified 2026-06-15), so a card thumbnail needs the detail
	// fetch. URLs are Cloudinary; see ThumbnailURL for the resized crop.
	Images []ProductImage `json:"images"`
}

// ProductImage is one entry of a product's `images[]` (detail endpoint only).
// Beyond url there is a name/alt and a `formats[]` list of named crops, which
// we ignore — Cloudinary lets us request any size from the base url directly.
type ProductImage struct {
	ID  string `json:"id"`
	URL string `json:"url"`
	Alt string `json:"alt"`
}

// ThumbnailURL returns the first product image resized to a px×px padded crop
// via Cloudinary transform params, or "" when the product carries no image.
// The transform is injected after the "/upload/" marker, the documented place
// for Cloudinary delivery options.
func (p *Product) ThumbnailURL(px int) string {
	if len(p.Images) == 0 || p.Images[0].URL == "" {
		return ""
	}
	const marker = "/upload/"
	raw := p.Images[0].URL
	i := strings.Index(raw, marker)
	if i < 0 {
		return raw // not a recognized Cloudinary url; hand back unmodified
	}
	i += len(marker)
	t := fmt.Sprintf("c_pad,b_white,f_auto,q_auto,w_%d,h_%d/", px, px)
	return raw[:i] + t + raw[i:]
}

// UnitWeight is the per-piece weight label for cards ("140 g", "1,5 kg"), from
// itemDefinition.weight; "" when the product is not sold by piece weight.
func (p *Product) UnitWeight() string {
	w := p.ItemDefinition.Weight
	if w == nil || w.Value <= 0 {
		return ""
	}
	if w.Unit == "kg" && w.Value < 1 {
		return fmt.Sprintf("%g g", w.Value*1000)
	}
	return fmt.Sprintf("%g %s", w.Value, w.Unit)
}

// Attribute is one entry of the product's `attributes[]` array. In the
// search/category feed this is a short list (origin + an editorial tag); in
// /articleDetailBySlug it's the full PIM matrix (allergens, nutrition,
// ingredients, …). We decode it generically and pick fields out by key.
type Attribute struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Value string `json:"value"`
}

// UnmarshalJSON coerces `value` to a string. The detail endpoint sends numeric
// values for nutrition rows (e.g. `"value": 20`) while the feed sends strings
// (e.g. `"value": "Sans nitrite"`); decode either without erroring.
func (a *Attribute) UnmarshalJSON(b []byte) error {
	var raw struct {
		Key   string          `json:"key"`
		Label string          `json:"label"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	a.Key, a.Label = raw.Key, raw.Label
	v := string(raw.Value)
	if len(v) > 0 && v[0] == '"' {
		if err := json.Unmarshal(raw.Value, &a.Value); err != nil {
			return err
		}
	} else if v != "null" {
		a.Value = v // number/bool — keep its literal text
	}
	return nil
}

// SpecificTag returns the editorial "specific-tag" badge value — the small
// pill mon-marché shows on product cards (e.g. "Sans nitrite", "Nouveau",
// "BIO") — or "" when the product has none. Lives in attributes[] under
// key "specific-tag".
func (p *Product) SpecificTag() string {
	for _, a := range p.Attributes {
		if a.Key == "specific-tag" {
			return a.Value
		}
	}
	return ""
}

// PriceUnit is the denominator of the display price ("/kg", "/pièce").
func (p *Product) PriceUnit() string {
	if pw := p.Pricing.SellPrices.PerWeightUnit; pw != nil && pw.Main {
		if pw.Value == 1 {
			return "/" + pw.Unit
		}
		return fmt.Sprintf("/%g%s", pw.Value, pw.Unit)
	}
	if g := p.Granularity; g != nil && g.Singular != "" {
		return "/" + g.Singular
	}
	return ""
}

type Promo struct {
	Mechanism         string  `json:"mechanism"`
	ItemOriginalPrice float64 `json:"itemOriginalPrice"`
	Conditions        struct {
		Type        string  `json:"type"`  // PERCENT | VALUE
		Value       float64 `json:"value"` // percent points, or cents off, per Type
		NthQuantity int     `json:"nthQuantity"`
	} `json:"conditions"`
}

type SearchResponse struct {
	Count int       `json:"count"`
	Items []Product `json:"items"`
	Next  string    `json:"next,omitempty"` // opaque cursor; empty on the last page
}

type Navigation struct {
	Families []struct {
		ID         string        `json:"id"`
		Name       string        `json:"name"`
		Link       string        `json:"link"`
		Categories []NavCategory `json:"categories"`
	} `json:"families"`
}

type NavCategory struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Slug     string        `json:"slug"`
	Children []NavCategory `json:"children"`
}

type Category struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Slug          string        `json:"slug"`
	Subcategories []Subcategory `json:"subcategories"`
	Items         []Product     `json:"items"` // mixed feed; see Products()
	Parent        *struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	} `json:"parent"`
}

type Subcategory struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Slug      string `json:"slug"`
	ItemCount int    `json:"itemCount"`
}

// Products filters the mixed item feed (PRODUCT | RECIPE | CUSTOM_CONTENT).
func (c *Category) Products() []Product {
	var out []Product
	for _, it := range c.Items {
		if it.Type == "PRODUCT" {
			out = append(out, it)
		}
	}
	return out
}

type ArticleDetail struct {
	Product
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

// Cart deliberately types only non-PII fields: customer identity, address
// text and delivery notes are never decoded. The address location/postal code
// are kept because /addresses/deliverySlots2 needs them as request input;
// renderers must not print them.
type Cart struct {
	ID                    string        `json:"id"`
	Products              []CartProduct `json:"products"`
	Price                 CartPrice     `json:"price"`
	MinOrderAmountReached bool          `json:"minOrderAmountReached"`
	Delivery              CartDelivery  `json:"delivery"`

	// RawDelivery is the verbatim `delivery` JSON object. It holds the customer's
	// delivery PII (access note, full address) as opaque bytes that are *never*
	// decoded into named fields here, so the note/address values never enter a Go
	// string that could be logged or printed. Its sole use is replaying the
	// {note,address} into PATCH /cart/delivery2 (slot selection). `json:"-"` keeps
	// it out of any re-marshal (e.g. --json output); it lives only in memory.
	RawDelivery json.RawMessage `json:"-"`
}

func (c *Cart) UnmarshalJSON(b []byte) error {
	type alias Cart // shed the custom method to avoid infinite recursion
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*c = Cart(a)
	var probe struct {
		Delivery json.RawMessage `json:"delivery"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return err
	}
	c.RawDelivery = probe.Delivery
	return nil
}

type CartDelivery struct {
	Address struct {
		Location Location `json:"location"`
		// Only the two components the deliverySlots2 request needs;
		// street/city/formattedAddress are deliberately not decoded.
		AddressComponents struct {
			PostalCode  string `json:"postalCode"`
			CountryCode string `json:"countryCode"`
		} `json:"addressComponents"`
	} `json:"address"`
	DeliveryPrices []struct {
		MinCartNetPrice float64 `json:"minCartNetPrice"`
		ShippingAmount  float64 `json:"shippingAmount"`
	} `json:"deliveryPrices"`
}

type CartProduct struct {
	CanonicalID       string  `json:"canonicalId"`
	SKU               string  `json:"sku"`
	Name              string  `json:"name"`
	ItemPrice         float64 `json:"itemPrice"` // cents; may be fractional
	AvailableQuantity int     `json:"availableQuantity"`
	Quotation         struct {
		Editable bool `json:"editable"`
		Count    int  `json:"count"`
	} `json:"quotation"`
	Promo *Promo `json:"promo"`
}

type CartPrice struct {
	Quotation struct {
		Net              float64 `json:"net"`
		DutyFree         float64 `json:"dutyFree"`
		VAT              float64 `json:"vat"`
		Shipping         float64 `json:"shipping"`
		PreparationFee   float64 `json:"preparationFee"`
		Preauthorization float64 `json:"preauthorization"`
		Discount         float64 `json:"discount"`
		PromoSavings     float64 `json:"promoSavings"`
		Currency         string  `json:"currency"`
	} `json:"quotation"`
}

// Quantity returns the cart quantity for a canonicalId (0 if absent).
func (c *Cart) Quantity(canonicalID string) int {
	for i := range c.Products {
		if c.Products[i].CanonicalID == canonicalID {
			return c.Products[i].Quotation.Count
		}
	}
	return 0
}

// FreeShippingThreshold is the cheapest cart net total that ships free,
// per the delivery price tiers (80,00 € as of docs/api.md; a cart not yet
// bound to a slot shows a single 0/0 placeholder tier). -1 when unknown.
func (c *Cart) FreeShippingThreshold() float64 {
	thr, found := 0.0, false
	for _, dp := range c.Delivery.DeliveryPrices {
		if dp.ShippingAmount == 0 && (!found || dp.MinCartNetPrice < thr) {
			thr, found = dp.MinCartNetPrice, true
		}
	}
	if !found {
		return -1
	}
	return thr
}

type OrdersPast struct {
	Count int            `json:"count"`
	Items []OrderSummary `json:"items"`
}

type OrderSummary struct {
	ID         string  `json:"id"`
	State      string  `json:"state"`
	TotalPrice float64 `json:"totalPrice"`
	Delivery   struct {
		TimeSlot *OrderTimeSlot `json:"timeSlot"`
	} `json:"delivery"`
	Products []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"products"`
}

type OrderTimeSlot struct {
	From       int64 `json:"from"`
	To         int64 `json:"to"`
	OrderUntil int64 `json:"orderUntil"`
}

// Order is the full order detail; like Cart it skips all PII fields
// (customer, delivery address/note), keeping only the time slot.
type Order struct {
	ID            string    `json:"id"`
	State         string    `json:"state"`
	ArticlesCount int       `json:"articlesCount"`
	Price         CartPrice `json:"price"`
	Delivery      struct {
		TimeSlot *OrderTimeSlot `json:"timeSlot"`
	} `json:"delivery"`
	Products []OrderProduct `json:"products"`
}

type OrderProduct struct {
	CanonicalID string  `json:"canonicalId"`
	Name        string  `json:"name"`
	ItemPrice   float64 `json:"itemPrice"`
	Quotation   struct {
		Count int `json:"count"`
	} `json:"quotation"`
}

type DeliverySlotsResponse struct {
	DeliveryZones []DeliveryZone `json:"deliveryZones"`
}

type DeliveryZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	Shop struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"shop"`
	MinOrderAmount float64        `json:"minOrderAmount"` // zone minimum, distinct from free shipping
	DeliverySlots  []DeliverySlot `json:"deliverySlots"`
}

type DeliverySlot struct {
	ID           string `json:"id"`
	From         int64  `json:"from"`
	To           int64  `json:"to"`
	OrderUntil   int64  `json:"orderUntil"` // order-by deadline for this window
	DeliveryMode string `json:"deliveryMode"`
	IsFull       bool   `json:"isFull"`
	IsExpired    bool   `json:"isExpired"`
	IsExcluded   bool   `json:"isExcluded"`
	ExtraPrice   struct {
		Currency string  `json:"currency"`
		DutyFree float64 `json:"dutyFree"`
	} `json:"extraPrice"`

	// Raw is the verbatim slot JSON, replayed into PATCH /cart/delivery2 so the
	// selection request carries every field the server sent (daysLimit, rate,
	// deliveryPricesWithDeltas, …), not just the subset modeled above. Not PII;
	// `json:"-"` only because it would duplicate the typed fields in --json output.
	Raw json.RawMessage `json:"-"`
}

func (s *DeliverySlot) UnmarshalJSON(b []byte) error {
	type alias DeliverySlot
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*s = DeliverySlot(a)
	s.Raw = append(json.RawMessage(nil), b...)
	return nil
}

func (s *DeliverySlot) Selectable() bool { return !s.IsFull && !s.IsExpired && !s.IsExcluded }
