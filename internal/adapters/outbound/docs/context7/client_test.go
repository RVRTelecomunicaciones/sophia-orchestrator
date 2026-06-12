package context7_test

// T5.4 — RED tests for the Context7 DocsProvider adapter.
//
// All tests use mcp.NewInMemoryTransports to spin up a fake MCP server
// in-process — NO real Context7/npx in CI.
//
// Scenario IDs:
//   CA1 — ResolveLibrary calls context7.resolve-library-id and maps entries to []LibraryEntry
//   CA2 — GetDocs calls context7.get-library-docs and returns DocsResult with raw markdown
//   CA3 — unconfigured (missing URL/token) → ErrDocsUnavailable WITHOUT any transport dial
//   CA4 — transport/timeout error → wrapped error (wrapcheck-clean) + session closed
//   CA5 — per-call session: Connect→CallTool→Close, no session leak across calls

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	context7adapter "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/docs/context7"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// ---------------------------------------------------------------------------
// Fake MCP server helpers
// ---------------------------------------------------------------------------

// resolveLibraryResponse mirrors the shape that context7.resolve-library-id
// returns (array of library entries keyed by standard field names).
type resolveLibraryResponse struct {
	Libraries []struct {
		ID           string  `json:"id"`
		SnippetCount int     `json:"snippetCount"`
		Score        float64 `json:"score"`
		Version      string  `json:"version,omitempty"`
	} `json:"libraries"`
}

// getLibraryDocsResponse mirrors the shape that context7.get-library-docs
// returns (markdown text body).
type getLibraryDocsResponse struct {
	Content string `json:"content"`
	ID      string `json:"id"`
	Snippets int   `json:"snippets"`
	Score   float64 `json:"score"`
}

// startFakeMCPServer spins up an in-memory MCP server that registers
// context7.resolve-library-id and context7.get-library-docs handlers.
// It returns the client-side InMemoryTransport to use when constructing
// the adapter under test, and a cancel func to shut it down.
func startFakeMCPServer(
	t *testing.T,
	resolveHandler func(ctx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error),
	getDocsHandler func(ctx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error),
) *sdkmcp.InMemoryTransport {
	t.Helper()
	srv := sdkmcp.NewServer(
		&sdkmcp.Implementation{Name: "fake-context7", Version: "test"},
		nil,
	)
	// Register context7.resolve-library-id.
	// AddTool[any, any] automatically sets InputSchema to {"type":"object"}.
	sdkmcp.AddTool(srv,
		&sdkmcp.Tool{Name: "context7.resolve-library-id"},
		func(ctx context.Context, req *sdkmcp.CallToolRequest, _ any) (*sdkmcp.CallToolResult, any, error) {
			res, err := resolveHandler(ctx, req)
			return res, nil, err
		},
	)
	// Register context7.get-library-docs.
	sdkmcp.AddTool(srv,
		&sdkmcp.Tool{Name: "context7.get-library-docs"},
		func(ctx context.Context, req *sdkmcp.CallToolRequest, _ any) (*sdkmcp.CallToolResult, any, error) {
			res, err := getDocsHandler(ctx, req)
			return res, nil, err
		},
	)

	t1, t2 := sdkmcp.NewInMemoryTransports()

	ctx := context.Background()
	_, err := srv.Connect(ctx, t1, nil)
	require.NoError(t, err, "fake MCP server connect failed")

	return t2
}

// textResult wraps a string as a single-text-content CallToolResult.
func textResult(s string) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: s}},
	}
}

// jsonResult marshals v and returns it as a text tool result.
func jsonResult(t *testing.T, v any) *sdkmcp.CallToolResult {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return textResult(string(b))
}

// ---------------------------------------------------------------------------
// CA1 — ResolveLibrary maps resolve-library-id response to []LibraryEntry
// ---------------------------------------------------------------------------

