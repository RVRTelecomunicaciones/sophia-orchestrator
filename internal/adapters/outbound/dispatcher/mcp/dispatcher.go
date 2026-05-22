// Package mcp implements outbound.AgentDispatcher that routes dispatches
// through the sophia-agent-mcp host bridge via MCP Streamable HTTP.
//
// The dispatcher is THIN: it shapes the outbound MCP tool call, sends it
// via the SDK client, maps the response to DispatchResult, and translates all
// bridge failure classes into wrapped outbound.ErrDispatchFailed with
// an explicit failure tag. No policy logic lives here.
//
// Error tags (present in the wrapped error message):
//   - transport_error — network failure, non-2xx, framing error
//   - auth_error      — 401/403 from bridge
//   - provider_error  — bridge returned MCP-level status="error" with class provider_error
//   - envelope_error  — status="ok" but envelope_raw is missing or not a JSON object
//
// V1 constraints:
//   - Transport: Streamable HTTP only (stdio deferred to V2).
//   - Tools: agent.run and agent.health.
//   - Per-dispatch session lifecycle (Connect → CallTool → Close). No pooling.
//   - The dispatcher MUST NOT import anything from sophia-agent-mcp module.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// SuggestedMaxConcurrentDefault is the conservative V1 default for the
// host-bridge transport. A single local bridge serialises subprocess
// execution, so keeping concurrency low avoids queuing pressure.
const SuggestedMaxConcurrentDefault = 4

// Config tunes the MCP dispatcher.
type Config struct {
	// BridgeURL is the base URL of the sophia-agent-mcp HTTP server,
	// e.g. "http://127.0.0.1:7775". Required.
	BridgeURL string
	// Token is the bearer token sent in Authorization header. Required.
	Token string
	// Origin is the HTTP Origin header value sent to satisfy the bridge's
	// allowlist check. Default "http://localhost".
	Origin string
	// Transport selects the MCP transport. Only "streamable-http" is
	// supported in V1. Default "streamable-http".
	Transport string
	// TimeoutMS is the per-request HTTP timeout in milliseconds.
	// Default 300000 (5 minutes). Callers may pass a shorter
	// req.TimeoutMS on individual dispatches; this is the outer cap.
	TimeoutMS int
	// ProviderAllowlist lists the providers the bridge is allowed to
	// delegate to (e.g. ["opencode", "claude"]). Empty means "no
	// local filtering" — the bridge enforces its own allowlist.
	ProviderAllowlist []string
	// DefaultModel is the global default model forwarded to agent.run.
	// Empty means "let the bridge/provider choose".
	DefaultModel string
	// ModelByPhase maps a lowercase phase string to a model override.
	// Same semantics as opencode.Config.ModelByPhase.
	ModelByPhase map[string]string
	// Provider is the upstream provider name forwarded as the required
	// "provider" argument to agent.run (e.g. "opencode"). Set from
	// SOPHIA_MCP_PROVIDER via bootstrap. Bootstrap fails fast when MCP
	// is selected and this is empty.
	Provider string
	// Suggested is returned by SuggestedMaxConcurrent.
	// Zero falls back to SuggestedMaxConcurrentDefault.
	Suggested int
}

// DefaultConfig returns safe production defaults.
func DefaultConfig() Config {
	return Config{
		Transport: "streamable-http",
		TimeoutMS: 300_000,
		Origin:    "http://localhost",
		Suggested: SuggestedMaxConcurrentDefault,
	}
}

// sdkSession is the minimal surface the dispatcher needs from an SDK
// ClientSession. Tests substitute fakeSession; production uses the real
// *sdkmcp.ClientSession returned by client.Connect.
type sdkSession interface {
	CallTool(ctx context.Context, params *sdkmcp.CallToolParams) (*sdkmcp.CallToolResult, error)
	Close() error
}

// sessionOpener opens a new MCP session for a single dispatch or health-check.
// The real implementation wraps StreamableClientTransport + client.Connect.
// Tests inject a fake opener that returns a fakeSession.
type sessionOpener func(ctx context.Context) (sdkSession, error)

// Dispatcher implements outbound.AgentDispatcher for the MCP host-bridge.
type Dispatcher struct {
	cfg  Config
	open sessionOpener // default: real SDK opener; tests inject fakes
}

