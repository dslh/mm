package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const defaultBaseURL = "https://www.mon-marche.fr/api"

// defaultMinInterval keeps request rates human-paced (ToS review, CLAUDE.md);
// a 0–500ms jitter is added on top.
const defaultMinInterval = 1 * time.Second

// Client authenticates with the session cookie only and serializes all
// requests through the pacing lock.
type Client struct {
	httpc *http.Client
	sess  *Session

	// base is the API root and minInterval the pacing floor; both default to the
	// production values in New and are only overridden by NewTestClient.
	base        string
	minInterval time.Duration

	mu   sync.Mutex
	last time.Time
}

func New(statePath string) (*Client, error) {
	sess, err := LoadSession(statePath)
	if err != nil {
		return nil, err
	}
	return &Client{
		httpc:       &http.Client{Timeout: 30 * time.Second},
		sess:        sess,
		base:        defaultBaseURL,
		minInterval: defaultMinInterval,
	}, nil
}

// Close writes the rolled-forward cookie expiry back to the state file.
func (c *Client) Close() error { return c.sess.save() }

func (c *Client) SessionExpires() time.Time { return c.sess.Expires() }

func (c *Client) pace() {
	if c.minInterval <= 0 {
		return // pacing disabled (tests)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.last.IsZero() {
		wait := c.minInterval + time.Duration(rand.Int64N(500))*time.Millisecond
		if d := time.Until(c.last.Add(wait)); d > 0 {
			time.Sleep(d)
		}
	}
	c.last = time.Now()
}

// APIError is a structured error body ({statusCode, message, code, ...}).
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s (HTTP %d, %s)", e.Message, e.Status, e.Code)
}

// Error-code namespaces, per docs/api.md "Failure modes".
func (e *APIError) IsAuth() bool         { return strings.HasPrefix(e.Code, "E_01_") }
func (e *APIError) IsCartNotFound() bool { return e.Code == "E_08_0005" }
func (e *APIError) IsProduct() bool      { return strings.HasPrefix(e.Code, "E_ECOM_") }

const (
	CodeStaleProduct = "E_ECOM_01_0012" // unknown/stale canonicalId
	CodeOutOfStock   = "E_ECOM_01_0004"
	CodeQtyTooHigh   = "E_ECOM_01_0003"
)

// DriftError: the response didn't match the documented contract — the private
// API may have changed and needs re-verifying against fresh browser traffic.
type DriftError struct {
	Status  int
	Snippet string
	Cause   error
}

func (e *DriftError) Error() string {
	var b strings.Builder
	b.WriteString("unexpected API response")
	if e.Status != 0 {
		fmt.Fprintf(&b, " (HTTP %d)", e.Status)
	}
	if e.Cause != nil {
		fmt.Fprintf(&b, ": %v", e.Cause)
	}
	if e.Snippet != "" {
		fmt.Fprintf(&b, "; body: %s", e.Snippet)
	}
	return b.String()
}

// reqOpts tunes a single request. `sensitive` marks an endpoint whose response
// body carries PII (cart, order detail, delivery2): on a decode/HTTP failure the
// raw body is withheld from the DriftError snippet so personal data never lands
// in an error string, log line, or stderr — only its size is reported.
type reqOpts struct {
	sensitive bool
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	return c.doOpts(ctx, method, path, query, body, out, reqOpts{})
}

func (c *Client) doOpts(ctx context.Context, method, path string, query url.Values, body, out any, opts reqOpts) error {
	c.pace()
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.AddCookie(&http.Cookie{Name: "session", Value: c.sess.token()})

	resp, err := c.httpc.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	c.sess.updateFromResponse(resp)

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return fmt.Errorf("%s %s: reading response: %w", method, path, err)
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(raw, out); err != nil {
			return &DriftError{Status: resp.StatusCode, Snippet: snippet(raw, opts.sensitive), Cause: err}
		}
		return nil
	}

	var eb struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	}
	if json.Unmarshal(raw, &eb) == nil && eb.Code != "" {
		return &APIError{Status: resp.StatusCode, Code: eb.Code, Message: eb.Message}
	}
	return &DriftError{Status: resp.StatusCode, Snippet: snippet(raw, opts.sensitive)}
}

func snippet(b []byte, sensitive bool) string {
	if sensitive {
		// Never echo a PII-bearing body; report only its size for drift triage.
		return fmt.Sprintf("<%d bytes withheld (may contain personal data)>", len(b))
	}
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
