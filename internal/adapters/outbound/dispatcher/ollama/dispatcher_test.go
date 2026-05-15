package ollama_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/dispatcher/ollama"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// fakeRuntime captures the dispatched ExecutionRequest and returns a canned
// ExecutionReceipt — same pattern as opencode's dispatcher_test.go.
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

// helper — decode captured payload into a map for shape assertions.
func decodePayload(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var p map[string]any
	require.NoError(t, json.Unmarshal(raw, &p))
	return p
}

// helper — extract args as []string from a captured payload.
func argsOf(t *testing.T, payload map[string]any) []string {
	t.Helper()
	argsAny, ok := payload["args"].([]any)
	require.True(t, ok, "args must be a JSON array")
	out := make([]string, 0, len(argsAny))
	for _, a := range argsAny {
		out = append(out, a.(string))
	}
	return out
}

func TestNew_PanicsOnNilRuntime(t *testing.T) {
	require.Panics(t, func() {
		_ = ollama.New(nil, ollama.DefaultConfig())
	})
}

func TestProvider_ReusesOpenCodeEnum(t *testing.T) {
	// V2.0 reuses session.ProviderOpenCode for all dispatcher adapters
	// because session.Provider is a closed enum that wasn't extended for
	// V2 (see ADR-0007 §Consequences). Per-call adapter provenance lands
	// in V2.1; for now audit logs use the dispatcher hint + receipt.
	d := ollama.New(&fakeRuntime{}, ollama.DefaultConfig())
	require.Equal(t, session.ProviderOpenCode, d.Provider())
}

func TestSuggestedMaxConcurrent_DefaultIsTwo(t *testing.T) {
	// Single-GPU hosts queue concurrent ollama runs against the same
	// model file — 2 is the conservative default that lets a CPU-bound
	// phase pipeline with a GPU-bound one without thrashing.
	d := ollama.New(&fakeRuntime{}, ollama.DefaultConfig())
	require.Equal(t, ollama.SuggestedMaxConcurrentDefault, d.SuggestedMaxConcurrent())
	require.Equal(t, 2, d.SuggestedMaxConcurrent())
}

func TestSuggestedMaxConcurrent_HonorsConfigOverride(t *testing.T) {
	d := ollama.New(&fakeRuntime{}, ollama.Config{Suggested: 5})
	require.Equal(t, 5, d.SuggestedMaxConcurrent())
}

func TestNew_DefaultsCmdToOllama(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{Status: outbound.ReceiptSuccess}}
	d := ollama.New(rt, ollama.Config{}) // empty cfg — must default Cmd
	_ = d.HealthCheck(context.Background())
	payload := decodePayload(t, rt.captured.Payload)
	require.Equal(t, "ollama", payload["command"])
}

func TestHealthCheck_Success(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{Status: outbound.ReceiptSuccess, ExitCode: 0}}
	d := ollama.New(rt, ollama.DefaultConfig())
	require.NoError(t, d.HealthCheck(context.Background()))

	payload := decodePayload(t, rt.captured.Payload)
	args := argsOf(t, payload)
	require.Equal(t, []string{"--version"}, args, "HealthCheck must shell `ollama --version`")
}

func TestHealthCheck_RuntimeError(t *testing.T) {
	rt := &fakeRuntime{returnErr: errors.New("runtime down")}
	d := ollama.New(rt, ollama.DefaultConfig())
	require.Error(t, d.HealthCheck(context.Background()))
}

func TestHealthCheck_NonSuccessReceipt(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{Status: outbound.ReceiptFailure, ExitCode: 127}}
	d := ollama.New(rt, ollama.DefaultConfig())
	require.Error(t, d.HealthCheck(context.Background()))
}

func TestDispatch_FailsFastWhenNoModelConfigured(t *testing.T) {
	// Ollama has no implicit default model — unlike opencode (which lets
	// the upstream CLI pick), ollama errors at the adapter boundary so the
	// operator gets a clear message instead of a downstream "model not
	// found" from the ollama daemon.
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{Status: outbound.ReceiptSuccess}}
	d := ollama.New(rt, ollama.DefaultConfig()) // no Model, no ModelByPhase

	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt:    "anything",
		PhaseType: "spec",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "model is required")
}

