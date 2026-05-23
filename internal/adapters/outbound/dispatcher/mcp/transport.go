// Package mcp — transport.go provides authRoundTripper, a custom http.RoundTripper
// that injects Authorization and Origin headers on every outgoing HTTP request.
//
// # Token rotation
//
// The token is captured at construction time (when [wrapHTTPClient] is called).
// Rotating the bridge token in V1 requires reconstructing the Dispatcher (i.e.,
// restarting the orchestrator bootstrap). There is no runtime hot-swap; the
// field is intentionally immutable after construction. See spec scenario O3.
package mcp

import (
	"net/http"
	"time"
)

// authRoundTripper wraps an inner http.RoundTripper and injects
// Authorization and Origin headers on every outgoing request.
//
// The token is snapshotted at construction time — see [wrapHTTPClient].
// Callers must not mutate fields after construction.
type authRoundTripper struct {
	base   http.RoundTripper // underlying transport; must not be nil
	token  string            // bearer token, captured at construction
	origin string            // Origin header value; omitted when empty
}

// RoundTrip clones the request (to satisfy the RoundTripper contract that
// callers must not mutate the original) then sets the Authorization and
// Origin headers before forwarding to the inner transport.
func (rt *authRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone before mutation — per net/http RoundTripper contract.
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+rt.token)
	if rt.origin != "" {
		r.Header.Set("Origin", rt.origin)
	}
	return rt.base.RoundTrip(r) //nolint:wrapcheck // RoundTripper must return the inner error unwrapped; wrapping breaks *url.Error detection by net/http internals.
}

// wrapHTTPClient wraps base with authRoundTripper so every request carries
// the given bearer token and origin header. If base is nil a default client
// with a 5-minute timeout is created. If base.Transport is nil,
// http.DefaultTransport is used as the inner transport.
//
// The returned *http.Client is a shallow copy of base with Transport replaced;
// all other fields (Timeout, Jar, CheckRedirect) are preserved.
func wrapHTTPClient(base *http.Client, token, origin string) *http.Client {
	if base == nil {
		base = &http.Client{Timeout: 5 * time.Minute}
	}
	inner := base.Transport
	if inner == nil {
		inner = http.DefaultTransport
	}
	out := *base // shallow copy — preserves Timeout, Jar, etc.
	out.Transport = &authRoundTripper{base: inner, token: token, origin: origin}
	return &out
}
