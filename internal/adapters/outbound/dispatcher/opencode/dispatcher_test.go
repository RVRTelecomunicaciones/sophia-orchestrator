package opencode_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/dispatcher/opencode"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// fakeRuntime captures the dispatched ExecutionRequest and returns a canned
// ExecutionReceipt.
type fakeRuntime struct {
	captured   outbound.ExecutionRequest
	returnErr  error
	returnRecp *outbound.ExecutionReceipt
}

func (f *fakeRuntime) Execute(_ context.Context, req outbound.ExecutionRequest) (*outbound.ExecutionReceipt, error) {
	f.captured = req
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	return f.returnRecp, nil
}

func TestNew_PanicsOnNilRuntime(t *testing.T) {
	require.Panics(t, func() {
		_ = opencode.New(nil, opencode.DefaultConfig())
	})
}

func TestProvider_AndSuggestedMaxConcurrent(t *testing.T) {
	d := opencode.New(&fakeRuntime{}, opencode.DefaultConfig())
	require.Equal(t, session.ProviderOpenCode, d.Provider())
	require.Equal(t, opencode.SuggestedMaxConcurrentDefault, d.SuggestedMaxConcurrent())
}

func TestNew_DefaultsCmd(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{Status: outbound.ReceiptSuccess}}
	d := opencode.New(rt, opencode.Config{}) // empty cfg
	_ = d.HealthCheck(context.Background())
	var payload map[string]any
	require.NoError(t, json.Unmarshal(rt.captured.Payload, &payload))
	require.Equal(t, "opencode", payload["cmd"])
}

func TestHealthCheck_Success(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{Status: outbound.ReceiptSuccess, ExitCode: 0}}
	d := opencode.New(rt, opencode.DefaultConfig())
	require.NoError(t, d.HealthCheck(context.Background()))
}

func TestHealthCheck_RuntimeError(t *testing.T) {
	rt := &fakeRuntime{returnErr: errors.New("runtime down")}
	d := opencode.New(rt, opencode.DefaultConfig())
	require.Error(t, d.HealthCheck(context.Background()))
}

func TestHealthCheck_NonSuccessReceipt(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{Status: outbound.ReceiptFailure, ExitCode: 127}}
	d := opencode.New(rt, opencode.DefaultConfig())
	require.Error(t, d.HealthCheck(context.Background()))
}

func TestDispatch_HappyPath(t *testing.T) {
	stdout := []byte("Some preamble text\n```json\n{\"schema_version\":\"v1\",\"phase\":\"spec\",\"status\":\"DONE\"}\n```\nMore text")
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{
		Status:     outbound.ReceiptSuccess,
		Stdout:     stdout,
		ExitCode:   0,
		DurationMS: 1234,
	}}
	d := opencode.New(rt, opencode.DefaultConfig())
	res, err := d.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt:       "do the thing",
		WorktreePath: "/tmp/wt",
		TimeoutMS:    60_000,
	})
	require.NoError(t, err)
	require.Equal(t, 0, res.ExitCode)
	require.Equal(t, 1234, res.DurationMS)
	require.NotEmpty(t, res.EnvelopeRaw)

	var env map[string]any
	require.NoError(t, json.Unmarshal(res.EnvelopeRaw, &env))
	require.Equal(t, "v1", env["schema_version"])
	require.Equal(t, "DONE", env["status"])

	// Verify args injected --cwd
	var payload map[string]any
	require.NoError(t, json.Unmarshal(rt.captured.Payload, &payload))
	args := payload["args"].([]any)
	foundCwd := false
	for _, a := range args {
		if a == "--cwd" {
			foundCwd = true
			break
		}
	}
	require.True(t, foundCwd)
	require.Equal(t, "do the thing", payload["stdin"])
}

func TestDispatch_NoEnvelopeReturnsNil(t *testing.T) {
	stdout := []byte("just plain text, no fenced json")
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{Status: outbound.ReceiptSuccess, Stdout: stdout}}
	d := opencode.New(rt, opencode.DefaultConfig())
	res, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "x"})
	require.NoError(t, err)
	require.Empty(t, res.EnvelopeRaw)
}

