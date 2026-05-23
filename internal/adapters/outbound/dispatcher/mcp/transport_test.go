// Package mcp — transport_test.go white-box unit tests for authRoundTripper.
// Uses package mcp (not mcp_test) so it can access the unexported wrapHTTPClient helper.
package mcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// roundTripperFunc is a test helper that adapts a plain function to http.RoundTripper.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestAuthRoundTripper_InjectsBearer covers spec scenario O1:
// every outgoing request carries Authorization: Bearer <token>.
func TestAuthRoundTripper_InjectsBearer(t *testing.T) {
	const token = "test-token-abc"

	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
	}))
	t.Cleanup(srv.Close)

	client := wrapHTTPClient(srv.Client(), token, "")
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer resp.Body.Close() //nolint:errcheck

	assert.Equal(t, "Bearer "+token, capturedAuth,
		"O1: Authorization header must be 'Bearer <token>' on every outgoing request")
}

// TestAuthRoundTripper_InjectsOrigin covers spec scenario O2:
// every outgoing request carries Origin: <configured-origin>.
func TestAuthRoundTripper_InjectsOrigin(t *testing.T) {
	const origin = "http://localhost"

	var capturedOrigin string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedOrigin = r.Header.Get("Origin")
	}))
	t.Cleanup(srv.Close)

	client := wrapHTTPClient(srv.Client(), "tok", origin)
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer resp.Body.Close() //nolint:errcheck

	assert.Equal(t, origin, capturedOrigin,
		"O2: Origin header must match the configured origin on every outgoing request")
}

// TestAuthRoundTripper_TokenIsSnapshotAtConstruction covers spec scenario O3:
// the token is captured at wrapHTTPClient call time. Constructing the client with
// token A and then making a request MUST send "Bearer token-A" — not any later value.
//
// This test documents the V1 contract: token rotation requires reconstructing
// the Dispatcher (i.e., an orchestrator bootstrap restart). There is no
// runtime hot-swap.
func TestAuthRoundTripper_TokenIsSnapshotAtConstruction(t *testing.T) {
	const initialToken = "token-A"
	const rotatedToken = "token-B"

	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
	}))
	t.Cleanup(srv.Close)

	// Construct with initialToken. The client snapshots it here.
	client := wrapHTTPClient(srv.Client(), initialToken, "")

	// Build a second client with the "rotated" token to confirm they are independent.
	// (The first client must NOT pick up the rotated token.)
	_ = wrapHTTPClient(srv.Client(), rotatedToken, "")

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req) // must use the FIRST client (initialToken)
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer resp.Body.Close() //nolint:errcheck

	assert.Equal(t, "Bearer "+initialToken, capturedAuth,
		"O3: token must be snapshotted at construction; the first client must still send the initial token")
	assert.NotEqual(t, "Bearer "+rotatedToken, capturedAuth,
		"O3: rotated token must NOT appear on the first client — rotation requires reconstruction")
}

// TestAuthRoundTripper_EmptyOriginOmitted verifies that when origin is empty
// the Origin header is not injected (avoids sending a blank header).
func TestAuthRoundTripper_EmptyOriginOmitted(t *testing.T) {
	var capturedOrigin string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedOrigin = r.Header.Get("Origin")
	}))
	t.Cleanup(srv.Close)

	client := wrapHTTPClient(srv.Client(), "tok", "" /* empty origin */)
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer resp.Body.Close() //nolint:errcheck

	assert.Empty(t, capturedOrigin,
		"when origin is empty no Origin header should be sent")
}

// TestWrapHTTPClient_NilBaseCreatesDefault verifies that passing a nil base
// client does not panic and produces a working HTTP client.
func TestWrapHTTPClient_NilBaseCreatesDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	// nil base must not panic.
	client := wrapHTTPClient(nil, "tok", "http://localhost")
	require.NotNil(t, client)

	// Must be able to make a request (server reachable via default transport).
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestWrapHTTPClient_PreservesInnerTransport verifies that wrapHTTPClient
// composes (wraps) the base client's existing transport rather than replacing it.
func TestWrapHTTPClient_PreservesInnerTransport(t *testing.T) {
	var innerCalled bool
	inner := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		innerCalled = true
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       http.NoBody,
		}, nil
	})

	base := &http.Client{Transport: inner}
	client := wrapHTTPClient(base, "tok", "http://localhost")

	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close() //nolint:errcheck
	}

	assert.True(t, innerCalled, "wrapHTTPClient must delegate to the original base transport")
}
