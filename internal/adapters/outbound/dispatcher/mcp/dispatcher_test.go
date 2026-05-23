package mcp

// White-box test file (package mcp, not mcp_test). White-box access is
// required to inject fakeSession via the unexported open field on Dispatcher.
//
// All scenario IDs reference spec.md:
//   N1 — session lifecycle (open, CallTool once, close)
//   N2 — context cancellation → transport_error
//   N4 — two concurrent dispatches open independent sessions
//   P1 — SDK transport error → transport_error
//   P2 — HTTP 401 → auth_error
//   P3 — HTTP 403 → auth_error
//   P4 — tool result IsError=true → provider_error
//   P5 — malformed envelope_raw → envelope_error
//   Q1 — Provider() returns session.ProviderMCP
//   Q2 — SuggestedMaxConcurrent() > 0
//   Q3 — args["provider"] injected on every agent.run
//   Q4 — empty provider returns provider_error BEFORE opening any session (BUG-6b)
//   R1 — BUG-7 regression: Connect is called before CallTool (SDK handles initialize)

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// fakeSession is an in-process implementation of sdkSession.
// Tests configure it to return canned results or errors.
type fakeSession struct {
	mu        sync.Mutex
	callCount int
	callArgs  []map[string]any // arguments from each CallTool invocation
	callTool  string           // last tool name called
	result    *sdkmcp.CallToolResult
	callErr   error

	// blockUntil lets tests simulate a slow session (for N2 cancel test).
	blockUntil chan struct{} // if non-nil, CallTool blocks until this is closed

	closed bool
}

func (f *fakeSession) CallTool(ctx context.Context, params *sdkmcp.CallToolParams) (*sdkmcp.CallToolResult, error) {
	f.mu.Lock()
	f.callCount++
	f.callTool = params.Name
	args, _ := params.Arguments.(map[string]any)
	// Deep-clone args so tests can inspect them after the call.
	clone := make(map[string]any, len(args))
	for k, v := range args {
		clone[k] = v
	}
	f.callArgs = append(f.callArgs, clone)
	blockCh := f.blockUntil
	f.mu.Unlock()

	if blockCh != nil {
		// Block until either the context is cancelled or blockCh is closed.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-blockCh:
		}
	}

	if f.callErr != nil {
		return nil, f.callErr
	}
	return f.result, nil
}

func (f *fakeSession) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

// mkTextResult wraps a JSON string as an MCP CallToolResult with text content.
func mkTextResult(text string) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: text},
		},
	}
}

// mkErrorResult builds an IsError=true CallToolResult with text content.
func mkErrorResult(text string) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{
		IsError: true,
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: text},
		},
	}
}

// bridgeJSON builds the JSON string a bridge sends back as tool text content.
func bridgeJSON(status, errClass, errMsg, stdout, stderr string, envelopeRaw any, exitCode, durationMS int) string {
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
	b, _ := json.Marshal(m)
	return string(b)
}

// newTestDispatcher builds a Dispatcher whose open field is replaced by the
// provided sessionOpener. The Config always has Provider="opencode" unless
// the caller overrides it with cfgFns.
func newTestDispatcher(opener sessionOpener, cfgFns ...func(*Config)) *Dispatcher {
	cfg := Config{
		BridgeURL: "http://127.0.0.1:7775",
		Token:     "test-bearer-token-1234",
		Origin:    "http://localhost",
		Transport: "streamable-http",
		TimeoutMS: 5000,
		Suggested: 4,
		Provider:  "opencode",
	}
	for _, fn := range cfgFns {
		fn(&cfg)
	}
	return &Dispatcher{cfg: cfg, open: opener}
}

// singleOpener returns an opener that always returns the given fakeSession.
func singleOpener(sess *fakeSession) sessionOpener {
	return func(_ context.Context) (sdkSession, error) {
		return sess, nil
	}
}

// errorOpener returns an opener that always fails with the given error.
func errorOpener(err error) sessionOpener {
	return func(_ context.Context) (sdkSession, error) {
		return nil, err
	}
}

// ---------------------------------------------------------------------------
// Q1, Q2 — Public contract (unchanged surface)
// ---------------------------------------------------------------------------

// Q1: Provider() returns session.ProviderMCP.
func TestProvider(t *testing.T) {
	d := New(nil, DefaultConfig())
	assert.Equal(t, session.ProviderMCP, d.Provider())
}