func TestDispatch_LastFencedJSONWins(t *testing.T) {
	stdout := []byte("```json\n{\"first\":true}\n```\nmiddle\n```json\n{\"last\":true}\n```")
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{Status: outbound.ReceiptSuccess, Stdout: stdout}}
	d := opencode.New(rt, opencode.DefaultConfig())
	res, _ := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "x"})
	require.Contains(t, string(res.EnvelopeRaw), `"last"`)
	require.NotContains(t, string(res.EnvelopeRaw), `"first"`)
}

func TestDispatch_RuntimeError(t *testing.T) {
	rt := &fakeRuntime{returnErr: errors.New("runtime down")}
	d := opencode.New(rt, opencode.DefaultConfig())
	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "x"})
	require.Error(t, err)
}

func TestDispatch_WorktreePathDotOmitsCwdArg(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{Status: outbound.ReceiptSuccess}}
	d := opencode.New(rt, opencode.DefaultConfig())
	_, _ = d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "x", WorktreePath: "."})
	var payload map[string]any
	require.NoError(t, json.Unmarshal(rt.captured.Payload, &payload))
	args := payload["args"].([]any)
	for _, a := range args {
		require.NotEqual(t, "--cwd", a, "WorktreePath \".\" should not inject --cwd")
	}
}

func TestDispatch_ExtraArgsAppended(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{Status: outbound.ReceiptSuccess}}
	d := opencode.New(rt, opencode.Config{ExtraArgs: []string{"--no-color", "--verbose"}})
	_, _ = d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "x"})
	var payload map[string]any
	require.NoError(t, json.Unmarshal(rt.captured.Payload, &payload))
	args := payload["args"].([]any)
	hasNoColor := false
	for _, a := range args {
		if a == "--no-color" {
			hasNoColor = true
		}
	}
	require.True(t, hasNoColor)
}

// --- M-E0 #3: receipt.Status guard tests ---

// TestDispatch_ReceiptFailure_ReturnsErrDispatchFailed verifies that when the
// runtime reports status="failure" (e.g. opencode binary not found), Dispatch
// returns outbound.ErrDispatchFailed and does NOT attempt envelope extraction.
func TestDispatch_ReceiptFailure_ReturnsErrDispatchFailed(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{
		Status:   outbound.ReceiptFailure,
		Stderr:   []byte("exec: opencode: no such file or directory"),
		ExitCode: 127,
	}}
	d := opencode.New(rt, opencode.DefaultConfig())

	res, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "x"})

	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrDispatchFailed),
		"expected ErrDispatchFailed, got: %v", err)
	require.Nil(t, res, "result must be nil when dispatch fails")
}

// TestDispatch_ReceiptTimeout_ReturnsErrDispatchFailed verifies that a
// status="timeout" receipt (agent CLI hung) also produces ErrDispatchFailed.
func TestDispatch_ReceiptTimeout_ReturnsErrDispatchFailed(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{
		Status:   outbound.ReceiptTimeout,
		Stderr:   []byte("process timed out after 30s"),
		ExitCode: -1,
	}}
	d := opencode.New(rt, opencode.DefaultConfig())

	res, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "x"})

	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrDispatchFailed),
		"expected ErrDispatchFailed, got: %v", err)
	require.Nil(t, res, "result must be nil on timeout")
}

// TestDispatch_ReceiptSuccess_StillReturnsResult confirms that the happy path
// is not regressed: status="success" with valid fenced JSON yields a populated
// DispatchResult with EnvelopeRaw set.
func TestDispatch_ReceiptSuccess_StillReturnsResult(t *testing.T) {
	stdout := []byte("thinking...\n```json\n{\"schema_version\":\"v1\",\"status\":\"DONE\"}\n```\n")
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{
		Status:     outbound.ReceiptSuccess,
		Stdout:     stdout,
		ExitCode:   0,
		DurationMS: 500,
	}}
	d := opencode.New(rt, opencode.DefaultConfig())

	res, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "x"})

	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotEmpty(t, res.EnvelopeRaw, "EnvelopeRaw must be populated on success")
	var env map[string]any
	require.NoError(t, json.Unmarshal(res.EnvelopeRaw, &env))
	require.Equal(t, "v1", env["schema_version"])
	require.Equal(t, 500, res.DurationMS)
}
