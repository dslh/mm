package ops

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dslh/mm/internal/api"
)

// fakeMM is a scriptable stand-in for the mon-marché API: it serves a cart that
// mutates on PATCH /cart/product, canned search results, and order detail.
type fakeMM struct {
	cart        map[string]cartLine // canonicalId -> line
	avail       map[string]int      // canonicalId -> availableQuantity
	searchHits  map[string][]api.Product
	order       *api.Order
	patchErr    map[string]string // canonicalId -> error code to return on PATCH
	cartMissing bool              // GET /cart returns E_08_0005
}

type cartLine struct {
	name  string
	price float64
	count int
}

func newFake() *fakeMM {
	return &fakeMM{
		cart:       map[string]cartLine{},
		avail:      map[string]int{},
		searchHits: map[string][]api.Product{},
		patchErr:   map[string]string{},
	}
}

func (f *fakeMM) cartJSON() string {
	var products []string
	for id, ln := range f.cart {
		products = append(products, `{
			"canonicalId": "`+id+`",
			"name": "`+ln.name+`",
			"itemPrice": `+jsonNum(ln.price)+`,
			"availableQuantity": `+jsonNum(float64(f.avail[id]))+`,
			"quotation": {"editable": true, "count": `+jsonNum(float64(ln.count))+`}
		}`)
	}
	return `{"id":"c1","products":[` + strings.Join(products, ",") + `],
		"price":{"quotation":{"net":0,"currency":"EUR"}},
		"delivery":{"address":{"location":{},"addressComponents":{}}}}`
}