// Q1 (alias check)
func TestDispatch_ProviderEnum(t *testing.T) {
	d := New(nil, DefaultConfig())
	assert.Equal(t, session.ProviderMCP, d.Provider())
	assert.Equal(t, session.Provider("mcp"), d.Provider())
}

// Q2: SuggestedMaxConcurrent() > 0.
func TestSuggestedMaxConcurrent(t *testing.T) {
	d := New(nil, DefaultConfig())
	assert.Greater(t, d.SuggestedMaxConcurrent(), 0)
}

// Q2: Default value is 4.
func TestSuggestedMaxConcurrent_DefaultValue(t *testing.T) {
	d := New(nil, Config{
		BridgeURL: "http://localhost:7775",
		Token:     "tok",
	})
	assert.Equal(t, 4, d.SuggestedMaxConcurrent())
}

// Q2: Custom value respected.
func TestDispatch_SuggestedPositive(t *testing.T) {
	d := New(nil, Config{Suggested: 2, TimeoutMS: 1000})
	assert.Equal(t, 2, d.SuggestedMaxConcurrent())
}

// ---------------------------------------------------------------------------
// N1 — Session lifecycle
// ---------------------------------------------------------------------------

// N1: Dispatch opens exactly one session, calls one tool, closes the session.
func TestDispatch_OpensSingleSessionAndCloses(t *testing.T) {
	envelope := map[string]any{"status": "ok"}
	sess := &fakeSession{
		result: mkTextResult(bridgeJSON("ok", "", "", "stdout", "", envelope, 0, 100)),
	}

	d := newTestDispatcher(singleOpener(sess))
	result, err := d.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt:       "hello",
		WorktreePath: "/tmp/repo",
		TimeoutMS:    1000,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "mcp", result.AdapterID)

	// Exactly one CallTool for agent.run.
	assert.Equal(t, 1, sess.callCount, "N1: exactly one CallTool call expected")
	assert.Equal(t, "agent.run", sess.callTool, "N1: tool name must be agent.run")

	// Session must be closed.
	assert.True(t, sess.closed, "N1: session must be closed after Dispatch")
}

// ---------------------------------------------------------------------------
// N2 — Context cancellation
// ---------------------------------------------------------------------------

// N2: When context is cancelled during session.CallTool, Dispatch returns
// a transport_error wrapping ErrDispatchFailed.
func TestDispatch_ContextCancellation_TransportError(t *testing.T) {
	blockCh := make(chan struct{})
	sess := &fakeSession{blockUntil: blockCh}

	d := newTestDispatcher(singleOpener(sess))

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)
	var dispatchErr error
	go func() {
		defer wg.Done()
		_, dispatchErr = d.Dispatch(ctx, outbound.DispatchRequest{Prompt: "hi"})
	}()

	// Cancel the context while CallTool is blocking.
	cancel()
	wg.Wait()

	require.Error(t, dispatchErr)
	require.True(t, errors.Is(dispatchErr, outbound.ErrDispatchFailed),
		"N2: error must wrap ErrDispatchFailed: %v", dispatchErr)
	assert.Contains(t, dispatchErr.Error(), "transport_error", "N2: must be tagged transport_error")
}

// ---------------------------------------------------------------------------
// N4 — Concurrent sessions are independent
// ---------------------------------------------------------------------------

// N4: Two concurrent Dispatch calls must open independent sessions.
func TestDispatch_ConcurrentSessionsIndependent(t *testing.T) {
	envelope := map[string]any{"status": "ok"}

	var mu sync.Mutex
	opened := make([]*fakeSession, 0, 2)

	opener := func(_ context.Context) (sdkSession, error) {
		sess := &fakeSession{
			result: mkTextResult(bridgeJSON("ok", "", "", "", "", envelope, 0, 50)),
		}
		mu.Lock()
		opened = append(opened, sess)
		mu.Unlock()
		return sess, nil
	}

	d := newTestDispatcher(opener)

	var wg sync.WaitGroup
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			_, _ = d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "concurrent"})
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	require.Len(t, opened, 2, "N4: two independent sessions must be opened")
	// Sessions must be distinct objects.
	assert.NotSame(t, opened[0], opened[1], "N4: sessions must not be the same object")
	// Each session called exactly once.
	assert.Equal(t, 1, opened[0].callCount, "N4: session[0] must have exactly one CallTool")
	assert.Equal(t, 1, opened[1].callCount, "N4: session[1] must have exactly one CallTool")
}

