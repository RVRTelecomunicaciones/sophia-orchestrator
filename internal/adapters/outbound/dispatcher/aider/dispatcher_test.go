package aider_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/dispatcher/aider"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// fakeRuntime captures the dispatched ExecutionRequest and returns a canned
// ExecutionReceipt — same pattern as opencode + ollama dispatcher tests.
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

func decodePayload(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var p map[string]any
	require.NoError(t, json.Unmarshal(raw, &p))
	return p
}

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
		_ = aider.New(nil, aider.DefaultConfig())
	})
}

func TestProvider_ReusesOpenCodeEnum(t *testing.T) {
	// V1 session.Provider is a closed enum; V2 adapters reuse
	// ProviderOpenCode. Audit logs disambiguate via the receipt's
	// command line. ADR-0007 §Consequences.
	d := aider.New(&fakeRuntime{}, aider.DefaultConfig())
	require.Equal(t, session.ProviderOpenCode, d.Provider())
}

func TestSuggestedMaxConcurrent_DefaultIsOne(t *testing.T) {
	// Aider edits the worktree in-place; concurrent runs against the
	// same worktree race. Worktree isolation is the orchestrator's
	// responsibility — the default is conservative and the operator
	// can size up explicitly when worktrees are isolated per spawn.
	d := aider.New(&fakeRuntime{}, aider.DefaultConfig())
	require.Equal(t, aider.SuggestedMaxConcurrentDefault, d.SuggestedMaxConcurrent())
	require.Equal(t, 1, d.SuggestedMaxConcurrent())
}

func TestSuggestedMaxConcurrent_HonorsConfigOverride(t *testing.T) {
	d := aider.New(&fakeRuntime{}, aider.Config{Suggested: 3})
	require.Equal(t, 3, d.SuggestedMaxConcurrent())
}

func TestNew_DefaultsCmdToAider(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{Status: outbound.ReceiptSuccess}}
	d := aider.New(rt, aider.Config{}) // empty cfg — must default Cmd
	_ = d.HealthCheck(context.Background())
	payload := decodePayload(t, rt.captured.Payload)
	require.Equal(t, "aider", payload["command"])
}

func TestHealthCheck_Success(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{Status: outbound.ReceiptSuccess, ExitCode: 0}}
	d := aider.New(rt, aider.DefaultConfig())
	require.NoError(t, d.HealthCheck(context.Background()))

	payload := decodePayload(t, rt.captured.Payload)
	require.Equal(t, []string{"--version"}, argsOf(t, payload))
}

func TestHealthCheck_RuntimeError(t *testing.T) {
	rt := &fakeRuntime{returnErr: errors.New("runtime down")}
	d := aider.New(rt, aider.DefaultConfig())
	require.Error(t, d.HealthCheck(context.Background()))
}

func TestHealthCheck_NonSuccessReceipt(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{Status: outbound.ReceiptFailure, ExitCode: 127}}
	d := aider.New(rt, aider.DefaultConfig())
	require.Error(t, d.HealthCheck(context.Background()))
}

func TestDispatch_HappyPath_NoEnvelopeAlwaysNil(t *testing.T) {
	// Even when stdout contains what LOOKS like a fenced JSON block
	// (e.g. aider quoting a diff that happens to include JSON), the
	// adapter MUST NOT try to extract — aider's contract is "no
	// envelope, see git diff in the worktree".
	stdout := []byte("Edited foo.go. Here's a snippet:\n```json\n{\"this\":\"is not an envelope\"}\n```\nAll done.")
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{
		Status:     outbound.ReceiptSuccess,
		Stdout:     stdout,
		ExitCode:   0,
		DurationMS: 9876,
	}}
	d := aider.New(rt, aider.Config{Model: "claude-opus-4-7"})

	res, err := d.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt:       "refactor the auth middleware",
		WorktreePath: "/tmp/wt",
		TimeoutMS:    300_000,
	})
	require.NoError(t, err)
	require.Equal(t, 0, res.ExitCode)
	require.Equal(t, 9876, res.DurationMS)
	require.Equal(t, stdout, res.Stdout)
	require.Nil(t, res.EnvelopeRaw,
		"aider must NEVER populate EnvelopeRaw — caller reconstructs from worktree state")

	// Wire shape — aider-specific:
	//   args = ["--yes-always", "--no-auto-commits",
	//           "--model", "<model>",
	//           "--message", "<prompt>"]
	payload := decodePayload(t, rt.captured.Payload)
	args := argsOf(t, payload)
	require.Equal(t, []string{
		"--yes-always", "--no-auto-commits",
		"--model", "claude-opus-4-7",
		"--message", "refactor the auth middleware",
	}, args)

	require.Equal(t, "/tmp/wt", payload["working_dir"])

	// No OPENCODE_CONFIG_CONTENT — that's an opencode-only env injection.
	_, hasEnv := payload["env"]
	require.False(t, hasEnv, "aider must NOT inject env vars (credentials come from runtime image)")
}

