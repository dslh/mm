package ops

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dslh/mm/internal/api"
)

// slotsFake serves a cart with a delivery address plus a deliverySlots2 list,
// and captures the body sent to PATCH /cart/delivery2 so the test can assert on
// the replay payload.
type slotsFake struct {
	cartJSON     string
	slotsJSON    string
	deliveryBody []byte // captured PATCH /cart/delivery2 body
}

func (f *slotsFake) ops(t *testing.T) *Ops {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/cart":
			io.WriteString(w, f.cartJSON)
		case r.Method == "GET" && r.URL.Path == "/api/orders/past":
			io.WriteString(w, `{"count":0,"items":[]}`)
		case r.Method == "POST" && r.URL.Path == "/api/addresses/deliverySlots2":
			io.WriteString(w, f.slotsJSON)
		case r.Method == "PATCH" && r.URL.Path == "/api/cart/delivery2":
			f.deliveryBody, _ = io.ReadAll(r.Body)
			io.WriteString(w, f.cartJSON) // echo updated cart
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(srv.Close)
	return &Ops{API: api.NewTestClient(srv.URL+"/api", "tok")}
}

const cartWithAddress = `{
	"id": "c1", "products": [],
	"price": {"quotation": {"net": 0, "currency": "EUR"}},
	"delivery": {
		"note": "code 1234",
		"address": {
			"location": {"lat": 48.85, "lng": 2.35},
			"addressComponents": {"postalCode": "75011", "countryCode": "FR"}
		}
	}
}`

const cartNoAddress = `{
	"id": "c1", "products": [],
	"price": {"quotation": {"net": 0, "currency": "EUR"}},
	"delivery": {"address": {"location": {}, "addressComponents": {}}}
}`

const slotsList = `{"deliveryZones": [{
	"id": "z1", "name": "Paris", "deliverySlots": [
		{"id": "open1", "from": 1000, "to": 2000, "isFull": false},
		{"id": "full1", "from": 3000, "to": 4000, "isFull": true}
	]
}]}`

func TestSlotsNeedsAddress(t *testing.T) {
	f := &slotsFake{cartJSON: cartNoAddress}
	if _, err := f.ops(t).Slots(context.Background()); err == nil {
		t.Error("want error when cart has no delivery address")
	}
}

func TestSlotsListsWindows(t *testing.T) {
	f := &slotsFake{cartJSON: cartWithAddress, slotsJSON: slotsList}
	res, err := f.ops(t).Slots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.DeliveryZones) != 1 || len(res.DeliveryZones[0].DeliverySlots) != 2 {
		t.Fatalf("unexpected slots: %+v", res)
	}
}

func TestSelectSlotEmptyID(t *testing.T) {
	f := &slotsFake{cartJSON: cartWithAddress}
	if _, _, err := f.ops(t).SelectSlot(context.Background(), ""); err == nil {
		t.Error("want error for empty slot id")
	}
}

func TestSelectSlotUnknownID(t *testing.T) {
	f := &slotsFake{cartJSON: cartWithAddress, slotsJSON: slotsList}
	_, _, err := f.ops(t).SelectSlot(context.Background(), "nope")
	if err == nil || !strings.Contains(err.Error(), "not among current windows") {
		t.Errorf("want not-among-windows error, got %v", err)
	}
}

func TestSelectSlotNotSelectable(t *testing.T) {
	f := &slotsFake{cartJSON: cartWithAddress, slotsJSON: slotsList}
	_, _, err := f.ops(t).SelectSlot(context.Background(), "full1")
	if err == nil || !strings.Contains(err.Error(), "not selectable") {
		t.Errorf("want not-selectable error, got %v", err)
	}
}

// TestSelectSlotReplaysDeliveryAndSlot is the key contract: the PATCH body must
// carry the cart's {note,address} verbatim plus the chosen slot, and the note
// PII must reach the wire only inside that replayed body (never decoded).
func TestSelectSlotReplaysDeliveryAndSlot(t *testing.T) {
	f := &slotsFake{cartJSON: cartWithAddress, slotsJSON: slotsList}
	_, chosen, err := f.ops(t).SelectSlot(context.Background(), "open1")
	if err != nil {
		t.Fatal(err)
	}
	if chosen.ID != "open1" {
		t.Errorf("chosen = %q, want open1", chosen.ID)
	}
	if f.deliveryBody == nil {
		t.Fatal("no PATCH body captured")
	}
	var body struct {
		Delivery struct {
			Note    json.RawMessage `json:"note"`
			Address json.RawMessage `json:"address"`
		} `json:"delivery"`
		TimeSlot struct {
			ID string `json:"id"`
		} `json:"timeSlot"`
	}
	if err := json.Unmarshal(f.deliveryBody, &body); err != nil {
		t.Fatalf("bad PATCH body: %v", err)
	}
	if body.TimeSlot.ID != "open1" {
		t.Errorf("replayed slot id = %q, want open1", body.TimeSlot.ID)
	}
	if !strings.Contains(string(body.Delivery.Note), "code 1234") {
		t.Errorf("note not replayed verbatim: %s", body.Delivery.Note)
	}
	if !strings.Contains(string(body.Delivery.Address), "75011") {
		t.Errorf("address not replayed: %s", body.Delivery.Address)
	}
}

func TestAddSetWrappers(t *testing.T) {
	f := newFake()
	f.avail["p1"] = 10
	o := f.ops(t)
	cart, _ := o.GetCart(context.Background())

	cart, oc, err := o.AddByID(context.Background(), cart, "p1", 2)
	if err != nil || oc.Status != StatusUpdated || cart.Quantity("p1") != 2 {
		t.Fatalf("AddByID: status=%s qty=%d err=%v", oc.Status, cart.Quantity("p1"), err)
	}
	cart, oc, err = o.SetByID(context.Background(), cart, "p1", 5)
	if err != nil || oc.Final != 5 {
		t.Fatalf("SetByID: final=%d err=%v", oc.Final, err)
	}

	f.searchHits["pomme"] = []api.Product{{CanonicalID: "p2", Name: "Pomme", ItemPrice: 150, AvailableQuantity: 9, Type: "PRODUCT"}}
	_, oc, err = o.AddByQuery(context.Background(), cart, "pomme", 1)
	if err != nil || oc.CanonicalID != "p2" {
		t.Fatalf("AddByQuery: id=%s err=%v", oc.CanonicalID, err)
	}
}
