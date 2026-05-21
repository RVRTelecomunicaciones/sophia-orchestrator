// Package mcp implements outbound.AgentDispatcher that routes dispatches
// through the sophia-agent-mcp host bridge via MCP Streamable HTTP.
//
// The dispatcher is THIN: it shapes the outbound MCP tool call, sends it
// over HTTP, maps the response to DispatchResult, and translates all
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
//   - The dispatcher MUST NOT import anything from sophia-agent-mcp module.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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

// Dispatcher implements outbound.AgentDispatcher for the MCP host-bridge.
type Dispatcher struct {
	cfg        Config
	httpClient *http.Client
}

// New constructs a Dispatcher. httpClient may be nil; a default client
// with a 5-minute timeout is created automatically.
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
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: time.Duration(cfg.TimeoutMS) * time.Millisecond,
		}
	}
	return &Dispatcher{cfg: cfg, httpClient: httpClient}
}

// Provider returns session.ProviderMCP.
func (d *Dispatcher) Provider() session.Provider { return session.ProviderMCP }

// SuggestedMaxConcurrent returns the concurrency hint for the SpawnGovernor.
func (d *Dispatcher) SuggestedMaxConcurrent() int { return d.cfg.Suggested }

// HealthCheck calls agent.health on the bridge and returns nil if at least
// one provider is installed and authenticated; non-nil otherwise.
func (d *Dispatcher) HealthCheck(ctx context.Context) error {
	resp, err := d.callTool(ctx, "agent.health", map[string]any{})
	if err != nil {
		return fmt.Errorf("mcp HealthCheck: %w", err)
	}
	// Bridge returns {"status":"ok","providers":[...]} or {"status":"error",...}.
	if resp.Status != "ok" {
		msg := resp.ErrorMsg
		if msg == "" {
			msg = "bridge returned non-ok status"
		}
		return fmt.Errorf("mcp HealthCheck: %s", msg)
	}
	return nil
}

// Dispatch builds an agent.run MCP tool call, sends it to the bridge,
// and maps the response to a DispatchResult. All bridge failure classes
// are mapped to wrapped outbound.ErrDispatchFailed carrying a failure tag.
func (d *Dispatcher) Dispatch(ctx context.Context, req outbound.DispatchRequest) (*outbound.DispatchResult, error) {
	if d.cfg.Provider == "" {
		return nil, fmt.Errorf("%w: provider_error: ErrProviderEmpty: SOPHIA_MCP_PROVIDER must be set when using MCP dispatcher",
			outbound.ErrDispatchFailed)
	}
	// provider is set FIRST so it is always present regardless of
	// subsequent model/phase logic.
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

	resp, err := d.callTool(ctx, "agent.run", args)
	if err != nil {
		// callTool already wrapped with the appropriate tag.
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

// --- MCP JSON-RPC wire types (local, no import from sophia-agent-mcp) ---

// jsonRPCRequest is a minimal JSON-RPC 2.0 request envelope.
type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// toolCallParams is the MCP tools/call params shape.
type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// jsonRPCResponse is the JSON-RPC 2.0 response envelope.
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
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

// callTool sends a JSON-RPC tools/call request to the bridge and returns
// the decoded result. It maps HTTP and framing errors to transport_error,
// 401/403 to auth_error.
func (d *Dispatcher) callTool(ctx context.Context, tool string, args map[string]any) (*bridgeResult, error) {
	rpcReq := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: toolCallParams{
			Name:      tool,
			Arguments: args,
		},
	}
	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("%w: transport_error: marshal: %w",
			outbound.ErrDispatchFailed, err)
	}

	endpoint := strings.TrimRight(d.cfg.BridgeURL, "/") + "/mcp"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: transport_error: build request: %w",
			outbound.ErrDispatchFailed, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+d.cfg.Token)
	if d.cfg.Origin != "" {
		httpReq.Header.Set("Origin", d.cfg.Origin)
	}
	httpReq.Header.Set("Accept", "application/json, text/event-stream")

	httpResp, err := d.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: transport_error: %w",
			outbound.ErrDispatchFailed, err)
	}
	defer httpResp.Body.Close() //nolint:errcheck

	switch httpResp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("%w: auth_error: bridge returned HTTP %d",
			outbound.ErrDispatchFailed, httpResp.StatusCode)
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: transport_error: bridge returned HTTP %d",
			outbound.ErrDispatchFailed, httpResp.StatusCode)
	}

	rawBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: transport_error: read body: %w",
			outbound.ErrDispatchFailed, err)
	}

	var rpcResp jsonRPCResponse
	if err := json.Unmarshal(rawBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("%w: transport_error: decode JSON-RPC: %w",
			outbound.ErrDispatchFailed, err)
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("%w: transport_error: JSON-RPC error %d: %s",
			outbound.ErrDispatchFailed, rpcResp.Error.Code, rpcResp.Error.Message)
	}

	// MCP wraps tool results in a content array: {"content":[{"type":"text","text":"..."}]}
	// or the raw tool result directly, depending on SDK version.
	result, err := extractBridgeResult(rpcResp.Result)
	if err != nil {
		return nil, fmt.Errorf("%w: transport_error: decode result: %w",
			outbound.ErrDispatchFailed, err)
	}
	return result, nil
}

// mcpToolResult is the outer MCP tools/call result shape.
type mcpToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

// extractBridgeResult decodes the MCP tools/call result envelope.
// The go-sdk wraps tool results in a {"content":[{"type":"text","text":"<json>"}]} shape.
func extractBridgeResult(raw json.RawMessage) (*bridgeResult, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty result")
	}

	var toolResult mcpToolResult
	if err := json.Unmarshal(raw, &toolResult); err != nil {
		return nil, fmt.Errorf("decode tool result: %w", err)
	}

	// Find the text content item.
	var text string
	for _, c := range toolResult.Content {
		if c.Type == "text" {
			text = c.Text
			break
		}
	}
	if text == "" {
		// Fallback: try to decode raw directly as bridgeResult
		// (some test stubs return it unwrapped).
		var br bridgeResult
		if err := json.Unmarshal(raw, &br); err != nil {
			return nil, fmt.Errorf("no text content in tool result")
		}
		return &br, nil
	}

	var br bridgeResult
	if err := json.Unmarshal([]byte(text), &br); err != nil {
		return nil, fmt.Errorf("decode bridge result text: %w", err)
	}
	return &br, nil
}

// parseEnvelopeRaw validates and normalises the envelope_raw JSON from the bridge.
// Returns an error if the value is not a JSON object.
func parseEnvelopeRaw(raw json.RawMessage) ([]byte, error) {
	// Trim whitespace.
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("envelope_raw is empty")
	}
	// Must be a JSON object ({...}).
	if trimmed[0] != '{' {
		return nil, fmt.Errorf("envelope_raw is not a JSON object (got %q)", string(trimmed[:min(20, len(trimmed))]))
	}
	// Validate it parses.
	var probe map[string]any
	if err := json.Unmarshal(trimmed, &probe); err != nil {
		return nil, fmt.Errorf("envelope_raw parse error: %w", err)
	}
	return trimmed, nil
}

// Compile-time interface check.
var _ outbound.AgentDispatcher = (*Dispatcher)(nil)
