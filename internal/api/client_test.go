package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestErrorTaxonomy(t *testing.T) {
	tests := []struct {
		code        string
		auth        bool
		cartMissing bool
		product     bool
	}{
		{"E_01_0001", true, false, false},
		{"E_08_0005", false, true, false},
		{"E_ECOM_01_0012", false, false, true},
		{"E_ECOM_01_0004", false, false, true},
		{"E_99_0000", false, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.code, func(t *testing.T) {
			e := &APIError{Code: tc.code}
			if e.IsAuth() != tc.auth {
				t.Errorf("IsAuth = %v, want %v", e.IsAuth(), tc.auth)
			}
			if e.IsCartNotFound() != tc.cartMissing {
				t.Errorf("IsCartNotFound = %v, want %v", e.IsCartNotFound(), tc.cartMissing)
			}
			if e.IsProduct() != tc.product {
				t.Errorf("IsProduct = %v, want %v", e.IsProduct(), tc.product)
			}
		})
	}
}

func TestAPIErrorString(t *testing.T) {
	e := &APIError{Status: 404, Code: "E_08_0005", Message: "no cart"}
	want := "no cart (HTTP 404, E_08_0005)"
	if got := e.Error(); got != want {
		t.Errorf("Error = %q, want %q", got, want)
	}
}

func TestDriftErrorString(t *testing.T) {
	e := &DriftError{Status: 500, Snippet: "boom", Cause: errors.New("decode")}
	got := e.Error()
	for _, want := range []string{"unexpected API response", "HTTP 500", "decode", "boom"} {
		if !strings.Contains(got, want) {
			t.Errorf("Error %q missing %q", got, want)
		}
	}
}

func TestSnippet(t *testing.T) {
	// Sensitive bodies are withheld entirely, reporting only size.
	got := snippet([]byte("secret address here"), true)
	if strings.Contains(got, "secret") || strings.Contains(got, "address") {
		t.Errorf("sensitive snippet leaked PII: %q", got)
	}
	if !strings.Contains(got, "withheld") {
		t.Errorf("sensitive snippet should note withholding: %q", got)
	}
	// Non-sensitive bodies collapse whitespace and are kept.
	got = snippet([]byte("a   b\n\tc"), false)
	if got != "a b c" {
		t.Errorf("snippet = %q, want \"a b c\"", got)
	}
	// Long non-sensitive bodies are truncated with an ellipsis.
	got = snippet([]byte(strings.Repeat("x", 500)), false)
	if len([]rune(got)) > 301 || !strings.HasSuffix(got, "…") {
		t.Errorf("long snippet not truncated: len=%d", len([]rune(got)))
	}
}

// testServer spins an httptest server and returns a Client pointed at it.
func testServer(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewTestClient(srv.URL+"/api", "tok")
}

func TestDoSendsSessionCookie(t *testing.T) {
	var gotCookie string
	c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if ck, err := r.Cookie("session"); err == nil {
			gotCookie = ck.Value
		}
		io.WriteString(w, `{"families":[{"id":"f1"}]}`)
	})
	if _, err := c.Navigation(context.Background()); err != nil {
		t.Fatalf("Navigation: %v", err)
	}
	if gotCookie != "tok" {
		t.Errorf("session cookie = %q, want tok", gotCookie)
	}
}

func TestDoDecodesError(t *testing.T) {
	c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"statusCode":404,"message":"no cart","code":"E_08_0005"}`)
	})
	_, err := c.Cart(context.Background())
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("want APIError, got %T: %v", err, err)
	}
	if !ae.IsCartNotFound() {
		t.Errorf("want cart-not-found, got %+v", ae)
	}
}

func TestDoDriftsOnNonJSON(t *testing.T) {
	c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `<html>nope</html>`)
	})
	_, err := c.Navigation(context.Background())
	var de *DriftError
	if !errors.As(err, &de) {
		t.Fatalf("want DriftError, got %T: %v", err, err)
	}
}

// TestSensitiveDriftWithholdsBody ensures a malformed cart body (sensitive
// endpoint) never lands in the error string.
func TestSensitiveDriftWithholdsBody(t *testing.T) {
	c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `not json: 42 rue secrète`)
	})
	_, err := c.Cart(context.Background())
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), "secrète") {
		t.Errorf("sensitive body leaked into error: %v", err)
	}
}

func TestSetCartProductValidates(t *testing.T) {
	c := NewTestClient("http://unused", "tok")
	if _, err := c.SetCartProduct(context.Background(), "", 1); err == nil {
		t.Error("want error for empty canonicalId")
	}
	if _, err := c.SetCartProduct(context.Background(), "id", -1); err == nil {
		t.Error("want error for negative quantity")
	}
}

func TestSearchValidatesDrift(t *testing.T) {
	// A product item missing canonicalId is a contract violation -> DriftError.
	c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"count":1,"items":[{"type":"PRODUCT","name":"x"}]}`)
	})
	_, err := c.Search(context.Background(), "x", "")
	var de *DriftError
	if !errors.As(err, &de) {
		t.Fatalf("want DriftError, got %T: %v", err, err)
	}
}

func TestSearchAllFollowsCursor(t *testing.T) {
	var calls int
	c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("next") == "" {
			io.WriteString(w, `{"items":[{"canonicalId":"a","name":"A","type":"PRODUCT"}],"next":"cur1"}`)
		} else {
			io.WriteString(w, `{"items":[{"canonicalId":"b","name":"B","type":"PRODUCT"}]}`)
		}
	})
	res, err := c.SearchAll(context.Background(), "x", 5)
	if err != nil {
		t.Fatalf("SearchAll: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
	if len(res.Items) != 2 || res.Next != "" {
		t.Errorf("items = %d next = %q, want 2 items, empty next", len(res.Items), res.Next)
	}
}

func TestSearchAllRespectsMaxPages(t *testing.T) {
	c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Always returns a next cursor; SearchAll must stop at maxPages.
		io.WriteString(w, `{"items":[{"canonicalId":"a","name":"A","type":"PRODUCT"}],"next":"more"}`)
	})
	res, err := c.SearchAll(context.Background(), "x", 2)
	if err != nil {
		t.Fatalf("SearchAll: %v", err)
	}
	if res.Next == "" {
		t.Error("truncated result should still carry Next as a signal")
	}
}
