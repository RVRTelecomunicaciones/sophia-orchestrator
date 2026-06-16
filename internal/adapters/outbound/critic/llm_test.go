package critic_test

import (
	"context"
	"errors"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/critic"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// fakeDispatcher is a deterministic test double for the outbound dispatch path.
// It records the last request and returns a canned result/error so the LLM
// critic can be exercised without spawning a real OpenCode subprocess.
type fakeDispatcher struct {
	result  *outbound.DispatchResult
	err     error
	lastReq outbound.DispatchRequest
	calls   int
}

func (f *fakeDispatcher) Provider() session.Provider { return session.ProviderOpenCode }
func (f *fakeDispatcher) SuggestedMaxConcurrent() int { return 1 }
func (f *fakeDispatcher) HealthCheck(_ context.Context) error { return nil }
func (f *fakeDispatcher) Dispatch(_ context.Context, req outbound.DispatchRequest) (*outbound.DispatchResult, error) {
	f.calls++
	f.lastReq = req
	return f.result, f.err
}

// fakeGovernor records acquire/release ordering so we can assert the critic
// goes through the governor (D1.4) and always releases.
type fakeGovernor struct {
	acquired   int
	released   int
	acquireErr error
}

func (g *fakeGovernor) Acquire(_ context.Context) error {
	g.acquired++
	return g.acquireErr
}

func (g *fakeGovernor) Release(_ context.Context) error {
	g.released++
	return nil
}

func mkLLMInput(env *envelope.Envelope) outbound.CriticInput {
	return outbound.CriticInput{
		ChangeID:  ids.ChangeID{},
		PhaseType: phase.PhaseSpec,
		Envelope:  env,
	}
}

func cleanEnv() *envelope.Envelope {
	return &envelope.Envelope{Status: envelope.StatusDone, Confidence: 0.9}
}

// TestLLMCritic_Review_ParsesConcernsFromDispatcherResponse proves the critic
// parses a structured fenced-JSON concerns object from the dispatcher result
// into []phase.Concern.
func TestLLMCritic_Review_ParsesConcernsFromDispatcherResponse(t *testing.T) {
	disp := &fakeDispatcher{
		result: &outbound.DispatchResult{
			EnvelopeRaw: []byte(`{"concerns":[
				{"severity":"high","category":"correctness","message":"missing rollback","evidence":"data.plan has no rollback step"},
				{"severity":"medium","category":"completeness","message":"no test plan","evidence":"executive_summary omits tests"}
			]}`),
		},
	}
	gov := &fakeGovernor{}
	c := critic.NewLLM(disp, gov, critic.LLMConfig{})

	got, err := c.Review(context.Background(), mkLLMInput(cleanEnv()))
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, phase.Concern{
		Severity: "high",
		Category: "correctness",
		Message:  "missing rollback",
		Evidence: "data.plan has no rollback step",
	}, got[0])
	require.Equal(t, "completeness", got[1].Category)

	// Went through the governor and released exactly once (D1.4).
	require.Equal(t, 1, gov.acquired)
	require.Equal(t, 1, gov.released)
	require.Equal(t, 1, disp.calls)
	// The dispatch carried a non-empty review prompt.
	require.NotEmpty(t, disp.lastReq.Prompt)
}

// TestLLMCritic_Review_StdoutFallback proves the critic extracts the fenced
// JSON object from raw Stdout when EnvelopeRaw is empty (adapter that does not
// pre-extract).
func TestLLMCritic_Review_StdoutFallback(t *testing.T) {
	disp := &fakeDispatcher{
		result: &outbound.DispatchResult{
			Stdout: []byte("some preamble\n```json\n{\"concerns\":[{\"severity\":\"low\",\"category\":\"risk\",\"message\":\"m\",\"evidence\":\"e\"}]}\n```\ntrailer"),
		},
	}
	c := critic.NewLLM(disp, &fakeGovernor{}, critic.LLMConfig{})

	got, err := c.Review(context.Background(), mkLLMInput(cleanEnv()))
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "risk", got[0].Category)
}