// ---------------------------------------------------------------------------
// P1 — SDK transport error
// ---------------------------------------------------------------------------

// P1: If the sessionOpener returns a transport error (network, TLS, DNS),
// Dispatch returns a transport_error.
func TestDispatch_SDKTransportError_Transport(t *testing.T) {
	connErr := fmt.Errorf("dial tcp: connection refused")
	d := newTestDispatcher(errorOpener(connErr))

	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "hi"})

	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrDispatchFailed),
		"P1: must wrap ErrDispatchFailed: %v", err)
	assert.Contains(t, err.Error(), "transport_error", "P1: must be tagged transport_error")
}

// ---------------------------------------------------------------------------
// P2 — HTTP 401 → auth_error
// ---------------------------------------------------------------------------

// P2: If the bridge returns 401 during initialization, Dispatch returns auth_error.
func TestDispatch_HTTP401_Auth(t *testing.T) {
	// Simulate what the SDK returns when the bridge sends HTTP 401:
	// the SDK error contains "Unauthorized" in the message.
	authErr := fmt.Errorf("POST http://localhost/mcp: Unauthorized")
	d := newTestDispatcher(errorOpener(authErr))

	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "hi"})

	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrDispatchFailed),
		"P2: must wrap ErrDispatchFailed: %v", err)
	assert.Contains(t, err.Error(), "auth_error",
		"P2: 401 from bridge must map to auth_error")
	assert.NotContains(t, err.Error(), "transport_error",
		"P2: must NOT be tagged transport_error")
}

// ---------------------------------------------------------------------------
// P3 — HTTP 403 → auth_error
// ---------------------------------------------------------------------------

// P3: If the bridge returns 403 during initialization, Dispatch returns auth_error.
func TestDispatch_HTTP403_Auth(t *testing.T) {
	// Simulate what the SDK returns for HTTP 403 Forbidden.
	authErr := fmt.Errorf("POST http://localhost/mcp: Forbidden")
	d := newTestDispatcher(errorOpener(authErr))

	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "hi"})

	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrDispatchFailed),
		"P3: must wrap ErrDispatchFailed: %v", err)
	assert.Contains(t, err.Error(), "auth_error",
		"P3: 403 from bridge must map to auth_error")
	assert.NotContains(t, err.Error(), "transport_error",
		"P3: must NOT be tagged transport_error")
}

// ---------------------------------------------------------------------------
// P4 — Tool result IsError=true → provider_error
// ---------------------------------------------------------------------------

// P4: When the bridge returns CallToolResult{IsError:true}, Dispatch returns
// a provider_error with the text content embedded.
func TestDispatch_IsErrorResult_Provider(t *testing.T) {
	sess := &fakeSession{
		result: mkErrorResult("opencode binary not found"),
	}
	d := newTestDispatcher(singleOpener(sess))

	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "hi"})

	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrDispatchFailed),
		"P4: must wrap ErrDispatchFailed: %v", err)
	assert.Contains(t, err.Error(), "provider_error",
		"P4: IsError=true must map to provider_error")
	assert.Contains(t, err.Error(), "opencode binary not found",
		"P4: error message must include bridge text")
}

// ---------------------------------------------------------------------------
// P5 — Malformed envelope_raw → envelope_error
// ---------------------------------------------------------------------------

// P5: Table-driven test for various malformed envelope_raw values.
func TestDispatch_MalformedEnvelope_Envelope(t *testing.T) {
	tests := []struct {
		name        string
		envelopeRaw any
	}{
		{"null (absent)", nil},
		{"empty string", ""},
		{"non-object string", `"not an object"`},
		{"array", []string{"a", "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess := &fakeSession{
				result: mkTextResult(bridgeJSON("ok", "", "", "stdout", "", tt.envelopeRaw, 0, 100)),
			}
			d := newTestDispatcher(singleOpener(sess))

			_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "hi"})

			require.Error(t, err, "P5: expected error for case %q", tt.name)
			require.True(t, errors.Is(err, outbound.ErrDispatchFailed),
				"P5: must wrap ErrDispatchFailed: %v", err)
			assert.Contains(t, err.Error(), "envelope_error",
				"P5: malformed envelope_raw must map to envelope_error")
		})
	}
}

