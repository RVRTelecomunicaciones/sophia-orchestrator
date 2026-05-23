package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpdispatcher "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/dispatcher/mcp"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

const testToken = "test-bearer-token-1234"

// fakeBridge returns an httptest.Server that speaks a minimal JSON-RPC 2.0
// subset. The provided handler receives the decoded tool name + arguments and
// returns whatever the test wants as the tools/call result text.
func fakeBridge(t *testing.T, handler func(tool string, args map[string]any) (string, int)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Auth check.
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || auth[7:] != testToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		var req struct {
			Method string `json:"method"`
			ID     int    `json:"id"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		text, code := handler(req.Params.Name, req.Params.Arguments)
		if code != 0 {
			w.WriteHeader(code)
			return
		}

		// Wrap in MCP tools/call result envelope.
		result := map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": text},
			},
		}
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  result,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// bridgeResult builds the JSON string a bridge sends back as tool text.
func bridgeResultJSON(t *testing.T, status, errClass, errMsg, stdout, stderr string, envelopeRaw any, exitCode, durationMS int) string {
	t.Helper()
	m := map[string]any{
		"status":      status,
		"stdout":      stdout,
		"stderr":      stderr,
		"exit_code":   exitCode,
		"duration_ms": durationMS,
	}
	if errClass != "" {
		m["class"] = errClass
	}
	if errMsg != "" {
		m["error"] = errMsg
	}
	if envelopeRaw != nil {
		m["envelope_raw"] = envelopeRaw
	}
	b, err := json.Marshal(m)
	require.NoError(t, err)
	return string(b)
}

// newDispatcher creates a Dispatcher pointed at the given server URL.
// Provider is set to "opencode" so existing tests that do not test the
// provider field keep working after BUG-6b added the empty-provider guard.
func newDispatcher(srv *httptest.Server) *mcpdispatcher.Dispatcher {
	return mcpdispatcher.New(srv.Client(), mcpdispatcher.Config{
		BridgeURL: srv.URL,
		Token:     testToken,
		Origin:    "http://localhost",
		Transport: "streamable-http",
		TimeoutMS: 5000,
		Suggested: 4,
		Provider:  "opencode",
	})
}

// --- Provider / SuggestedMaxConcurrent ---

func TestProvider(t *testing.T) {
	d := mcpdispatcher.New(nil, mcpdispatcher.DefaultConfig())
	assert.Equal(t, session.ProviderMCP, d.Provider())
}

func TestSuggestedMaxConcurrent(t *testing.T) {
	d := mcpdispatcher.New(nil, mcpdispatcher.DefaultConfig())
	assert.Greater(t, d.SuggestedMaxConcurrent(), 0)
}

func TestSuggestedMaxConcurrent_DefaultValue(t *testing.T) {
	d := mcpdispatcher.New(nil, mcpdispatcher.Config{
		BridgeURL: "http://localhost:7775",
		Token:     "tok",
	})
	assert.Equal(t, 4, d.SuggestedMaxConcurrent())
}

// --- HealthCheck ---

func TestHealthCheck_OK(t *testing.T) {
	srv := fakeBridge(t, func(tool string, _ map[string]any) (string, int) {
		require.Equal(t, "agent.health", tool)
		return `{"status":"ok","providers":[{"name":"opencode","installed":true}]}`, 0
	})
	d := newDispatcher(srv)
	require.NoError(t, d.HealthCheck(context.Background()))
}

func TestHealthCheck_BridgeReturnsError(t *testing.T) {
	srv := fakeBridge(t, func(tool string, _ map[string]any) (string, int) {
		return `{"status":"error","error":"no providers installed"}`, 0
	})
	d := newDispatcher(srv)
	err := d.HealthCheck(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no providers installed")
}

func TestHealthCheck_Unauthorized(t *testing.T) {
	// Use wrong token to trigger 401.
	srv := fakeBridge(t, func(_ string, _ map[string]any) (string, int) {
		return "", 0 // won't be called
	})
	d := mcpdispatcher.New(srv.Client(), mcpdispatcher.Config{
		BridgeURL: srv.URL,
		Token:     "wrong-token",
		Origin:    "http://localhost",
		TimeoutMS: 5000,
	})
	err := d.HealthCheck(context.Background())
	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrDispatchFailed), "must wrap ErrDispatchFailed")
	assert.Contains(t, err.Error(), "auth_error")
}

// --- Dispatch happy path (A1) ---

func TestDispatch_HappyPath(t *testing.T) {
	envelope := map[string]any{
		"status":     "ok",
		"phase_type": "apply",
		"files":      []string{"main.go"},
	}
	srv := fakeBridge(t, func(tool string, args map[string]any) (string, int) {
		require.Equal(t, "agent.run", tool)
		assert.Equal(t, "test prompt", args["prompt"])
		return bridgeResultJSON(t, "ok", "", "", "some stdout", "", envelope, 0, 1234), 0
	})
	d := newDispatcher(srv)

	result, err := d.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt:       "test prompt",
		WorktreePath: "/tmp/worktree",
		TimeoutMS:    5000,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotNil(t, result.EnvelopeRaw, "envelope_raw must be present")
	assert.Equal(t, "mcp", result.AdapterID)
	assert.Equal(t, 1234, result.DurationMS)

	// Verify envelope bytes round-trip.
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(result.EnvelopeRaw, &decoded))
	assert.Equal(t, "ok", decoded["status"])
}

// --- Error mapping ---

// A2: bridge unreachable → transport_error
func TestDispatch_BridgeUnreachable_TransportError(t *testing.T) {
	d := mcpdispatcher.New(&http.Client{Timeout: 100 * time.Millisecond}, mcpdispatcher.Config{
		BridgeURL: "http://127.0.0.1:19999", // nothing listening
		Token:     testToken,
		TimeoutMS: 200,
		Provider:  "opencode",
	})
	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt: "hi",
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrDispatchFailed), "must wrap ErrDispatchFailed: %v", err)
	assert.Contains(t, err.Error(), "transport_error")
}

// A3: invalid token → auth_error
func TestDispatch_InvalidToken_AuthError(t *testing.T) {
	srv := fakeBridge(t, func(_ string, _ map[string]any) (string, int) {
		return "", 0 // won't be called (auth check fires first)
	})
	d := mcpdispatcher.New(srv.Client(), mcpdispatcher.Config{
		BridgeURL: srv.URL,
		Token:     "bad-token",
		Origin:    "http://localhost",
		TimeoutMS: 5000,
		Provider:  "opencode",
	})
	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "hi"})
	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrDispatchFailed))
	assert.Contains(t, err.Error(), "auth_error")
}

// A3b: 403 → auth_error
func TestDispatch_Forbidden_AuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Forbidden", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	d := mcpdispatcher.New(srv.Client(), mcpdispatcher.Config{
		BridgeURL: srv.URL,
		Token:     testToken,
		TimeoutMS: 5000,
		Provider:  "opencode",
	})
	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "hi"})
	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrDispatchFailed))
	assert.Contains(t, err.Error(), "auth_error")
}

// 500 → transport_error
func TestDispatch_HTTP500_TransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	d := mcpdispatcher.New(srv.Client(), mcpdispatcher.Config{
		BridgeURL: srv.URL,
		Token:     testToken,
		TimeoutMS: 5000,
		Provider:  "opencode",
	})
	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "hi"})
	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrDispatchFailed))
	assert.Contains(t, err.Error(), "transport_error")
}

// A4: MCP-level provider_error
func TestDispatch_ProviderError(t *testing.T) {
	srv := fakeBridge(t, func(_ string, _ map[string]any) (string, int) {
		return bridgeResultJSON(t, "error", "provider_error", "opencode binary not found", "", "opencode: not found", nil, 127, 0), 0
	})
	d := newDispatcher(srv)
	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "hi"})
	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrDispatchFailed))
	assert.Contains(t, err.Error(), "provider_error", "must tag as provider_error")
	assert.NotContains(t, err.Error(), "transport_error", "must NOT be tagged transport_error")
}

// A5: malformed envelope_raw → envelope_error, no panic
func TestDispatch_MalformedEnvelope_EnvelopeError(t *testing.T) {
	tests := []struct {
		name        string
		envelopeRaw any
	}{
		{"null", nil},                            // envelope_raw absent
		{"empty string", ""},                     // empty
		{"non-object string", `"not an object"`}, // string, not object
		{"array", []string{"a", "b"}},            // array
		{"truncated", `{"foo": `},                // truncated — but this can't be put in JSON directly
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := fakeBridge(t, func(_ string, _ map[string]any) (string, int) {
				return bridgeResultJSON(t, "ok", "", "", "stdout", "", tt.envelopeRaw, 0, 100), 0
			})
			d := newDispatcher(srv)
			_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "hi"})
			require.Error(t, err, "expected error for case %q", tt.name)
			require.True(t, errors.Is(err, outbound.ErrDispatchFailed))
			assert.Contains(t, err.Error(), "envelope_error")
		})
	}
}

// A5: must NOT panic on any malformed input.
func TestDispatch_MalformedJSONRPC_NoBodyCrash(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer "+testToken) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not valid json %%%`))
	}))
	t.Cleanup(srv.Close)

	d := mcpdispatcher.New(srv.Client(), mcpdispatcher.Config{
		BridgeURL: srv.URL,
		Token:     testToken,
		TimeoutMS: 5000,
		Provider:  "opencode",
	})
	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "hi"})
	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrDispatchFailed))
	assert.Contains(t, err.Error(), "transport_error")
}

