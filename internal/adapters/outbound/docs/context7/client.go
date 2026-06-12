// Package context7 implements outbound.DocsProvider by calling
// context7.resolve-library-id and context7.get-library-docs on the
// sophia-agent-mcp bridge via MCP Streamable HTTP.
//
// The adapter reuses the same StreamableClientTransport + authRoundTripper
// construction pattern as the MCP dispatcher (DG-C7-8), and follows the same
// per-call Connect → CallTool → Close session lifecycle.
//
// V1 constraints:
//   - Per-call session lifecycle (Connect → CallTool → Close). No pooling.
//   - Missing BridgeURL or Token → ErrDocsUnavailable returned immediately,
//     no transport dial attempted.
//   - Safe for concurrent use from multiple goroutines.
package context7

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// Config holds the parameters needed to reach the agent-mcp bridge.
// These values mirror what the MCP dispatcher uses (DG-C7-8).
type Config struct {
	// BridgeURL is the base URL of the sophia-agent-mcp HTTP server,
	// e.g. "http://127.0.0.1:7775". Required for production use.
	// Empty value causes every call to return ErrDocsUnavailable.
	BridgeURL string

	// Token is the bearer token sent in the Authorization header.
	// Empty value causes every call to return ErrDocsUnavailable.
	Token string

	// Origin is the HTTP Origin header value sent to satisfy the bridge's
	// allowlist check. Defaults to "http://localhost".
	Origin string

	// HTTPClient is the base HTTP client to wrap. If nil a default client
	// with a 5-minute timeout is created.
	HTTPClient *http.Client
}

// sdkSession is the minimal surface of *sdkmcp.ClientSession needed here.
// Matches the interface the dispatcher already uses internally.
type sdkSession interface {
	CallTool(ctx context.Context, params *sdkmcp.CallToolParams) (*sdkmcp.CallToolResult, error)
	Close() error
}

// sessionOpener opens a fresh per-call MCP session.
type sessionOpener func(ctx context.Context) (sdkSession, error)

// Client implements outbound.DocsProvider using the agent-mcp bridge.
type Client struct {
	open      sessionOpener
	available bool // false when BridgeURL/Token are empty
}

var _ outbound.DocsProvider = (*Client)(nil)

// New constructs a Client from Config. If BridgeURL or Token are empty the
// returned client will return ErrDocsUnavailable on every call without
// attempting any network connection.
func New(cfg Config) *Client {
	if cfg.BridgeURL == "" || cfg.Token == "" {
		return &Client{available: false}
	}
	if cfg.Origin == "" {
		cfg.Origin = "http://localhost"
	}

	httpClient := wrapHTTPClient(cfg.HTTPClient, cfg.Token, cfg.Origin)
	sdkClient := sdkmcp.NewClient(
		&sdkmcp.Implementation{Name: "sophia-orchestator", Version: "v2.1"},
		nil,
	)
	cfgSnap := cfg
	opener := func(ctx context.Context) (sdkSession, error) {
		transport := &sdkmcp.StreamableClientTransport{
			Endpoint:             strings.TrimRight(cfgSnap.BridgeURL, "/") + "/mcp",
			HTTPClient:           httpClient,
			DisableStandaloneSSE: true,
		}
		return sdkClient.Connect(ctx, transport, nil)
	}
	return &Client{open: opener, available: true}
}

// NewWithConfig is an alias for New. Exported for tests that need to pass a
// custom HTTPClient (e.g. to count dials or inject errors).
func NewWithConfig(cfg Config) *Client { return New(cfg) }

// NewWithTransport constructs a Client that uses the given MCP Transport
// directly, bypassing Streamable HTTP. Intended for single-call in-process
// tests using mcp.NewInMemoryTransports. For multi-call tests, prefer
// NewWithTransportFactory.
func NewWithTransport(t sdkmcp.Transport) *Client {
	sdkClient := sdkmcp.NewClient(
		&sdkmcp.Implementation{Name: "sophia-orchestator", Version: "v2.1"},
		nil,
	)
	opener := func(ctx context.Context) (sdkSession, error) {
		return sdkClient.Connect(ctx, t, nil)
	}
	return &Client{open: opener, available: true}
}

// NewWithTransportFactory constructs a Client that calls factory() before
// each session to obtain a fresh Transport. This allows multi-call in-process
// tests where each call needs a new InMemoryTransport pair.
func NewWithTransportFactory(factory func() sdkmcp.Transport) *Client {
	sdkClient := sdkmcp.NewClient(
		&sdkmcp.Implementation{Name: "sophia-orchestator", Version: "v2.1"},
		nil,
	)
	opener := func(ctx context.Context) (sdkSession, error) {
		return sdkClient.Connect(ctx, factory(), nil)
	}
	return &Client{open: opener, available: true}
}

// ---------------------------------------------------------------------------
// outbound.DocsProvider implementation
// ---------------------------------------------------------------------------

