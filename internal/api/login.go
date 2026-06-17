package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrBadCredentials is returned by Login when the server rejects the
// email/password pair (HTTP 401 or an E_01_ auth code), as distinct from a
// transport error or a contract mismatch. Callers map it to a friendly
// "wrong email or password" message rather than the "session expired" path.
var ErrBadCredentials = errors.New("invalid email or password")

// Login authenticates directly against POST /auth/signin and writes a fresh
// session-state file to statePath (creating its directory if needed).
//
// The password is sent once in the request body and is never logged, stored,
// or returned: only the resulting `session` cookie is persisted. The cookie
// arrives as a Set-Cookie response header — hidden from the SPA's JavaScript
// because it is httpOnly, but visible to a real HTTP client. Verified contract:
// docs/api.md "Login".
func Login(ctx context.Context, statePath, email, password string) (*Session, error) {
	body, err := json.Marshal(map[string]string{"email": email, "password": password})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", defaultBaseURL+"/auth/signin", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	httpc := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST /auth/signin: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("POST /auth/signin: reading response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var eb struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		}
		if json.Unmarshal(raw, &eb) == nil && eb.Code != "" {
			if resp.StatusCode == http.StatusUnauthorized || strings.HasPrefix(eb.Code, "E_01_") {
				return nil, ErrBadCredentials
			}
			return nil, &APIError{Status: resp.StatusCode, Code: eb.Code, Message: eb.Message}
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, ErrBadCredentials
		}
		// The signin body can echo the submitted email; treat it as sensitive so
		// no personal data lands in a drift snippet.
		return nil, &DriftError{Status: resp.StatusCode, Snippet: snippet(raw, true)}
	}

	var cookie *http.Cookie
	for _, ck := range resp.Cookies() {
		if ck.Name == "session" {
			cookie = ck
		}
	}
	if cookie == nil || cookie.Value == "" {
		return nil, &DriftError{Status: resp.StatusCode, Snippet: "signin succeeded but returned no session cookie"}
	}

	sess, err := newSessionFromCookie(statePath, cookie)
	if err != nil {
		return nil, err
	}
	if err := sess.save(); err != nil {
		return nil, fmt.Errorf("writing %s: %w", statePath, err)
	}
	return sess, nil
}

// newSessionFromCookie synthesizes a storage-state file from a single Set-Cookie
// value, filling the constant cookie attributes (domain/path/flags documented in
// docs/api.md "Lifetime"). The expiry prefers the cookie's own Max-Age/Expires
// and otherwise assumes the sliding 60-day window; either way the first
// authenticated request rolls it forward and save() corrects the stored date.
func newSessionFromCookie(path string, ck *http.Cookie) (*Session, error) {
	exp := ck.Expires
	if exp.IsZero() && ck.MaxAge > 0 {
		exp = time.Now().Add(time.Duration(ck.MaxAge) * time.Second)
	}
	if exp.IsZero() {
		exp = time.Now().Add(60 * 24 * time.Hour)
	}
	st := storageState{
		Cookies: []stateCookie{{
			Name:     "session",
			Value:    ck.Value,
			Domain:   cookieDomain,
			Path:     "/",
			Expires:  float64(exp.Unix()),
			HTTPOnly: true,
			Secure:   true,
			SameSite: "Lax",
		}},
		Origins: json.RawMessage("[]"),
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	return &Session{path: path, state: st, idx: 0, changed: true}, nil
}