// Timeout: context deadline exceeded → transport_error
func TestDispatch_Timeout_TransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Delay longer than the ctx deadline.
		select {
		case <-r.Context().Done():
			http.Error(w, "context cancelled", http.StatusServiceUnavailable)
		case <-time.After(5 * time.Second):
			http.Error(w, "delayed", http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	d := mcpdispatcher.New(srv.Client(), mcpdispatcher.Config{
		BridgeURL: srv.URL,
		Token:     testToken,
		TimeoutMS: 5000,
		Provider:  "opencode",
	})
	_, err := d.Dispatch(ctx, outbound.DispatchRequest{Prompt: "hi"})
	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrDispatchFailed))
	assert.Contains(t, err.Error(), "transport_error")
}

// A6: Provider() returns session.ProviderMCP
func TestDispatch_ProviderEnum(t *testing.T) {
	d := mcpdispatcher.New(nil, mcpdispatcher.DefaultConfig())
	assert.Equal(t, session.ProviderMCP, d.Provider())
	assert.Equal(t, session.Provider("mcp"), d.Provider())
}

// A7: SuggestedMaxConcurrent is documented positive integer
func TestDispatch_SuggestedPositive(t *testing.T) {
	d := mcpdispatcher.New(nil, mcpdispatcher.Config{
		Suggested: 2,
		TimeoutMS: 1000,
	})
	assert.Equal(t, 2, d.SuggestedMaxConcurrent())
}