func TestDispatch_HappyPath_PositionalModelAndPrompt(t *testing.T) {
	stdout := []byte("Some preamble\n```json\n{\"schema_version\":\"v1\",\"phase\":\"spec\",\"status\":\"DONE\"}\n```\nMore text")
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{
		Status:     outbound.ReceiptSuccess,
		Stdout:     stdout,
		ExitCode:   0,
		DurationMS: 4321,
	}}
	d := ollama.New(rt, ollama.Config{Model: "deepseek-r1:7b"})

	res, err := d.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt:       "do the thing",
		WorktreePath: "/tmp/wt",
		TimeoutMS:    60_000,
	})
	require.NoError(t, err)
	require.Equal(t, 0, res.ExitCode)
	require.Equal(t, 4321, res.DurationMS)
	require.NotEmpty(t, res.EnvelopeRaw)

	var env map[string]any
	require.NoError(t, json.Unmarshal(res.EnvelopeRaw, &env))
	require.Equal(t, "v1", env["schema_version"])
	require.Equal(t, "DONE", env["status"])

	// Wire shape — ollama-specific:
	//   args = ["run", "<model>", "<prompt>"]
	// Model is POSITIONAL (no `-m` flag — that's opencode); prompt is
	// the LAST positional arg.
	payload := decodePayload(t, rt.captured.Payload)
	args := argsOf(t, payload)
	require.Equal(t, "run", args[0], "first arg must be the `run` subcommand")
	require.Equal(t, "deepseek-r1:7b", args[1], "second arg must be the positional model name")
	require.Equal(t, "do the thing", args[len(args)-1], "prompt must be the LAST positional arg")
	for _, a := range args {
		require.NotEqual(t, "-m", a, "ollama must NOT use the -m flag — model is positional")
		require.NotEqual(t, "--dir", a, "ollama does not support --dir; working_dir lives on the runtime payload")
	}

	// working_dir is set so any relative path the model emits is
	// interpreted against the worktree (consistent with opencode).
	require.Equal(t, "/tmp/wt", payload["working_dir"])

	// No OPENCODE_CONFIG_CONTENT — that's an opencode-only environment
	// injection for its permission sandbox. Ollama has no such concept.
	_, hasEnv := payload["env"]
	require.False(t, hasEnv, "ollama must NOT inject env (no OPENCODE_CONFIG_CONTENT etc.)")
}

func TestDispatch_WorktreePathEmpty_OmitsWorkingDir(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{Status: outbound.ReceiptSuccess}}
	d := ollama.New(rt, ollama.Config{Model: "qwen3:14b"})
	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "x"})
	require.NoError(t, err)
	payload := decodePayload(t, rt.captured.Payload)
	_, has := payload["working_dir"]
	require.False(t, has, "working_dir must be omitted when WorktreePath is empty")
}

func TestDispatch_WorktreePathDot_OmitsWorkingDir(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{Status: outbound.ReceiptSuccess}}
	d := ollama.New(rt, ollama.Config{Model: "qwen3:14b"})
	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "x", WorktreePath: "."})
	require.NoError(t, err)
	payload := decodePayload(t, rt.captured.Payload)
	_, has := payload["working_dir"]
	require.False(t, has, `WorktreePath "." must NOT inject working_dir`)
}

func TestDispatch_NoEnvelopeReturnsNil(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{
		Status: outbound.ReceiptSuccess,
		Stdout: []byte("just plain text, no fenced json"),
	}}
	d := ollama.New(rt, ollama.Config{Model: "llama3.3:70b"})
	res, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "x"})
	require.NoError(t, err)
	require.Empty(t, res.EnvelopeRaw)
}

func TestDispatch_LastFencedJSONWins(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{
		Status: outbound.ReceiptSuccess,
		Stdout: []byte("```json\n{\"first\":true}\n```\nmiddle\n```json\n{\"last\":true}\n```"),
	}}
	d := ollama.New(rt, ollama.Config{Model: "llama3.3:70b"})
	res, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "x"})
	require.NoError(t, err)
	require.Contains(t, string(res.EnvelopeRaw), `"last"`)
	require.NotContains(t, string(res.EnvelopeRaw), `"first"`)
}

func TestDispatch_RuntimeError(t *testing.T) {
	rt := &fakeRuntime{returnErr: errors.New("runtime down")}
	d := ollama.New(rt, ollama.Config{Model: "qwen3:14b"})
	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "x"})
	require.Error(t, err)
}