// TestLLMCritic_Review_MalformedOutputDegradesGracefully proves malformed or
// empty dispatcher output yields zero concerns and NO error — an advisory
// critic must never break a phase (design D-GA-5).
func TestLLMCritic_Review_MalformedOutputDegradesGracefully(t *testing.T) {
	tests := []struct {
		name   string
		result *outbound.DispatchResult
	}{
		{name: "garbage bytes", result: &outbound.DispatchResult{EnvelopeRaw: []byte("not json at all")}},
		{name: "empty result", result: &outbound.DispatchResult{}},
		{name: "wrong shape", result: &outbound.DispatchResult{EnvelopeRaw: []byte(`{"foo":"bar"}`)}},
		{name: "concerns wrong type", result: &outbound.DispatchResult{EnvelopeRaw: []byte(`{"concerns":"nope"}`)}},
		{name: "nil result", result: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			disp := &fakeDispatcher{result: tt.result}
			c := critic.NewLLM(disp, &fakeGovernor{}, critic.LLMConfig{})
			got, err := c.Review(context.Background(), mkLLMInput(cleanEnv()))
			require.NoError(t, err)
			require.Empty(t, got)
		})
	}
}

// TestLLMCritic_Review_DispatcherErrorSwallowed proves a dispatch error yields
// no concerns and no propagated error; the governor slot is still released.
func TestLLMCritic_Review_DispatcherErrorSwallowed(t *testing.T) {
	disp := &fakeDispatcher{err: errors.New("boom")}
	gov := &fakeGovernor{}
	c := critic.NewLLM(disp, gov, critic.LLMConfig{})

	got, err := c.Review(context.Background(), mkLLMInput(cleanEnv()))
	require.NoError(t, err)
	require.Empty(t, got)
	require.Equal(t, 1, gov.acquired)
	require.Equal(t, 1, gov.released)
}

// TestLLMCritic_Review_GovernorErrorSwallowed proves a governor acquire failure
// short-circuits to no concerns without dispatching and without error.
func TestLLMCritic_Review_GovernorErrorSwallowed(t *testing.T) {
	disp := &fakeDispatcher{}
	gov := &fakeGovernor{acquireErr: errors.New("saturated")}
	c := critic.NewLLM(disp, gov, critic.LLMConfig{})

	got, err := c.Review(context.Background(), mkLLMInput(cleanEnv()))
	require.NoError(t, err)
	require.Empty(t, got)
	require.Equal(t, 0, disp.calls, "must not dispatch when governor refuses the slot")
	require.Equal(t, 0, gov.released, "nothing to release when acquire failed")
}

// TestLLMCritic_Review_NilEnvelope returns no concerns and never dispatches —
// there is nothing to review.
func TestLLMCritic_Review_NilEnvelope(t *testing.T) {
	disp := &fakeDispatcher{}
	c := critic.NewLLM(disp, &fakeGovernor{}, critic.LLMConfig{})
	got, err := c.Review(context.Background(), mkLLMInput(nil))
	require.NoError(t, err)
	require.Empty(t, got)
	require.Equal(t, 0, disp.calls)
}

// TestLLMCritic_Review_FiltersGovernanceCategories locks the strictly-advisory
// invariant: even if the LLM emits a governance/policy category, the critic
// drops it. The advisory critic can NEVER escalate to governance.
func TestLLMCritic_Review_FiltersGovernanceCategories(t *testing.T) {
	disp := &fakeDispatcher{
		result: &outbound.DispatchResult{
			EnvelopeRaw: []byte(`{"concerns":[
				{"severity":"high","category":"governance","message":"x","evidence":"y"},
				{"severity":"high","category":"policy","message":"x","evidence":"y"},
				{"severity":"high","category":"correctness","message":"keep","evidence":"y"}
			]}`),
		},
	}
	c := critic.NewLLM(disp, &fakeGovernor{}, critic.LLMConfig{})
	got, err := c.Review(context.Background(), mkLLMInput(cleanEnv()))
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "correctness", got[0].Category)
}

// TestLLMCritic_Review_Deterministic proves that for a fixed fake dispatcher
// response the critic returns byte-identical concerns across calls (no clock,
// no randomness in the adapter).
func TestLLMCritic_Review_Deterministic(t *testing.T) {
	mk := func() *fakeDispatcher {
		return &fakeDispatcher{result: &outbound.DispatchResult{
			EnvelopeRaw: []byte(`{"concerns":[{"severity":"medium","category":"risk","message":"m","evidence":"e"}]}`),
		}}
	}
	c1 := critic.NewLLM(mk(), &fakeGovernor{}, critic.LLMConfig{})
	c2 := critic.NewLLM(mk(), &fakeGovernor{}, critic.LLMConfig{})
	first, err := c1.Review(context.Background(), mkLLMInput(cleanEnv()))
	require.NoError(t, err)
	second, err := c2.Review(context.Background(), mkLLMInput(cleanEnv()))
	require.NoError(t, err)
	require.Equal(t, first, second)
}

// Compile-time assertion that LLMCritic satisfies the outbound port.
var _ outbound.CriticPort = critic.NewLLM(&fakeDispatcher{}, &fakeGovernor{}, critic.LLMConfig{})