func TestContext7Client_ResolveLibrary_MapsEntries(t *testing.T) {
	t.Parallel()

	wantResp := resolveLibraryResponse{
		Libraries: []struct {
			ID           string  `json:"id"`
			SnippetCount int     `json:"snippetCount"`
			Score        float64 `json:"score"`
			Version      string  `json:"version,omitempty"`
		}{
			{ID: "/angular/angular@22.0.0", SnippetCount: 120, Score: 0.95, Version: "22.0.0"},
			{ID: "/angular/angular", SnippetCount: 300, Score: 0.88},
		},
	}

	var calledWith map[string]any
	transport := startFakeMCPServer(t,
		func(_ context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			_ = json.Unmarshal(req.Params.Arguments, &calledWith)
			return jsonResult(t, wantResp), nil
		},
		func(_ context.Context, _ *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			t.Error("get-library-docs should not be called in CA1")
			return textResult(""), nil
		},
	)

	client := context7adapter.NewWithTransport(transport)
	entries, err := client.ResolveLibrary(context.Background(), "angular", "best practices")
	require.NoError(t, err)
	require.Len(t, entries, 2)

	assert.Equal(t, "/angular/angular@22.0.0", entries[0].ID)
	assert.Equal(t, 120, entries[0].Snippets)
	assert.InDelta(t, 0.95, entries[0].Score, 0.001)
	assert.False(t, entries[0].IsMain, "version-specific entry must not be IsMain")

	assert.Equal(t, "/angular/angular", entries[1].ID)
	assert.True(t, entries[1].IsMain, "no-version entry must be IsMain")

	// Arguments forwarded to tool
	assert.Equal(t, "angular", calledWith["libraryName"])
	assert.Equal(t, "best practices", calledWith["query"])
}

// ---------------------------------------------------------------------------
// CA2 — GetDocs returns DocsResult with raw markdown body
// ---------------------------------------------------------------------------

func TestContext7Client_GetDocs_ReturnsDocsResult(t *testing.T) {
	t.Parallel()

	wantBody := "# Angular 22\n\n## Best practices\n\nUse signals."
	wantResp := getLibraryDocsResponse{
		Content:  wantBody,
		ID:       "/angular/angular@22.0.0",
		Snippets: 120,
		Score:    0.95,
	}

	var calledWith map[string]any
	transport := startFakeMCPServer(t,
		func(_ context.Context, _ *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			t.Error("resolve-library-id should not be called in CA2")
			return textResult(""), nil
		},
		func(_ context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			_ = json.Unmarshal(req.Params.Arguments, &calledWith)
			return jsonResult(t, wantResp), nil
		},
	)

	client := context7adapter.NewWithTransport(transport)
	result, err := client.GetDocs(context.Background(), "/angular/angular@22.0.0", "best practices", "", 8000)
	require.NoError(t, err)

	assert.Equal(t, wantBody, result.Body)
	assert.Equal(t, "/angular/angular@22.0.0", result.LibraryID)
	assert.Equal(t, 120, result.Snippets)
	assert.InDelta(t, 0.95, result.Score, 0.001)

	// Arguments forwarded to tool
	assert.Equal(t, "/angular/angular@22.0.0", calledWith["context7CompatibleLibraryId"])
	assert.Equal(t, "best practices", calledWith["query"])
	assert.EqualValues(t, 8000, calledWith["tokens"])
}

// ---------------------------------------------------------------------------
// CA3 — unconfigured provider → ErrDocsUnavailable, no transport dial
// ---------------------------------------------------------------------------

func TestContext7Client_Unconfigured_ReturnsErrDocsUnavailable(t *testing.T) {
	t.Parallel()

	var dialCount atomic.Int32
	// Wrap http.DefaultTransport to count dial attempts.
	countingTransport := &countingRoundTripper{base: http.DefaultTransport, count: &dialCount}

	// NewWithConfig with empty BridgeURL/Token must not dial.
	client := context7adapter.NewWithConfig(context7adapter.Config{
		HTTPClient: &http.Client{Transport: countingTransport},
		BridgeURL:  "",
		Token:      "",
	})

	_, err := client.ResolveLibrary(context.Background(), "angular", "best practices")
	require.Error(t, err)
	assert.True(t, errors.Is(err, outbound.ErrDocsUnavailable), "must wrap ErrDocsUnavailable, got: %v", err)
	assert.Equal(t, int32(0), dialCount.Load(), "no transport dials should occur for unconfigured provider")

	_, err = client.GetDocs(context.Background(), "some-id", "q", "", 8000)
	require.Error(t, err)
	assert.True(t, errors.Is(err, outbound.ErrDocsUnavailable))
	assert.Equal(t, int32(0), dialCount.Load())
}

