package api

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestNewSessionFromCookieRoundTrips(t *testing.T) {
	// Writes into a not-yet-existing .auth dir, mirroring a first-time login.
	path := filepath.Join(t.TempDir(), "fresh", ".auth", "state.json")
	ck := &http.Cookie{Name: "session", Value: "tok-xyz", MaxAge: 60 * 24 * 3600}

	sess, err := newSessionFromCookie(path, ck)
	if err != nil {
		t.Fatalf("newSessionFromCookie: %v", err)
	}
	if err := sess.save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// The synthesized file must be loadable by the normal client path.
	got, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got.token() != "tok-xyz" {
		t.Errorf("token = %q, want tok-xyz", got.token())
	}
	// MaxAge → expiry roughly 60 days out (allow a generous window for clock).
	d := time.Until(got.Expires())
	if d < 59*24*time.Hour || d > 61*24*time.Hour {
		t.Errorf("expiry %v out of expected ~60-day range", got.Expires())
	}
}

func TestNewSessionFromCookieDefaultsExpiry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	// No Expires and no Max-Age: fall back to the sliding 60-day assumption.
	sess, err := newSessionFromCookie(path, &http.Cookie{Name: "session", Value: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Until(sess.Expires()); d < 59*24*time.Hour || d > 61*24*time.Hour {
		t.Errorf("default expiry %v out of expected ~60-day range", sess.Expires())
	}
}
