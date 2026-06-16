package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeState(t *testing.T, cookies ...stateCookie) string {
	t.Helper()
	st := storageState{Cookies: cookies, Origins: json.RawMessage(`[{"keep":"me"}]`)}
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadSession(t *testing.T) {
	path := writeState(t,
		stateCookie{Name: "other", Domain: cookieDomain, Value: "x"},
		stateCookie{Name: "session", Domain: cookieDomain, Value: "tok123", Expires: 1_900_000_000},
	)
	s, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if s.token() != "tok123" {
		t.Errorf("token = %q, want tok123", s.token())
	}
	if s.Expires().Unix() != 1_900_000_000 {
		t.Errorf("Expires = %v", s.Expires())
	}
}

func TestLoadSessionErrors(t *testing.T) {
	if _, err := LoadSession(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Error("want error for missing file")
	}

	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte("{not json"), 0o600)
	if _, err := LoadSession(bad); err == nil {
		t.Error("want error for malformed json")
	}

	// Right file, but no session cookie for our domain.
	path := writeState(t, stateCookie{Name: "session", Domain: "example.com", Value: "x"})
	if _, err := LoadSession(path); err == nil {
		t.Error("want error when no session cookie for domain")
	}
}

func TestUpdateFromResponseRollsExpiry(t *testing.T) {
	path := writeState(t, stateCookie{Name: "session", Domain: cookieDomain, Value: "tok", Expires: 1000})
	s, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(60 * 24 * time.Hour)
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Add("Set-Cookie", (&http.Cookie{Name: "session", Value: "tok", Expires: future}).String())
	s.updateFromResponse(resp)

	if !s.changed {
		t.Error("expiry roll-forward should mark session changed")
	}
	if s.Expires().Unix() != future.Unix() {
		t.Errorf("Expires = %v, want %v", s.Expires().Unix(), future.Unix())
	}
}

func TestUpdateFromResponseRotatesValue(t *testing.T) {
	path := writeState(t, stateCookie{Name: "session", Domain: cookieDomain, Value: "old", Expires: 1000})
	s, _ := LoadSession(path)
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Add("Set-Cookie", (&http.Cookie{Name: "session", Value: "new"}).String())
	s.updateFromResponse(resp)
	if s.token() != "new" {
		t.Errorf("token = %q, want new", s.token())
	}
	if !s.changed {
		t.Error("value rotation should mark changed")
	}
}

func TestUpdateFromResponseNoOp(t *testing.T) {
	path := writeState(t, stateCookie{Name: "session", Domain: cookieDomain, Value: "tok", Expires: 1000})
	s, _ := LoadSession(path)
	// Unrelated cookie must not touch the session.
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Add("Set-Cookie", (&http.Cookie{Name: "tracking", Value: "z"}).String())
	s.updateFromResponse(resp)
	if s.changed {
		t.Error("unrelated cookie should not mark changed")
	}
}

func TestSaveOnlyWhenChanged(t *testing.T) {
	path := writeState(t, stateCookie{Name: "session", Domain: cookieDomain, Value: "tok", Expires: 1000})
	s, _ := LoadSession(path)

	// No change -> save is a no-op.
	if err := s.save(); err != nil {
		t.Fatal(err)
	}

	// Now mutate and save; expiry must persist and origins survive round-trip.
	s.state.Cookies[s.idx].Expires = 2000
	s.changed = true
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	if s.changed {
		t.Error("save should clear changed flag")
	}

	raw, _ := os.ReadFile(path)
	var st storageState
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	if st.Cookies[0].Expires != 2000 {
		t.Errorf("persisted expiry = %v, want 2000", st.Cookies[0].Expires)
	}
	var origins bytes.Buffer
	if err := json.Compact(&origins, st.Origins); err != nil {
		t.Fatal(err)
	}
	if origins.String() != `[{"keep":"me"}]` {
		t.Errorf("origins not preserved: %s", origins.String())
	}
}