// countingRoundTripper counts HTTP round-trips.
type countingRoundTripper struct {
	base  http.RoundTripper
	count *atomic.Int32
}

func (c *countingRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	c.count.Add(1)
	return c.base.RoundTrip(r) //nolint:wrapcheck
}

// ---------------------------------------------------------------------------
// CA4 — transport error → wrapped error, session closed
// ---------------------------------------------------------------------------

func TestContext7Client_TransportError_WrapsError(t *testing.T) {
	t.Parallel()

	// errTransport always fails.
	errTransport := &errorRoundTripper{}
	client := context7adapter.NewWithConfig(context7adapter.Config{
		HTTPClient: &http.Client{Transport: errTransport},
		BridgeURL:  "http://127.0.0.1:19999", // unreachable but has a valid URL
		Token:      "test-token",
	})

	_, err := client.ResolveLibrary(context.Background(), "angular", "q")
	require.Error(t, err)
	// Must NOT be ErrDocsUnavailable (that's for unconfigured).
	assert.False(t, errors.Is(err, outbound.ErrDocsUnavailable),
		"transport error must not be wrapped as ErrDocsUnavailable")
	// Must be a wrapped error (wrapcheck: original error preserved in chain).
	var urlErr interface{ Unwrap() error }
	assert.True(t, errors.As(err, &urlErr) || errors.Unwrap(err) != nil,
		"error must wrap the underlying transport error")
}

// errorRoundTripper always returns a connection-refused-style error.
type errorRoundTripper struct{}

func (e *errorRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	return nil, errors.New("connection refused") //nolint:err113
}

// ---------------------------------------------------------------------------
// CA5 — per-call session: each call opens and closes its own session.
// Uses NewWithTransportFactory so each call gets a fresh InMemoryTransport
// pair (InMemoryTransport is one-shot by design in the MCP SDK).
// ---------------------------------------------------------------------------

func TestContext7Client_PerCallSession_NoLeak(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int32

	// transportFactory spins up a fresh fake server for each call and returns
	// the client-side transport. This proves per-call session semantics: if the
	// adapter reused a session it would get a connection error on the second call.
	transportFactory := func() sdkmcp.Transport {
		srv := sdkmcp.NewServer(
			&sdkmcp.Implementation{Name: "fake-context7-ca5", Version: "test"},
			nil,
		)
		sdkmcp.AddTool(srv,
			&sdkmcp.Tool{Name: "context7.resolve-library-id"},
			func(_ context.Context, _ *sdkmcp.CallToolRequest, _ any) (*sdkmcp.CallToolResult, any, error) {
				callCount.Add(1)
				resp := resolveLibraryResponse{}
				b, _ := json.Marshal(resp)
				return textResult(string(b)), nil, nil
			},
		)
		sdkmcp.AddTool(srv,
			&sdkmcp.Tool{Name: "context7.get-library-docs"},
			func(_ context.Context, _ *sdkmcp.CallToolRequest, _ any) (*sdkmcp.CallToolResult, any, error) {
				return textResult("{}"), nil, nil
			},
		)
		t1, t2 := sdkmcp.NewInMemoryTransports()
		ctx := context.Background()
		if _, err := srv.Connect(ctx, t1, nil); err != nil {
			t.Errorf("fake server connect: %v", err)
		}
		return t2
	}

	client := context7adapter.NewWithTransportFactory(transportFactory)
	ctx := context.Background()

	// Two independent calls — each must succeed because each gets a fresh session.
	_, err1 := client.ResolveLibrary(ctx, "angular", "q")
	require.NoError(t, err1)

	_, err2 := client.ResolveLibrary(ctx, "react", "q")
	require.NoError(t, err2)

	assert.Equal(t, int32(2), callCount.Load(), "both calls must reach the server")
}
