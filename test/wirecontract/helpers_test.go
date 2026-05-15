//go:build wirecontract

// Package wirecontract_test exercises every cross-repo HTTP path that
// sophia-orchestator initiates, asserting the URL the client builds matches
// the route declared in docs/architecture/wire-contracts.md. Tests are gated
// behind the `wirecontract` build tag so they run only via the dedicated
// CI job (see .github/workflows/wire-contract.yml).
//
// Pattern:
//
//	srv, capt := newCapturer(t, http.StatusOK, `{"ok":true}`)
//	cfg := governance.DefaultConfig(srv.URL, "k")
//	client, _ := governance.New(cfg)
//	_, _ = client.EvaluatePhase(ctx, in)
//	wirecontract.AssertRoute(t, capt, "POST", "/governance/v1/decisions/phase", 1)
//
// Each test maps to a row in the matrix; failure messages cite the row
// number so a drift can be triaged without grepping.
package wirecontract_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// capturedRequest holds the method + path observed by newCapturer's stub
// server. Concurrency-safe so tests that fire multiple calls into one
// server still get deterministic reads.
type capturedRequest struct {
	mu     sync.Mutex
	method string
	path   string
	query  string
	body   []byte
}

func (c *capturedRequest) snapshot() (method, path, query string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.method, c.path, c.query
}

// newCapturer spins up an httptest.Server that records the inbound method,
// path, and query for each request and replies with the supplied status +
// body. The server is closed via t.Cleanup so callers don't need to remember.
func newCapturer(t *testing.T, status int, body string) (*httptest.Server, *capturedRequest) {
	t.Helper()
	capt := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capt.mu.Lock()
		capt.method = r.Method
		capt.path = r.URL.Path
		capt.query = r.URL.RawQuery
		capt.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if body != "" {
			_, _ = w.Write([]byte(body))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, capt
}

// assertRoute checks the captured request matches the matrix row.
//
// row is the matrix row number from docs/architecture/wire-contracts.md;
// it is included verbatim in the failure message so engineers can jump
// straight to the doc when CI fails.
func assertRoute(t *testing.T, capt *capturedRequest, expectedMethod, expectedPath string, row int) {
	t.Helper()
	method, path, _ := capt.snapshot()
	require.Equal(t, expectedMethod, method,
		"wire-contract drift on row #%d: see docs/architecture/wire-contracts.md", row)
	require.Equal(t, expectedPath, path,
		"wire-contract drift on row #%d: see docs/architecture/wire-contracts.md", row)
}

// assertRoutePrefix is for routes whose path includes a dynamic ID segment
// (e.g. /api/v1/memories/{id}). The full captured path must start with
// expectedPrefix and not contain the literal placeholder text.
func assertRoutePrefix(t *testing.T, capt *capturedRequest, expectedMethod, expectedPrefix string, row int) {
	t.Helper()
	method, path, _ := capt.snapshot()
	require.Equal(t, expectedMethod, method,
		"wire-contract drift on row #%d: see docs/architecture/wire-contracts.md", row)
	require.True(t, len(path) > len(expectedPrefix) && path[:len(expectedPrefix)] == expectedPrefix,
		fmt.Sprintf("wire-contract drift on row #%d: path %q must start with %q (see docs/architecture/wire-contracts.md)",
			row, path, expectedPrefix))
}
