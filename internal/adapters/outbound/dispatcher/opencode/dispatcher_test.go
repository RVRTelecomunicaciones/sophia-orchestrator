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
	require.Equal(t, "opencode", payload["command"])
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

	// Verify wire shape: worktree is passed via working_dir (NOT --dir),
	// prompt is the last positional arg, stdin is absent.
	// See dispatcher.go M-E0 8th wire-gap fix: opencode's permission
	// sandbox honors the launching shell's cwd, not --dir.
	var payload map[string]any
	require.NoError(t, json.Unmarshal(rt.captured.Payload, &payload))
	args := payload["args"].([]any)
	for _, a := range args {
		require.NotEqual(t, "--dir", a, "must NOT pass --dir; use working_dir on payload instead")
	}
	require.Equal(t, "/tmp/wt", payload["working_dir"], "worktree must be set as working_dir on the runtime payload")
	require.Equal(t, "do the thing", args[len(args)-1], "prompt must be the LAST positional arg")
	require.NotContains(t, payload, "stdin", "stdin field must NOT be sent — opencode reads from positional argv")

	// 10th wire-gap fix: env carries OPENCODE_CONFIG_CONTENT to allowlist
	// the worktree under opencode's permission system. Without this,
	// opencode auto-rejects every read/edit as external_directory.
	envMap, ok := payload["env"].(map[string]any)
	require.True(t, ok, "payload must include env map with OPENCODE_CONFIG_CONTENT")
	cfgRaw, ok := envMap["OPENCODE_CONFIG_CONTENT"].(string)
	require.True(t, ok, "env must include OPENCODE_CONFIG_CONTENT string")
	require.NotEmpty(t, cfgRaw)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal([]byte(cfgRaw), &cfg), "OPENCODE_CONFIG_CONTENT must be valid JSON")
	perm := cfg["permission"].(map[string]any)
	extDir := perm["external_directory"].(map[string]any)
	require.Equal(t, "allow", extDir["/tmp/wt"], "worktree must be allowed under external_directory")
	require.Equal(t, "allow", extDir["/tmp/wt/**"], "worktree recursive glob must be allowed under external_directory")
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
		require.NotEqual(t, "--dir", a, "WorktreePath \".\" should not inject --cwd")
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

// TestDispatcher_PerPhaseModelOverride exercises the SOPHIA_DISPATCHER_MODEL_<PHASE>
// override path: when ModelByPhase has an entry for the requested PhaseType,
// the dispatcher must pass `-m <override>` instead of `-m <Config.Model>`.
//
// Pre-existing callers that omit DispatchRequest.PhaseType (or leave it
// empty) must keep getting the global default — this is the backward-
// compat guarantee that makes the change a non-breaking add.
func TestDispatcher_PerPhaseModelOverride(t *testing.T) {
	type capture struct {
		args []string
	}
	tests := []struct {
		name        string
		cfg         opencode.Config
		req         outbound.DispatchRequest
		wantModel   string
		wantHasFlag bool
	}{
		{
			name: "phase override wins over global model",
			cfg: opencode.Config{
				Cmd:   "opencode",
				Model: "github-copilot/claude-sonnet-4.6",
				ModelByPhase: map[string]string{
					"apply": "openai/gpt-5.3-codex",
				},
			},
			req:         outbound.DispatchRequest{Prompt: "p", PhaseType: "apply"},
			wantModel:   "openai/gpt-5.3-codex",
			wantHasFlag: true,
		},
		{
			name: "phase without override falls back to global model",
			cfg: opencode.Config{
				Cmd:   "opencode",
				Model: "github-copilot/claude-sonnet-4.6",
				ModelByPhase: map[string]string{
					"apply": "openai/gpt-5.3-codex",
				},
			},
			req:         outbound.DispatchRequest{Prompt: "p", PhaseType: "spec"},
			wantModel:   "github-copilot/claude-sonnet-4.6",
			wantHasFlag: true,
		},
		{
			name: "empty PhaseType uses global model (backward compat)",
			cfg: opencode.Config{
				Cmd:   "opencode",
				Model: "github-copilot/claude-sonnet-4.6",
			},
			req:         outbound.DispatchRequest{Prompt: "p"},
			wantModel:   "github-copilot/claude-sonnet-4.6",
			wantHasFlag: true,
		},
		{
			name: "empty global model and no override emits no -m flag",
			cfg: opencode.Config{
				Cmd: "opencode",
			},
			req:         outbound.DispatchRequest{Prompt: "p", PhaseType: "explore"},
			wantModel:   "",
			wantHasFlag: false,
		},
		{
			name: "empty override entry falls back to global model",
			cfg: opencode.Config{
				Cmd:          "opencode",
				Model:        "github-copilot/claude-sonnet-4.6",
				ModelByPhase: map[string]string{"apply": ""},
			},
			req:         outbound.DispatchRequest{Prompt: "p", PhaseType: "apply"},
			wantModel:   "github-copilot/claude-sonnet-4.6",
			wantHasFlag: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{
				Status: outbound.ReceiptSuccess,
				Stdout: []byte("```json\n{\"schema_version\":\"v1\"}\n```"),
			}}
			d := opencode.New(rt, tt.cfg)

			_, err := d.Dispatch(context.Background(), tt.req)
			require.NoError(t, err)

			// Decode the captured payload to find -m flag in args.
			var payload map[string]any
			require.NoError(t, json.Unmarshal(rt.captured.Payload, &payload))
			argsAny, ok := payload["args"].([]any)
			require.True(t, ok, "args must be a JSON array")

			cap := capture{args: make([]string, 0, len(argsAny))}
			for _, a := range argsAny {
				cap.args = append(cap.args, a.(string))
			}

			// Find -m flag if present.
			var observedModel string
			var hasFlag bool
			for i, a := range cap.args {
				if a == "-m" && i+1 < len(cap.args) {
					observedModel = cap.args[i+1]
					hasFlag = true
					break
				}
			}
			require.Equal(t, tt.wantHasFlag, hasFlag,
				"args=%v wantHasFlag=%v hasFlag=%v", cap.args, tt.wantHasFlag, hasFlag)
			require.Equal(t, tt.wantModel, observedModel,
				"args=%v wantModel=%q", cap.args, tt.wantModel)
		})
	}
}