func TestDispatch_OmitsModelFlagWhenUnconfigured(t *testing.T) {
	// When neither Config.Model nor ModelByPhase[phase] is set, aider
	// is allowed to pick its own default from `~/.aider.conf.yml` or
	// provider env vars. The adapter must omit `--model` entirely
	// rather than passing an empty string (which aider rejects).
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{Status: outbound.ReceiptSuccess}}
	d := aider.New(rt, aider.DefaultConfig()) // no Model, no ModelByPhase

	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt:    "anything",
		PhaseType: "apply",
	})
	require.NoError(t, err)

	payload := decodePayload(t, rt.captured.Payload)
	args := argsOf(t, payload)
	for i, a := range args {
		require.NotEqual(t, "--model", a, "args[%d]=%q — must not pass --model when unconfigured", i, a)
	}
	// `--yes-always` and `--no-auto-commits` MUST still be present.
	require.Contains(t, args, "--yes-always")
	require.Contains(t, args, "--no-auto-commits")
	// `--message <prompt>` MUST be the last pair.
	require.Equal(t, "--message", args[len(args)-2])
	require.Equal(t, "anything", args[len(args)-1])
}

func TestDispatch_AlwaysPassesYesAndNoAutoCommits(t *testing.T) {
	// `--yes-always` skips interactive confirmations (runtime can't
	// supply stdin). `--no-auto-commits` keeps git commits under the
	// orchestrator's control. Both are NON-NEGOTIABLE — removing
	// either would break the apply phase contract.
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{Status: outbound.ReceiptSuccess}}
	d := aider.New(rt, aider.Config{Model: "any-model"})
	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "x"})
	require.NoError(t, err)

	payload := decodePayload(t, rt.captured.Payload)
	args := argsOf(t, payload)
	require.Contains(t, args, "--yes-always", "must always pass --yes-always (no interactive stdin)")
	require.Contains(t, args, "--no-auto-commits", "must always pass --no-auto-commits (orch owns git)")
}

func TestDispatch_WorktreePathEmpty_OmitsWorkingDir(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{Status: outbound.ReceiptSuccess}}
	d := aider.New(rt, aider.Config{Model: "x"})
	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "x"})
	require.NoError(t, err)
	payload := decodePayload(t, rt.captured.Payload)
	_, has := payload["working_dir"]
	require.False(t, has, "working_dir must be omitted when WorktreePath is empty")
}

func TestDispatch_WorktreePathDot_OmitsWorkingDir(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{Status: outbound.ReceiptSuccess}}
	d := aider.New(rt, aider.Config{Model: "x"})
	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "x", WorktreePath: "."})
	require.NoError(t, err)
	payload := decodePayload(t, rt.captured.Payload)
	_, has := payload["working_dir"]
	require.False(t, has, `WorktreePath "." must NOT inject working_dir`)
}

func TestDispatch_RuntimeError(t *testing.T) {
	rt := &fakeRuntime{returnErr: errors.New("runtime down")}
	d := aider.New(rt, aider.Config{Model: "x"})
	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "x"})
	require.Error(t, err)
}