// ---------------------------------------------------------------------------
// Q3 — args["provider"] injected on every agent.run
// ---------------------------------------------------------------------------

// Q3: The "provider" arg must be injected in every CallTool arguments.
func TestDispatch_InjectsProviderArg(t *testing.T) {
	envelope := map[string]any{"status": "ok"}
	sess := &fakeSession{
		result: mkTextResult(bridgeJSON("ok", "", "", "stdout", "", envelope, 0, 10)),
	}
	d := newTestDispatcher(singleOpener(sess), func(c *Config) {
		c.Provider = "opencode"
	})

	result, err := d.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt:       "test prompt",
		WorktreePath: "/tmp/worktree",
		TimeoutMS:    5000,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, sess.callArgs, 1)
	assert.Equal(t, "opencode", sess.callArgs[0]["provider"],
		"Q3: provider arg must be forwarded in every agent.run call (BUG-6b)")
}

// Q3: All required fields are present in args.
func TestDispatch_InjectsProviderArg_AllRequiredFieldsPresent(t *testing.T) {
	envelope := map[string]any{"status": "ok"}
	sess := &fakeSession{
		result: mkTextResult(bridgeJSON("ok", "", "", "", "", envelope, 0, 5)),
	}
	d := newTestDispatcher(singleOpener(sess), func(c *Config) {
		c.Provider = "opencode"
	})

	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt:       "hello world",
		WorktreePath: "/repo",
		TimeoutMS:    3000,
	})

	require.NoError(t, err)
	require.Len(t, sess.callArgs, 1)
	args := sess.callArgs[0]
	assert.Equal(t, "opencode", args["provider"], "provider must be set")
	assert.Equal(t, "hello world", args["prompt"], "prompt must be forwarded")
	assert.Equal(t, "/repo", args["cwd"], "cwd must be forwarded")
	assert.EqualValues(t, 3000, args["timeout_ms"], "timeout_ms must be forwarded")
	assert.Equal(t, "sophia.envelope.v1", args["output_contract"], "output_contract must be set")
}

// Q3 (contract): fake session asserts provider and returns a canned result.
func TestDispatch_Contract_FakeSession_SeesProvider(t *testing.T) {
	cannedEnvelope := map[string]any{
		"status":     "ok",
		"phase_type": "explore",
		"artifacts":  []string{"output.md"},
	}
	sess := &fakeSession{
		result: mkTextResult(bridgeJSON("ok", "", "", "agent output", "", cannedEnvelope, 0, 250)),
	}
	d := newTestDispatcher(singleOpener(sess), func(c *Config) {
		c.Provider = "opencode"
	})

	result, err := d.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt:    "e2e validate: respond OK",
		PhaseType: "explore",
		TimeoutMS: 5000,
	})

	require.NoError(t, err, "contract: Dispatch must succeed when fake session returns 200 ok")
	require.NotNil(t, result, "contract: DispatchResult must be non-nil")
	assert.Equal(t, "mcp", result.AdapterID, "contract: AdapterID must be 'mcp'")
	require.NotNil(t, result.EnvelopeRaw, "contract: EnvelopeRaw must be present")

	require.Len(t, sess.callArgs, 1)
	assert.Equal(t, "opencode", sess.callArgs[0]["provider"],
		"contract: fake session must observe arguments.provider == 'opencode'")

	var decodedEnv map[string]any
	require.NoError(t, json.Unmarshal(result.EnvelopeRaw, &decodedEnv))
	assert.Equal(t, "ok", decodedEnv["status"])
}

// ---------------------------------------------------------------------------
// Q4 — Empty provider guard (BUG-6b)
// ---------------------------------------------------------------------------

// Q4: When cfg.Provider is empty, Dispatch returns provider_error BEFORE
// opening any SDK session.
func TestDispatch_EmptyProvider_ReturnsProviderError(t *testing.T) {
	var sessionOpened atomic.Bool
	opener := func(_ context.Context) (sdkSession, error) {
		sessionOpened.Store(true)
		return &fakeSession{}, nil
	}
	d := newTestDispatcher(opener, func(c *Config) {
		c.Provider = "" // intentionally empty
	})

	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "hi"})

	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrDispatchFailed))
	assert.Contains(t, err.Error(), "provider_error", "Q4: must be tagged provider_error")
	assert.Contains(t, err.Error(), "ErrProviderEmpty", "Q4: must contain ErrProviderEmpty")
	assert.False(t, sessionOpened.Load(), "Q4: no session must be opened when provider is empty")
}

