// Package api is a typed client for the private mon-marché JSON API.
// Endpoint contracts are documented in docs/api.md; responses that stop
// matching them surface as DriftError rather than being silently tolerated.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// storageState mirrors the Playwright storage-state file (.auth/state.json).
// Origins is kept opaque so saving preserves it untouched.
type storageState struct {
	Cookies []stateCookie   `json:"cookies"`
	Origins json.RawMessage `json:"origins"`
}

type stateCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires"` // unix seconds; -1 = browser-session cookie
	HTTPOnly bool    `json:"httpOnly"`
	Secure   bool    `json:"secure"`
	SameSite string  `json:"sameSite"`
}

const cookieDomain = "www.mon-marche.fr"

// Session is the mon-marché session cookie extracted from a Playwright
// storage-state file. The cookie is a sliding 60-day window whose value never
// rotates, so responses only roll the expiry forward; save writes that back
// to keep the stored date accurate (docs/api.md "Lifetime").
type Session struct {
	path    string
	state   storageState
	idx     int
	changed bool
}

func LoadSession(path string) (*Session, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading auth state: %w (see `mm auth login`)", err)
	}
	var st storageState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	for i, ck := range st.Cookies {
		if ck.Name == "session" && ck.Domain == cookieDomain {
			return &Session{path: path, state: st, idx: i}, nil
		}
	}
	return nil, fmt.Errorf("no session cookie in %s (see `mm auth login`)", path)
}

func (s *Session) token() string { return s.state.Cookies[s.idx].Value }

func (s *Session) Expires() time.Time {
	return time.Unix(int64(s.state.Cookies[s.idx].Expires), 0)
}

func (s *Session) updateFromResponse(resp *http.Response) {
	for _, ck := range resp.Cookies() {
		if ck.Name != "session" {
			continue
		}
		c := &s.state.Cookies[s.idx]
		if ck.Value != "" && ck.Value != c.Value {
			c.Value = ck.Value
			s.changed = true
		}
		exp := ck.Expires
		if exp.IsZero() && ck.MaxAge > 0 {
			exp = time.Now().Add(time.Duration(ck.MaxAge) * time.Second)
		}
		if !exp.IsZero() {
			if e := float64(exp.Unix()); e != c.Expires {
				c.Expires = e
				s.changed = true
			}
		}
	}
}

func (s *Session) save() error {
	if !s.changed {
		return nil
	}
	out, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.path, append(out, '\n'), 0o600); err != nil {
		return err
	}
	s.changed = false
	return nil
}