func TestDispatch_ExtraArgsBeforeMessage(t *testing.T) {
	// Extras land BEFORE --message so `--message <prompt>` stays the
	// LAST pair in argv (consistent with opencode + ollama).
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{Status: outbound.ReceiptSuccess}}
	d := aider.New(rt, aider.Config{
		Model:     "claude-opus-4-7",
		ExtraArgs: []string{"--map-tokens", "0"},
	})
	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "x"})
	require.NoError(t, err)

	payload := decodePayload(t, rt.captured.Payload)
	args := argsOf(t, payload)
	require.Equal(t, []string{
		"--yes-always", "--no-auto-commits",
		"--model", "claude-opus-4-7",
		"--map-tokens", "0",
		"--message", "x",
	}, args)
}

// --- Receipt-status guard tests (M-E0 #3 semantics, mirrored from opencode) ---

func TestDispatch_ReceiptFailure_ReturnsErrDispatchFailed(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{
		Status:   outbound.ReceiptFailure,
		Stderr:   []byte("exec: aider: no such file or directory"),
		ExitCode: 127,
	}}
	d := aider.New(rt, aider.Config{Model: "x"})

	res, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "x"})
	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrDispatchFailed),
		"expected ErrDispatchFailed, got: %v", err)
	require.Nil(t, res, "result must be nil when dispatch fails")
}

func TestDispatch_ReceiptTimeout_ReturnsErrDispatchFailed(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{
		Status:   outbound.ReceiptTimeout,
		Stderr:   []byte("process timed out after 300s"),
		ExitCode: -1,
	}}
	d := aider.New(rt, aider.Config{Model: "x"})

	res, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "x"})
	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrDispatchFailed))
	require.Nil(t, res)
}

// --- Per-phase model override (mirrors TestDispatcher_PerPhaseModelOverride) ---

func TestDispatcher_PerPhaseModelOverride(t *testing.T) {
	tests := []struct {
		name        string
		cfg         aider.Config
		req         outbound.DispatchRequest
		wantModel   string
		wantHasFlag bool
	}{
		{
			name: "phase override wins over global model",
			cfg: aider.Config{
				Model: "claude-sonnet-4.6",
				ModelByPhase: map[string]string{
					"apply": "claude-opus-4-7",
				},
			},
			req:         outbound.DispatchRequest{Prompt: "p", PhaseType: "apply"},
			wantModel:   "claude-opus-4-7",
			wantHasFlag: true,
		},
		{
			name: "phase without override falls back to global model",
			cfg: aider.Config{
				Model: "claude-sonnet-4.6",
				ModelByPhase: map[string]string{
					"apply": "claude-opus-4-7",
				},
			},
			req:         outbound.DispatchRequest{Prompt: "p", PhaseType: "spec"},
			wantModel:   "claude-sonnet-4.6",
			wantHasFlag: true,
		},
		{
			name:        "empty PhaseType uses global model",
			cfg:         aider.Config{Model: "claude-sonnet-4.6"},
			req:         outbound.DispatchRequest{Prompt: "p"},
			wantModel:   "claude-sonnet-4.6",
			wantHasFlag: true,
		},
		{
			name:        "empty global model and no override omits --model flag",
			cfg:         aider.Config{},
			req:         outbound.DispatchRequest{Prompt: "p", PhaseType: "apply"},
			wantHasFlag: false,
		},
		{
			name: "empty override entry falls back to global model",
			cfg: aider.Config{
				Model:        "claude-sonnet-4.6",
				ModelByPhase: map[string]string{"apply": ""},
			},
			req:         outbound.DispatchRequest{Prompt: "p", PhaseType: "apply"},
			wantModel:   "claude-sonnet-4.6",
			wantHasFlag: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{Status: outbound.ReceiptSuccess}}
			d := aider.New(rt, tt.cfg)

			_, err := d.Dispatch(context.Background(), tt.req)
			require.NoError(t, err)

			payload := decodePayload(t, rt.captured.Payload)
			args := argsOf(t, payload)

			var observedModel string
			var hasFlag bool
			for i, a := range args {
				if a == "--model" && i+1 < len(args) {
					observedModel = args[i+1]
					hasFlag = true
					break
				}
			}
			require.Equal(t, tt.wantHasFlag, hasFlag,
				"args=%v wantHasFlag=%v hasFlag=%v", args, tt.wantHasFlag, hasFlag)
			require.Equal(t, tt.wantModel, observedModel,
				"args=%v wantModel=%q", args, tt.wantModel)
		})
	}
}