// New constructs a Dispatcher. httpClient may be nil; a default client with a
// 5-minute timeout is created automatically. The client is wrapped with
// authRoundTripper so every SDK HTTP request carries Authorization and Origin
// headers.
//
// The constructor signature New(httpClient *http.Client, cfg Config) is stable
// (unchanged from before the rewrite) — wire.go requires no edits.
func New(httpClient *http.Client, cfg Config) *Dispatcher {
	if cfg.Transport == "" {
		cfg.Transport = "streamable-http"
	}
	if cfg.Origin == "" {
		cfg.Origin = "http://localhost"
	}
	if cfg.TimeoutMS <= 0 {
		cfg.TimeoutMS = 300_000
	}
	if cfg.Suggested <= 0 {
		cfg.Suggested = SuggestedMaxConcurrentDefault
	}

	// Wrap the caller-supplied HTTP client with authRoundTripper so every
	// SDK HTTP request carries Authorization and Origin headers.
	wrapped := wrapHTTPClient(httpClient, cfg.Token, cfg.Origin)

	sdkClient := sdkmcp.NewClient(
		&sdkmcp.Implementation{Name: "sophia-orchestator", Version: "v2.1"},
		nil,
	)

	// Capture cfg and wrapped client into the opener closure.
	cfgSnapshot := cfg
	return &Dispatcher{
		cfg: cfgSnapshot,
		open: func(ctx context.Context) (sdkSession, error) {
			transport := &sdkmcp.StreamableClientTransport{
				Endpoint:             strings.TrimRight(cfgSnapshot.BridgeURL, "/") + "/mcp",
				HTTPClient:           wrapped,
				DisableStandaloneSSE: true, // per-dispatch sessions; no server-push needed
			}
			return sdkClient.Connect(ctx, transport, nil)
		},
	}
}

// Provider returns session.ProviderMCP.
func (d *Dispatcher) Provider() session.Provider { return session.ProviderMCP }

// SuggestedMaxConcurrent returns the concurrency hint for the SpawnGovernor.
func (d *Dispatcher) SuggestedMaxConcurrent() int { return d.cfg.Suggested }

// HealthCheck calls agent.health on the bridge and returns nil if at least
// one provider is installed and authenticated; non-nil otherwise.
func (d *Dispatcher) HealthCheck(ctx context.Context) error {
	resp, err := d.callSDK(ctx, "agent.health", map[string]any{})
	if err != nil {
		return fmt.Errorf("mcp HealthCheck: %w", err)
	}
	if resp.Status != "ok" {
		msg := resp.ErrorMsg
		if msg == "" {
			msg = "bridge returned non-ok status"
		}
		return fmt.Errorf("mcp HealthCheck: %s", msg)
	}
	return nil
}

// Dispatch builds an agent.run MCP tool call, sends it to the bridge via the
// SDK client, and maps the response to a DispatchResult. All bridge failure
// classes are mapped to wrapped outbound.ErrDispatchFailed carrying a failure tag.
//
// BUG-6b guard: if cfg.Provider is empty the dispatcher returns provider_error
// BEFORE opening any SDK session (Q4).
func (d *Dispatcher) Dispatch(ctx context.Context, req outbound.DispatchRequest) (*outbound.DispatchResult, error) {
	if d.cfg.Provider == "" {
		return nil, fmt.Errorf("%w: provider_error: ErrProviderEmpty: SOPHIA_MCP_PROVIDER must be set when using MCP dispatcher",
			outbound.ErrDispatchFailed)
	}

	// provider is set FIRST so it is always present regardless of
	// subsequent model/phase logic (BUG-6b / Q3).
	args := map[string]any{
		"provider":        d.cfg.Provider,
		"prompt":          req.Prompt,
		"cwd":             req.WorktreePath,
		"timeout_ms":      req.TimeoutMS,
		"output_contract": "sophia.envelope.v1",
	}
	if model := d.modelFor(req.PhaseType); model != "" {
		args["model"] = model
	}

	resp, err := d.callSDK(ctx, "agent.run", args)
	if err != nil {
		return nil, err
	}

	// MCP-level error from bridge (provider missing, timeout, etc.).
	if resp.Status == "error" {
		tag := "provider_error"
		if resp.ErrorClass != "" {
			tag = resp.ErrorClass
		}
		return nil, fmt.Errorf("%w: %s: %s",
			outbound.ErrDispatchFailed, tag, resp.ErrorMsg)
	}

	// status == "ok" — extract envelope_raw.
	if resp.EnvelopeRaw == nil {
		return nil, fmt.Errorf("%w: envelope_error: bridge returned ok but envelope_raw is absent",
			outbound.ErrDispatchFailed)
	}
	envBytes, err := parseEnvelopeRaw(resp.EnvelopeRaw)
	if err != nil {
		return nil, fmt.Errorf("%w: envelope_error: %w",
			outbound.ErrDispatchFailed, err)
	}

	return &outbound.DispatchResult{
		ExitCode:    resp.ExitCode,
		Stdout:      []byte(resp.Stdout),
		Stderr:      []byte(resp.Stderr),
		EnvelopeRaw: envBytes,
		DurationMS:  resp.DurationMS,
		AdapterID:   "mcp",
	}, nil
}