func jsonNum(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

func (f *fakeMM) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/cart":
			if f.cartMissing {
				w.WriteHeader(404)
				w.Write([]byte(`{"message":"no cart","code":"E_08_0005"}`))
				return
			}
			w.Write([]byte(f.cartJSON()))

		case r.Method == "GET" && r.URL.Path == "/api/orders/past":
			w.Write([]byte(`{"count":0,"items":[]}`)) // auth probe target

		case r.Method == "PATCH" && r.URL.Path == "/api/cart/product":
			var body struct {
				Product struct {
					ID       string `json:"id"`
					Quantity int    `json:"quantity"`
				} `json:"product"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			id := body.Product.ID
			if code, ok := f.patchErr[id]; ok {
				w.WriteHeader(400)
				w.Write([]byte(`{"message":"err","code":"` + code + `"}`))
				return
			}
			ln := f.cart[id]
			if body.Product.Quantity == 0 {
				delete(f.cart, id)
			} else {
				ln.count = body.Product.Quantity
				if ln.name == "" {
					ln.name = "Item " + id
				}
				f.cart[id] = ln
			}
			w.Write([]byte(f.cartJSON()))

		case r.Method == "GET" && r.URL.Path == "/api/search2":
			q := r.URL.Query().Get("text")
			hits := f.searchHits[q]
			out, _ := json.Marshal(api.SearchResponse{Count: len(hits), Items: hits})
			w.Write(out)

		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/orders/"):
			out, _ := json.Marshal(f.order)
			w.Write(out)

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}
}

func (f *fakeMM) ops(t *testing.T) *Ops {
	t.Helper()
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	return &Ops{API: api.NewTestClient(srv.URL+"/api", "tok")}
}

func intp(n int) *int { return &n }

func TestApplyAddByIDIncrements(t *testing.T) {
	f := newFake()
	f.cart["p1"] = cartLine{name: "Tomate", price: 200, count: 1}
	f.avail["p1"] = 10
	o := f.ops(t)

	cart, outcomes, err := o.Apply(context.Background(), []ApplyLine{{ID: "p1", N: intp(2)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || outcomes[0].Status != StatusUpdated {
		t.Fatalf("outcome = %+v", outcomes)
	}
	if outcomes[0].Final != 3 {
		t.Errorf("final qty = %d, want 3 (1 existing + 2)", outcomes[0].Final)
	}
	if cart.Quantity("p1") != 3 {
		t.Errorf("cart qty = %d, want 3", cart.Quantity("p1"))
	}
}

func TestApplyClampsToStock(t *testing.T) {
	f := newFake()
	f.cart["p1"] = cartLine{name: "Tomate", price: 200, count: 0}
	delete(f.cart, "p1") // not in cart yet
	f.avail["p1"] = 3
	// Seed availability via a search hit so applyOne learns stock before PATCH.
	f.searchHits["tomate"] = []api.Product{
		{CanonicalID: "p1", Name: "Tomate", ItemPrice: 200, AvailableQuantity: 3, Type: "PRODUCT"},
	}
	o := f.ops(t)

	_, outcomes, err := o.Apply(context.Background(), []ApplyLine{{Query: "tomate", Set: intp(10)}})
	if err != nil {
		t.Fatal(err)
	}
	oc := outcomes[0]
	if oc.Status != StatusClamped {
		t.Fatalf("status = %s, want clamped (%+v)", oc.Status, oc)
	}
	if oc.Final != 3 || oc.Requested != 10 || oc.Available != 3 {
		t.Errorf("clamp wrong: final=%d requested=%d available=%d", oc.Final, oc.Requested, oc.Available)
	}
}

func TestApplyOutOfStock(t *testing.T) {
	f := newFake()
	f.searchHits["rare"] = []api.Product{
		{CanonicalID: "p9", Name: "Rare", ItemPrice: 500, AvailableQuantity: 0, Type: "PRODUCT"},
	}
	o := f.ops(t)
	_, outcomes, err := o.Apply(context.Background(), []ApplyLine{{Query: "rare", N: intp(1)}})
	if err != nil {
		t.Fatal(err)
	}
	if outcomes[0].Status != StatusOutOfStock {
		t.Errorf("status = %s, want out-of-stock", outcomes[0].Status)
	}
}

func TestApplySearchNotFound(t *testing.T) {
	f := newFake()
	o := f.ops(t)
	_, outcomes, err := o.Apply(context.Background(), []ApplyLine{{Query: "unobtainium", N: intp(1)}})
	if err != nil {
		t.Fatal(err)
	}
	if outcomes[0].Status != StatusNotFound {
		t.Errorf("status = %s, want not-found", outcomes[0].Status)
	}
}

func TestApplySearchPicksFirstWithAlternates(t *testing.T) {
	f := newFake()
	f.searchHits["lait"] = []api.Product{
		{CanonicalID: "a", Name: "Lait A", ItemPrice: 100, AvailableQuantity: 5, Type: "PRODUCT"},
		{CanonicalID: "b", Name: "Lait B", ItemPrice: 110, AvailableQuantity: 5, Type: "PRODUCT"},
		{CanonicalID: "c", Name: "Lait C", ItemPrice: 120, AvailableQuantity: 5, Type: "PRODUCT"},
		{CanonicalID: "d", Name: "Lait D", ItemPrice: 130, AvailableQuantity: 5, Type: "PRODUCT"},
	}
	o := f.ops(t)
	_, outcomes, err := o.Apply(context.Background(), []ApplyLine{{Query: "lait", N: intp(1)}})
	if err != nil {
		t.Fatal(err)
	}
	oc := outcomes[0]
	if oc.CanonicalID != "a" || oc.Status != StatusUpdated {
		t.Fatalf("picked wrong: %+v", oc)
	}
	if len(oc.Alternates) != 2 { // alternates capped at next 2 (indices 1..3 -> min(3,len))
		t.Errorf("alternates = %d, want 2", len(oc.Alternates))
	}
	if oc.Via != "search:lait" {
		t.Errorf("via = %q", oc.Via)
	}
}

// Non-product feed entries (recipes) must be filtered out of search resolution.
func TestApplySearchSkipsNonProducts(t *testing.T) {
	f := newFake()
	f.searchHits["soupe"] = []api.Product{
		{CanonicalID: "r1", Name: "Recette", Type: "RECIPE"},
		{CanonicalID: "p1", Name: "Soupe", ItemPrice: 300, AvailableQuantity: 4, Type: "PRODUCT"},
	}
	o := f.ops(t)
	_, outcomes, err := o.Apply(context.Background(), []ApplyLine{{Query: "soupe", N: intp(1)}})
	if err != nil {
		t.Fatal(err)
	}
	if outcomes[0].CanonicalID != "p1" {
		t.Errorf("picked %s, want p1 (recipe should be skipped)", outcomes[0].CanonicalID)
	}
}

func TestApplyStaleID(t *testing.T) {
	f := newFake()
	f.avail["gone"] = 5 // pretend available, but PATCH rejects as stale
	f.patchErr["gone"] = api.CodeStaleProduct
	o := f.ops(t)
	_, outcomes, err := o.Apply(context.Background(), []ApplyLine{{ID: "gone", N: intp(1)}})
	if err != nil {
		t.Fatal(err)
	}
	if outcomes[0].Status != StatusStaleID {
		t.Errorf("status = %s, want stale-id", outcomes[0].Status)
	}
}

func TestApplyRemoveViaSetZero(t *testing.T) {
	f := newFake()
	f.cart["p1"] = cartLine{name: "Tomate", price: 200, count: 4}
	f.avail["p1"] = 10
	o := f.ops(t)
	cart, outcomes, err := o.Apply(context.Background(), []ApplyLine{{ID: "p1", Set: intp(0)}})
	if err != nil {
		t.Fatal(err)
	}
	if outcomes[0].Status != StatusRemoved {
		t.Errorf("status = %s, want removed", outcomes[0].Status)
	}
	if cart.Quantity("p1") != 0 {
		t.Errorf("cart still has p1: %d", cart.Quantity("p1"))
	}
}

// A negative net (more decrement than in cart) clamps to 0, removing the line.
func TestApplyNegativeClampsToZero(t *testing.T) {
	f := newFake()
	f.cart["p1"] = cartLine{name: "Tomate", price: 200, count: 1}
	f.avail["p1"] = 10
	o := f.ops(t)
	_, outcomes, err := o.Apply(context.Background(), []ApplyLine{{ID: "p1", N: intp(-5)}})
	if err != nil {
		t.Fatal(err)
	}
	if outcomes[0].Status != StatusRemoved {
		t.Errorf("status = %s, want removed", outcomes[0].Status)
	}
}

func TestApplyLineNeedsExactlyOneSelector(t *testing.T) {
	f := newFake()
	o := f.ops(t)
	for _, ln := range []ApplyLine{
		{N: intp(1)},                      // neither id nor query
		{ID: "x", Query: "y", N: intp(1)}, // both
	} {
		_, outcomes, err := o.Apply(context.Background(), []ApplyLine{ln})
		if err != nil {
			t.Fatal(err)
		}
		if outcomes[0].Status != StatusError {
			t.Errorf("line %+v: status = %s, want error", ln, outcomes[0].Status)
		}
	}
}

func TestApplyBatchSequential(t *testing.T) {
	f := newFake()
	f.cart["p1"] = cartLine{name: "Tomate", price: 200, count: 0}
	delete(f.cart, "p1")
	f.avail["p1"] = 10
	f.avail["p2"] = 10
	f.searchHits["pomme"] = []api.Product{
		{CanonicalID: "p2", Name: "Pomme", ItemPrice: 150, AvailableQuantity: 10, Type: "PRODUCT"},
	}
	o := f.ops(t)
	cart, outcomes, err := o.Apply(context.Background(), []ApplyLine{
		{ID: "p1", N: intp(2)},
		{Query: "pomme", N: intp(3)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 2 {
		t.Fatalf("outcomes = %d, want 2", len(outcomes))
	}
	if cart.Quantity("p1") != 2 || cart.Quantity("p2") != 3 {
		t.Errorf("batch result wrong: p1=%d p2=%d", cart.Quantity("p1"), cart.Quantity("p2"))
	}
}

func TestGetCartTreatsMissingAsEmpty(t *testing.T) {
	f := newFake()
	f.cartMissing = true // 404 E_08_0005, but auth probe (orders/past) succeeds
	o := f.ops(t)
	cart, err := o.GetCart(context.Background())
	if err != nil {
		t.Fatalf("GetCart: %v", err)
	}
	if len(cart.Products) != 0 {
		t.Errorf("want empty cart, got %d products", len(cart.Products))
	}
}

func TestReorderDryRun(t *testing.T) {
	f := newFake()
	f.order = &api.Order{ID: "o1", Products: []api.OrderProduct{
		{CanonicalID: "p1", Name: "Tomate", ItemPrice: 200},
		{CanonicalID: "p2", Name: "Pomme", ItemPrice: 150},
	}}
	f.order.Products[0].Quotation.Count = 2
	f.order.Products[1].Quotation.Count = 3
	o := f.ops(t)

	order, cart, outcomes, err := o.Reorder(context.Background(), "o1", true)
	if err != nil {
		t.Fatal(err)
	}
	if cart != nil {
		t.Error("dry run must not touch the cart")
	}
	if order.ID != "o1" || len(outcomes) != 2 {
		t.Fatalf("order=%v outcomes=%d", order, len(outcomes))
	}
	for _, oc := range outcomes {
		if oc.Status != StatusPlanned || oc.Via != "order" {
			t.Errorf("outcome = %+v, want planned/order", oc)
		}
	}
	if outcomes[0].Requested != 2 {
		t.Errorf("planned qty = %d, want 2", outcomes[0].Requested)
	}
}

func TestReorderAppliesIncrementally(t *testing.T) {
	f := newFake()
	f.cart["p1"] = cartLine{name: "Tomate", price: 200, count: 1} // already 1 in cart
	f.avail["p1"] = 10
	f.avail["p2"] = 10
	f.order = &api.Order{ID: "o1", Products: []api.OrderProduct{
		{CanonicalID: "p1", Name: "Tomate", ItemPrice: 200},
		{CanonicalID: "p2", Name: "Pomme", ItemPrice: 150},
	}}
	f.order.Products[0].Quotation.Count = 2
	f.order.Products[1].Quotation.Count = 1
	o := f.ops(t)

	_, cart, outcomes, err := o.Reorder(context.Background(), "o1", false)
	if err != nil {
		t.Fatal(err)
	}
	if cart.Quantity("p1") != 3 { // 1 existing + 2 from order
		t.Errorf("p1 = %d, want 3 (incremental)", cart.Quantity("p1"))
	}
	if cart.Quantity("p2") != 1 {
		t.Errorf("p2 = %d, want 1", cart.Quantity("p2"))
	}
	for _, oc := range outcomes {
		if oc.Via != "order" {
			t.Errorf("via = %q, want order", oc.Via)
		}
	}
}

// A stale line in a reorder gets search-based alternates attached, never an
// auto-substitution.
func TestReorderStaleLineSuggestsAlternates(t *testing.T) {
	f := newFake()
	f.avail["gone"] = 5
	f.patchErr["gone"] = api.CodeStaleProduct
	f.searchHits["Vieux Produit"] = []api.Product{
		{CanonicalID: "new1", Name: "Nouveau", ItemPrice: 210, AvailableQuantity: 5, Type: "PRODUCT"},
		{CanonicalID: "new2", Name: "Autre", ItemPrice: 220, AvailableQuantity: 5, Type: "PRODUCT"},
	}
	f.order = &api.Order{ID: "o1", Products: []api.OrderProduct{
		{CanonicalID: "gone", Name: "Vieux Produit", ItemPrice: 200},
	}}
	f.order.Products[0].Quotation.Count = 1
	o := f.ops(t)

	_, _, outcomes, err := o.Reorder(context.Background(), "o1", false)
	if err != nil {
		t.Fatal(err)
	}
	oc := outcomes[0]
	if oc.Status != StatusStaleID {
		t.Fatalf("status = %s, want stale-id", oc.Status)
	}
	if len(oc.Alternates) != 2 {
		t.Errorf("alternates = %d, want 2 suggestions", len(oc.Alternates))
	}
}