func TestDispatch_ExtraArgsAppendedBetweenModelAndPrompt(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{Status: outbound.ReceiptSuccess}}
	d := ollama.New(rt, ollama.Config{
		Model:     "qwen3:14b",
		ExtraArgs: []string{"--format", "json"},
	})
	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "x"})
	require.NoError(t, err)

	payload := decodePayload(t, rt.captured.Payload)
	args := argsOf(t, payload)
	// Expected order: ["run", "<model>", <extras...>, "<prompt>"]
	require.Equal(t, []string{"run", "qwen3:14b", "--format", "json", "x"}, args)
}

// --- Receipt-status guard tests (M-E0 #3 semantics, mirrored from opencode) ---

func TestDispatch_ReceiptFailure_ReturnsErrDispatchFailed(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{
		Status:   outbound.ReceiptFailure,
		Stderr:   []byte("exec: ollama: no such file or directory"),
		ExitCode: 127,
	}}
	d := ollama.New(rt, ollama.Config{Model: "qwen3:14b"})

	res, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "x"})
	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrDispatchFailed),
		"expected ErrDispatchFailed, got: %v", err)
	require.Nil(t, res, "result must be nil when dispatch fails")
}

func TestDispatch_ReceiptTimeout_ReturnsErrDispatchFailed(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{
		Status:   outbound.ReceiptTimeout,
		Stderr:   []byte("process timed out after 30s"),
		ExitCode: -1,
	}}
	d := ollama.New(rt, ollama.Config{Model: "qwen3:14b"})

	res, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "x"})
	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrDispatchFailed))
	require.Nil(t, res)
}

// --- Per-phase model override (mirrors TestDispatcher_PerPhaseModelOverride) ---

func TestDispatcher_PerPhaseModelOverride(t *testing.T) {
	tests := []struct {
		name      string
		cfg       ollama.Config
		req       outbound.DispatchRequest
		wantModel string
		wantErr   bool
	}{
		{
			name: "phase override wins over global model",
			cfg: ollama.Config{
				Model: "deepseek-r1:7b",
				ModelByPhase: map[string]string{
					"verify": "qwen3:14b",
				},
			},
			req:       outbound.DispatchRequest{Prompt: "p", PhaseType: "verify"},
			wantModel: "qwen3:14b",
		},
		{
			name: "phase without override falls back to global model",
			cfg: ollama.Config{
				Model: "deepseek-r1:7b",
				ModelByPhase: map[string]string{
					"verify": "qwen3:14b",
				},
			},
			req:       outbound.DispatchRequest{Prompt: "p", PhaseType: "spec"},
			wantModel: "deepseek-r1:7b",
		},
		{
			name:      "empty PhaseType uses global model (backward compat)",
			cfg:       ollama.Config{Model: "deepseek-r1:7b"},
			req:       outbound.DispatchRequest{Prompt: "p"},
			wantModel: "deepseek-r1:7b",
		},
		{
			name:    "no model anywhere fails with helpful error",
			cfg:     ollama.Config{}, // no Model, no ModelByPhase
			req:     outbound.DispatchRequest{Prompt: "p", PhaseType: "explore"},
			wantErr: true,
		},
		{
			name: "empty override entry falls back to global model",
			cfg: ollama.Config{
				Model:        "deepseek-r1:7b",
				ModelByPhase: map[string]string{"apply": ""},
			},
			req:       outbound.DispatchRequest{Prompt: "p", PhaseType: "apply"},
			wantModel: "deepseek-r1:7b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{
				Status: outbound.ReceiptSuccess,
				Stdout: []byte("```json\n{\"schema_version\":\"v1\"}\n```"),
			}}
			d := ollama.New(rt, tt.cfg)

			_, err := d.Dispatch(context.Background(), tt.req)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			payload := decodePayload(t, rt.captured.Payload)
			args := argsOf(t, payload)
			// args[1] is always the positional model — no flag scan needed.
			require.GreaterOrEqual(t, len(args), 3, "expected at least [run, model, prompt]")
			require.Equal(t, tt.wantModel, args[1],
				"args=%v wantModel=%q", args, tt.wantModel)
		})
	}
}