// modelFor returns the model for the given phase, following the same
// lookup order as opencode.Dispatcher.
func (d *Dispatcher) modelFor(phaseType string) string {
	if phaseType != "" {
		if m, ok := d.cfg.ModelByPhase[phaseType]; ok && m != "" {
			return m
		}
	}
	return d.cfg.DefaultModel
}

// callSDK opens a per-dispatch SDK session, calls the named tool, closes the
// session, and decodes the result into a bridgeResult. All SDK and HTTP errors
// are mapped to the existing error taxonomy.
//
// R1 (BUG-7): the SDK's client.Connect handles the MCP initialize handshake
// automatically before any CallTool, so "method invalid during initialization"
// cannot originate from this dispatcher.
func (d *Dispatcher) callSDK(ctx context.Context, tool string, args map[string]any) (*bridgeResult, error) {
	sess, err := d.open(ctx)
	if err != nil {
		return nil, classifyConnectError(err)
	}
	defer sess.Close() //nolint:errcheck

	result, err := sess.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      tool,
		Arguments: args,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("%w: transport_error: context: %w",
				outbound.ErrDispatchFailed, err)
		}
		return nil, fmt.Errorf("%w: transport_error: %w",
			outbound.ErrDispatchFailed, err)
	}

	// IsError=true → provider_error with the text content as detail.
	if result.IsError {
		text := extractTextContent(result)
		return nil, fmt.Errorf("%w: provider_error: %s",
			outbound.ErrDispatchFailed, text)
	}

	// Decode the bridge envelope from the text content.
	text := extractTextContent(result)
	if text == "" {
		return nil, fmt.Errorf("%w: envelope_error: tool result has no text content",
			outbound.ErrDispatchFailed)
	}
	var br bridgeResult
	if err := json.Unmarshal([]byte(text), &br); err != nil {
		return nil, fmt.Errorf("%w: envelope_error: decode bridge result: %w",
			outbound.ErrDispatchFailed, err)
	}
	return &br, nil
}

// classifyConnectError maps errors returned by client.Connect (before any
// CallTool) to the error taxonomy.
//
// The SDK at v1.6.0 does not export typed HTTP status errors. When the bridge
// returns 401 or 403, the SDK error string contains "Unauthorized" or
// "Forbidden" respectively (see streamable.go checkResponse). String matching
// is the documented fallback strategy per design §Error Taxonomy Mapping.
func classifyConnectError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: transport_error: context: %w",
			outbound.ErrDispatchFailed, err)
	}
	msg := err.Error()
	if strings.Contains(msg, "Unauthorized") || strings.Contains(msg, "Forbidden") ||
		strings.Contains(msg, "401") || strings.Contains(msg, "403") {
		return fmt.Errorf("%w: auth_error: %w",
			outbound.ErrDispatchFailed, err)
	}
	return fmt.Errorf("%w: transport_error: %w",
		outbound.ErrDispatchFailed, err)
}

// extractTextContent returns the text from the first TextContent item in a
// CallToolResult, or the empty string if none is found.
func extractTextContent(result *sdkmcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	for _, c := range result.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// bridgeResult is the decoded result payload from a bridge tool call.
type bridgeResult struct {
	Status      string          `json:"status"`
	ErrorMsg    string          `json:"error"`
	ErrorClass  string          `json:"class"`
	Stdout      string          `json:"stdout"`
	Stderr      string          `json:"stderr"`
	ExitCode    int             `json:"exit_code"`
	DurationMS  int             `json:"duration_ms"`
	EnvelopeRaw json.RawMessage `json:"envelope_raw"`
}

// parseEnvelopeRaw validates and normalises the envelope_raw JSON from the bridge.
// Returns an error if the value is not a JSON object.
func parseEnvelopeRaw(raw json.RawMessage) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("envelope_raw is empty")
	}
	if trimmed[0] != '{' {
		return nil, fmt.Errorf("envelope_raw is not a JSON object (got %q)", string(trimmed[:min(20, len(trimmed))]))
	}
	var probe map[string]any
	if err := json.Unmarshal(trimmed, &probe); err != nil {
		return nil, fmt.Errorf("envelope_raw parse error: %w", err)
	}
	return trimmed, nil
}

// Compile-time interface check.
var _ outbound.AgentDispatcher = (*Dispatcher)(nil)
