package api

import (
	"net/http"
	"time"
)

// NewTestClient builds a Client that talks to baseURL with a fixed in-memory
// session token and no pacing delay. It bypasses the on-disk .auth/state.json
// so tests (including those in other packages, e.g. internal/ops) can drive the
// client against an httptest.Server. The token is sent as the session cookie on
// every request but is otherwise opaque to the server stub.
//
// This is test support, not part of the production surface: New is the only
// constructor real code should use.
func NewTestClient(baseURL, token string) *Client {
	sess := &Session{
		state: storageState{Cookies: []stateCookie{{
			Name:    "session",
			Value:   token,
			Domain:  cookieDomain,
			Expires: float64(time.Now().Add(60 * 24 * time.Hour).Unix()),
		}}},
		idx: 0,
	}
	return &Client{
		httpc:       &http.Client{Timeout: 30 * time.Second},
		sess:        sess,
		base:        baseURL,
		minInterval: 0, // no pacing in tests
	}
}