// ---------------------------------------------------------------------------
// R1 — BUG-7 regression: SDK handles initialize handshake
// ---------------------------------------------------------------------------

// R1: The sessionOpener (which wraps client.Connect) is called BEFORE
// any CallTool. This proves that the SDK's initialize handshake happens
// automatically via Connect, so "method invalid during initialization"
// cannot originate from this dispatcher.
//
// The test asserts the call ORDER: opener → CallTool. A dispatcher that
// skipped Connect and called CallTool directly (the old raw HTTP approach)
// would break this ordering.
func TestDispatch_InitializeHandshakeSatisfied_NoBUG7(t *testing.T) {
	type event struct{ what string }
	var mu sync.Mutex
	var events []event

	envelope := map[string]any{"status": "ok"}
	innerSess := &fakeSession{
		result: mkTextResult(bridgeJSON("ok", "", "", "", "", envelope, 0, 1)),
	}

	// interceptor records CallTool events; the opener records Connect events.
	interceptor := &callOrderFakeSession{
		inner:  innerSess,
		result: innerSess.result,
		onCall: func() {
			mu.Lock()
			events = append(events, event{"callTool"})
			mu.Unlock()
		},
	}
	innerSess.result = nil // inner result is now owned by interceptor

	opener := func(_ context.Context) (sdkSession, error) {
		mu.Lock()
		events = append(events, event{"connect"}) // represents initialize handshake
		mu.Unlock()
		return interceptor, nil
	}

	d := newTestDispatcher(opener)
	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "hi"})

	require.NoError(t, err, "R1: Dispatch must succeed")

	mu.Lock()
	defer mu.Unlock()

	require.Len(t, events, 2, "R1: expected exactly 2 events (connect + callTool)")
	assert.Equal(t, "connect", events[0].what,
		"R1: Connect (initialize handshake) must happen BEFORE CallTool")
	assert.Equal(t, "callTool", events[1].what,
		"R1: CallTool must happen AFTER Connect")
}

// callOrderFakeSession wraps a fakeSession and calls onCall before delegating.
type callOrderFakeSession struct {
	inner  *fakeSession
	result *sdkmcp.CallToolResult
	onCall func()
}

func (s *callOrderFakeSession) CallTool(ctx context.Context, params *sdkmcp.CallToolParams) (*sdkmcp.CallToolResult, error) {
	s.onCall()
	s.inner.result = s.result
	return s.inner.CallTool(ctx, params)
}

func (s *callOrderFakeSession) Close() error {
	return s.inner.Close()
}

// ---------------------------------------------------------------------------
// Model forwarding
// ---------------------------------------------------------------------------

// Q3 (extended): per-phase model override is sent in args.
func TestDispatch_ModelByPhase(t *testing.T) {
	envelope := map[string]any{"status": "ok"}
	sess := &fakeSession{
		result: mkTextResult(bridgeJSON("ok", "", "", "", "", envelope, 0, 10)),
	}
	d := newTestDispatcher(singleOpener(sess), func(c *Config) {
		c.DefaultModel = "global-model"
		c.ModelByPhase = map[string]string{"apply": "apply-model"}
	})

	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt:    "hi",
		PhaseType: "apply",
	})

	require.NoError(t, err)
	require.Len(t, sess.callArgs, 1)
	assert.Equal(t, "apply-model", sess.callArgs[0]["model"],
		"per-phase model must override default")
}

// Q3 (default model): default model is used when no phase override exists.
func TestDispatch_DefaultModel(t *testing.T) {
	envelope := map[string]any{"status": "ok"}
	sess := &fakeSession{
		result: mkTextResult(bridgeJSON("ok", "", "", "", "", envelope, 0, 10)),
	}
	d := newTestDispatcher(singleOpener(sess), func(c *Config) {
		c.DefaultModel = "default-model"
	})

	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt: "hi",
	})

	require.NoError(t, err)
	require.Len(t, sess.callArgs, 1)
	assert.Equal(t, "default-model", sess.callArgs[0]["model"],
		"default model must be forwarded when no phase override")
}