// ResolveLibrary calls context7.resolve-library-id on the bridge and maps
// the response to []outbound.LibraryEntry. A per-call session is opened and
// closed for each invocation.
func (c *Client) ResolveLibrary(ctx context.Context, framework, query string) ([]outbound.LibraryEntry, error) {
	if !c.available {
		return nil, outbound.ErrDocsUnavailable
	}

	raw, err := c.callTool(ctx, "context7.resolve-library-id", map[string]any{
		"libraryName": framework,
		"query":       query,
	})
	if err != nil {
		return nil, err
	}

	return parseResolveResponse(raw)
}

// GetDocs calls context7.get-library-docs on the bridge and returns a
// DocsResult with the raw markdown body. A per-call session is opened and
// closed for each invocation.
func (c *Client) GetDocs(ctx context.Context, libraryID, query, topic string, tokens int) (outbound.DocsResult, error) {
	if !c.available {
		return outbound.DocsResult{}, outbound.ErrDocsUnavailable
	}

	args := map[string]any{
		"context7CompatibleLibraryId": libraryID,
		"query":                       query,
		"tokens":                      tokens,
	}
	if topic != "" {
		args["topic"] = topic
	}

	raw, err := c.callTool(ctx, "context7.get-library-docs", args)
	if err != nil {
		return outbound.DocsResult{}, err
	}

	return parseGetDocsResponse(raw, libraryID)
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// callTool opens a per-call session, calls the named tool, closes the session,
// and returns the raw text content from the response. Session is always closed
// (even on error) to prevent leaks.
func (c *Client) callTool(ctx context.Context, tool string, args map[string]any) (string, error) {
	sess, err := c.open(ctx)
	if err != nil {
		return "", fmt.Errorf("context7: open session: %w", err)
	}
	defer sess.Close() //nolint:errcheck

	result, err := sess.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      tool,
		Arguments: args,
	})
	if err != nil {
		return "", fmt.Errorf("context7: call %s: %w", tool, err)
	}
	if result.IsError {
		text := extractText(result)
		return "", fmt.Errorf("context7: %s returned error: %s", tool, text)
	}

	text := extractText(result)
	if text == "" {
		return "", fmt.Errorf("context7: %s returned empty content", tool)
	}
	return text, nil
}

// extractText returns the text from the first TextContent in the result.
func extractText(result *sdkmcp.CallToolResult) string {
	for _, c := range result.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Response parsers
// ---------------------------------------------------------------------------

// resolveEntry is the JSON shape of a single library entry from resolve-library-id.
type resolveEntry struct {
	ID           string  `json:"id"`
	SnippetCount int     `json:"snippetCount"`
	Score        float64 `json:"score"`
	Version      string  `json:"version,omitempty"`
}

// resolveResponse is the JSON wrapper from context7.resolve-library-id.
type resolveResponse struct {
	Libraries []resolveEntry `json:"libraries"`
}

func parseResolveResponse(raw string) ([]outbound.LibraryEntry, error) {
	var resp resolveResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil, fmt.Errorf("context7: parse resolve-library-id response: %w", err)
	}

	entries := make([]outbound.LibraryEntry, 0, len(resp.Libraries))
	for _, e := range resp.Libraries {
		entries = append(entries, outbound.LibraryEntry{
			ID:       e.ID,
			Snippets: e.SnippetCount,
			Score:    e.Score,
			// An entry without a version-specific suffix is considered the
			// "main" (non-pinned) entry used as a thin-entry fallback.
			IsMain: e.Version == "",
		})
	}
	return entries, nil
}

// getDocsResponse is the JSON shape returned by context7.get-library-docs.
type getDocsResponse struct {
	Content  string  `json:"content"`
	ID       string  `json:"id"`
	Snippets int     `json:"snippets"`
	Score    float64 `json:"score"`
}

func parseGetDocsResponse(raw, requestedID string) (outbound.DocsResult, error) {
	var resp getDocsResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return outbound.DocsResult{}, fmt.Errorf("context7: parse get-library-docs response: %w", err)
	}

	id := resp.ID
	if id == "" {
		id = requestedID
	}

	return outbound.DocsResult{
		LibraryID: id,
		Snippets:  resp.Snippets,
		Score:     resp.Score,
		Body:      resp.Content,
	}, nil
}

// ---------------------------------------------------------------------------
// authRoundTripper — mirrors dispatcher/mcp/transport.go
// ---------------------------------------------------------------------------

// authRoundTripper injects Authorization and Origin headers on every request.
type authRoundTripper struct {
	base   http.RoundTripper
	token  string
	origin string
}

func (rt *authRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+rt.token)
	if rt.origin != "" {
		r.Header.Set("Origin", rt.origin)
	}
	return rt.base.RoundTrip(r) //nolint:wrapcheck
}

// wrapHTTPClient wraps base with authRoundTripper.
func wrapHTTPClient(base *http.Client, token, origin string) *http.Client {
	if base == nil {
		base = &http.Client{Timeout: 5 * time.Minute}
	}
	inner := base.Transport
	if inner == nil {
		inner = http.DefaultTransport
	}
	out := *base
	out.Transport = &authRoundTripper{base: inner, token: token, origin: origin}
	return &out
}