// Model forwarding: per-phase model override sent to agent.run
func TestDispatch_ModelByPhase(t *testing.T) {
	var capturedModel string
	srv := fakeBridge(t, func(tool string, args map[string]any) (string, int) {
		if m, ok := args["model"].(string); ok {
			capturedModel = m
		}
		envelope := map[string]any{"status": "ok"}
		return bridgeResultJSON(t, "ok", "", "", "", "", envelope, 0, 10), 0
	})
	d := mcpdispatcher.New(srv.Client(), mcpdispatcher.Config{
		BridgeURL:    srv.URL,
		Token:        testToken,
		Origin:       "http://localhost",
		TimeoutMS:    5000,
		DefaultModel: "global-model",
		ModelByPhase: map[string]string{"apply": "apply-model"},
		Provider:     "opencode",
	})

	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt:    "hi",
		PhaseType: "apply",
	})
	require.NoError(t, err)
	assert.Equal(t, "apply-model", capturedModel)
}

func TestDispatch_DefaultModel(t *testing.T) {
	var capturedModel string
	srv := fakeBridge(t, func(_ string, args map[string]any) (string, int) {
		if m, ok := args["model"].(string); ok {
			capturedModel = m
		}
		envelope := map[string]any{"status": "ok"}
		return bridgeResultJSON(t, "ok", "", "", "", "", envelope, 0, 10), 0
	})
	d := mcpdispatcher.New(srv.Client(), mcpdispatcher.Config{
		BridgeURL:    srv.URL,
		Token:        testToken,
		Origin:       "http://localhost",
		TimeoutMS:    5000,
		DefaultModel: "default-model",
		Provider:     "opencode",
	})

	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt: "hi",
	})
	require.NoError(t, err)
	assert.Equal(t, "default-model", capturedModel)
}

// M1 (BUG-6b): Dispatch injects provider arg into every agent.run call.
func TestDispatch_InjectsProviderArg(t *testing.T) {
	var capturedProvider string
	srv := fakeBridge(t, func(tool string, args map[string]any) (string, int) {
		require.Equal(t, "agent.run", tool)
		if p, ok := args["provider"].(string); ok {
			capturedProvider = p
		}
		envelope := map[string]any{"status": "ok"}
		return bridgeResultJSON(t, "ok", "", "", "stdout", "", envelope, 0, 10), 0
	})

	d := mcpdispatcher.New(srv.Client(), mcpdispatcher.Config{
		BridgeURL: srv.URL,
		Token:     testToken,
		Origin:    "http://localhost",
		TimeoutMS: 5000,
		Provider:  "opencode",
	})

	result, err := d.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt:       "test prompt",
		WorktreePath: "/tmp/worktree",
		TimeoutMS:    5000,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "opencode", capturedProvider,
		"provider arg must be forwarded in tools/call arguments (M1)")
}