// ---------------------------------------------------------------------------
// HealthCheck tests
// ---------------------------------------------------------------------------

// HealthCheck success path.
func TestHealthCheck_OK_SDKPath(t *testing.T) {
	sess := &fakeSession{
		result: mkTextResult(`{"status":"ok","providers":[{"name":"opencode","installed":true}]}`),
	}
	d := newTestDispatcher(singleOpener(sess))
	require.NoError(t, d.HealthCheck(context.Background()))
	assert.Equal(t, "agent.health", sess.callTool, "HealthCheck must call agent.health")
}

// HealthCheck when bridge returns status=error.
func TestHealthCheck_BridgeReturnsError(t *testing.T) {
	sess := &fakeSession{
		result: mkTextResult(`{"status":"error","error":"no providers installed"}`),
	}
	d := newTestDispatcher(singleOpener(sess))
	err := d.HealthCheck(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no providers installed")
}

// HealthCheck when opener returns auth error.
func TestHealthCheck_Unauthorized(t *testing.T) {
	authErr := fmt.Errorf("POST http://localhost/mcp: Unauthorized")
	d := newTestDispatcher(errorOpener(authErr))
	err := d.HealthCheck(context.Background())
	require.Error(t, err)
	// HealthCheck wraps with "mcp HealthCheck:", then the dispatcher wraps with ErrDispatchFailed + auth_error.
	assert.True(t, errors.Is(err, outbound.ErrDispatchFailed),
		"HealthCheck auth error must wrap ErrDispatchFailed: %v", err)
	assert.Contains(t, err.Error(), "auth_error")
}

// ---------------------------------------------------------------------------
// Happy-path Dispatch
// ---------------------------------------------------------------------------

// Full happy-path: verifies envelope_raw round-trip and AdapterID.
func TestDispatch_HappyPath(t *testing.T) {
	envelope := map[string]any{
		"status":     "ok",
		"phase_type": "apply",
		"files":      []string{"main.go"},
	}
	sess := &fakeSession{
		result: mkTextResult(bridgeJSON("ok", "", "", "some stdout", "", envelope, 0, 1234)),
	}
	d := newTestDispatcher(singleOpener(sess))

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

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(result.EnvelopeRaw, &decoded))
	assert.Equal(t, "ok", decoded["status"])
}

// ---------------------------------------------------------------------------
// Error-mapping completeness checks
// ---------------------------------------------------------------------------

// MCP-level provider_error from bridge (status=error, class=provider_error).
func TestDispatch_ProviderError(t *testing.T) {
	sess := &fakeSession{
		result: mkTextResult(bridgeJSON("error", "provider_error", "opencode binary not found", "", "opencode: not found", nil, 127, 0)),
	}
	d := newTestDispatcher(singleOpener(sess))

	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "hi"})

	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrDispatchFailed))
	assert.Contains(t, err.Error(), "provider_error")
	assert.NotContains(t, err.Error(), "transport_error")
}

// Bridge unreachable → transport_error (P1 via integration-style opener error).
func TestDispatch_BridgeUnreachable_TransportError(t *testing.T) {
	connErr := fmt.Errorf("dial tcp 127.0.0.1:19999: connect: connection refused")
	d := newTestDispatcher(errorOpener(connErr))

	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "hi"})

	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrDispatchFailed), "must wrap ErrDispatchFailed: %v", err)
	assert.Contains(t, err.Error(), "transport_error")
}

// Invalid token (401) → auth_error (P2 alias for TestDispatch_InvalidToken_AuthError).
func TestDispatch_InvalidToken_AuthError(t *testing.T) {
	authErr := fmt.Errorf("calling initialize: sending initialize: Unauthorized")
	d := newTestDispatcher(errorOpener(authErr))

	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "hi"})

	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrDispatchFailed))
	assert.Contains(t, err.Error(), "auth_error")
}

// 403 Forbidden → auth_error (P3 alias).
func TestDispatch_Forbidden_AuthError(t *testing.T) {
	forbidErr := fmt.Errorf("calling initialize: sending initialize: Forbidden")
	d := newTestDispatcher(errorOpener(forbidErr))

	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "hi"})

	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrDispatchFailed))
	assert.Contains(t, err.Error(), "auth_error")
}