// M1 (BUG-6b): Dispatch also preserves existing required fields alongside provider.
func TestDispatch_InjectsProviderArg_AllRequiredFieldsPresent(t *testing.T) {
	type capturedArgs struct {
		Provider       string
		Prompt         string
		CWD            string
		TimeoutMS      float64
		OutputContract string
	}
	var got capturedArgs

	srv := fakeBridge(t, func(tool string, args map[string]any) (string, int) {
		got.Provider, _ = args["provider"].(string)
		got.Prompt, _ = args["prompt"].(string)
		got.CWD, _ = args["cwd"].(string)
		got.TimeoutMS, _ = args["timeout_ms"].(float64)
		got.OutputContract, _ = args["output_contract"].(string)
		envelope := map[string]any{"status": "ok"}
		return bridgeResultJSON(t, "ok", "", "", "", "", envelope, 0, 5), 0
	})

	d := mcpdispatcher.New(srv.Client(), mcpdispatcher.Config{
		BridgeURL: srv.URL,
		Token:     testToken,
		Origin:    "http://localhost",
		TimeoutMS: 5000,
		Provider:  "opencode",
	})

	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt:       "hello world",
		WorktreePath: "/repo",
		TimeoutMS:    3000,
	})
	require.NoError(t, err)
	assert.Equal(t, "opencode", got.Provider, "provider must be set")
	assert.Equal(t, "hello world", got.Prompt, "prompt must be forwarded")
	assert.Equal(t, "/repo", got.CWD, "cwd must be forwarded")
	assert.Equal(t, float64(3000), got.TimeoutMS, "timeout_ms must be forwarded")
	assert.Equal(t, "sophia.envelope.v1", got.OutputContract, "output_contract must be set")
}

// M3 (BUG-6b): Contract test — fake bridge asserts provider in agent.run args
// and returns a canned bridgeResult; dispatcher must return non-nil DispatchResult.
func TestDispatch_Contract_FakeBridge_SeesProvider(t *testing.T) {
	cannedEnvelope := map[string]any{
		"status":     "ok",
		"phase_type": "explore",
		"artifacts":  []string{"output.md"},
	}
	var bridgeSeenProvider string

	srv := fakeBridge(t, func(tool string, args map[string]any) (string, int) {
		require.Equal(t, "agent.run", tool,
			"contract: only agent.run should be called in a Dispatch")
		// Record what the bridge observes — this is the contract assertion.
		bridgeSeenProvider, _ = args["provider"].(string)
		return bridgeResultJSON(t, "ok", "", "", "agent output", "", cannedEnvelope, 0, 250), 0
	})

	d := mcpdispatcher.New(srv.Client(), mcpdispatcher.Config{
		BridgeURL: srv.URL,
		Token:     testToken,
		Origin:    "http://localhost",
		TimeoutMS: 5000,
		Provider:  "opencode",
	})

	result, err := d.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt:    "e2e validate: respond OK",
		PhaseType: "explore",
		TimeoutMS: 5000,
	})

	// Contract: bridge must have seen the provider.
	assert.Equal(t, "opencode", bridgeSeenProvider,
		"contract: fake bridge must observe arguments.provider == 'opencode' (M3)")

	// Dispatcher must return a non-nil result without error.
	require.NoError(t, err, "contract: Dispatch must succeed when fake bridge responds 200 ok (M3)")
	require.NotNil(t, result, "contract: DispatchResult must be non-nil (M3)")
	assert.Equal(t, "mcp", result.AdapterID, "contract: AdapterID must be 'mcp'")
	assert.NotNil(t, result.EnvelopeRaw, "contract: EnvelopeRaw must be present")

	var decodedEnv map[string]any
	require.NoError(t, json.Unmarshal(result.EnvelopeRaw, &decodedEnv))
	assert.Equal(t, "ok", decodedEnv["status"])
}

// M1-fail: Dispatch returns provider_error when Provider is empty (guard).
func TestDispatch_EmptyProvider_ReturnsProviderError(t *testing.T) {
	srv := fakeBridge(t, func(_ string, _ map[string]any) (string, int) {
		return "", 0 // should not be called
	})
	d := mcpdispatcher.New(srv.Client(), mcpdispatcher.Config{
		BridgeURL: srv.URL,
		Token:     testToken,
		TimeoutMS: 5000,
		Provider:  "", // intentionally empty
	})

	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "hi"})
	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrDispatchFailed))
	assert.Contains(t, err.Error(), "provider_error")
	assert.Contains(t, err.Error(), "ErrProviderEmpty")
}

// MCP error codes in JSON-RPC response → transport_error
func TestDispatch_JSONRPCErrorCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer "+testToken) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"error": map[string]any{
				"code":    -32601,
				"message": "method not found",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	d := mcpdispatcher.New(srv.Client(), mcpdispatcher.Config{
		BridgeURL: srv.URL,
		Token:     testToken,
		TimeoutMS: 5000,
		Provider:  "opencode",
	})
	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "hi"})
	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrDispatchFailed))
	assert.Contains(t, err.Error(), "transport_error")
}
