package phase_test

import (
	"context"
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia/pkg/contract"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	appphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/phase"
	domainchange "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// fakeApplyExecutor is a configurable test double for phase.ApplyExecutor.
// It returns the canned envelope/error so the runApplyPhase critic insertion
// point (the deferred follow-up to design D-GA-5) can be exercised end to end
// through Run with PhaseApply.
type fakeApplyExecutor struct {
	env *envelope.Envelope
	err error
}

func (e *fakeApplyExecutor) Execute(
	_ context.Context,
	_ *domainchange.Change,
	_ *phase.Phase,
	_ inbound.RunPhaseInput,
) (*envelope.Envelope, error) {
	return e.env, e.err
}

// applyCriticHarness wires a phase.Service whose ApplyExecutor returns a fixed
// envelope and whose Critic is injectable, so the apply-phase advisory critic
// path can be asserted. The change is hydrated at PhaseTasks so PhaseApply is
// the valid next transition.
type applyCriticHarness struct {
	svc       *appphase.Service
	phaseRepo *fakePhaseRepo
	events    *fakeEvents
	changeID  ids.ChangeID
}

func newApplyCriticHarness(t *testing.T, critic outbound.CriticPort, applyEnv *envelope.Envelope) *applyCriticHarness {
	t.Helper()
	cr := newFakeChangeRepo()
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	clock := shared.FixedClock(time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC))
	// PhaseTasks current → PhaseApply is the valid next transition.
	cr.byID[cid.String()] = domainchange.Hydrate(cid, "feat-x", "proj",
		domainchange.StatusActive, phase.PhaseTasks,
		domainchange.ArtifactStoreMemoryEngine, "main",
		clock.Now(), clock.Now())

	gov := &fakeGovernance{decision: &outbound.GovernanceDecision{
		Decision: outbound.DecisionAllow, AgentRole: "team-lead", Strategy: "parallel", Reason: "ok",
	}}
	idGen := shared.FixedIDGenerator([]string{
		"01ARZ3NDEKTSV4RRFFQ69G5P01",
		"01ARZ3NDEKTSV4RRFFQ69G5S01",
	})

	h := &applyCriticHarness{
		phaseRepo: newFakePhaseRepo(),
		events:    &fakeEvents{},
		changeID:  cid,
	}
	h.svc = appphase.New(appphase.Deps{
		ChangeRepo:    cr,
		PhaseRepo:     h.phaseRepo,
		SessionRepo:   newFakeSessionRepo(),
		Governance:    gov,
		Memory:        &fakeMemory{},
		Dispatcher:    &fakeDispatcher{result: &outbound.DispatchResult{ExitCode: 0}},
		SpawnGov:      &fakeSpawnGov{},
		Validator:     discipline.NewValidator(),
		IronLaw:       discipline.NewIronLawChecker(),
		Prompts:       discipline.NewPromptBuilder(),
		Audit:         &fakeAudit{},
		Events:        h.events,
		Clock:         clock,
		IDGen:         idGen,
		Scheduler:     appphase.SyncScheduler,
		ApplyExecutor: &fakeApplyExecutor{env: applyEnv},
		Critic:        critic,
	})
	return h
}

func (h *applyCriticHarness) run(t *testing.T, overrides map[string]any) {
	t.Helper()
	_, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:         h.changeID,
		PhaseType:        phase.PhaseApply,
		ContextOverrides: overrides,
	})
	require.NoError(t, err)
}

func (h *applyCriticHarness) savedPhase(t *testing.T) *phase.Phase {
	t.Helper()
	h.phaseRepo.mu.Lock()
	defer h.phaseRepo.mu.Unlock()
	require.Len(t, h.phaseRepo.byID, 1)
	for _, p := range h.phaseRepo.byID {
		return p
	}
	return nil
}

// mustApplyEnvelope builds a concrete *envelope.Envelope for the apply phase.
func mustApplyEnvelope(t *testing.T, status envelope.Status, conf float64) *envelope.Envelope {
	t.Helper()
	env, err := envelope.Parse(mustEnvelope(t, phase.PhaseApply, status, conf))
	require.NoError(t, err)
	return env
}

// --- opted-out apply phase is byte-identical (no concerns, plain payload) ---

func TestRunApply_Critic_DefaultOff_NeverCalled(t *testing.T) {
	fc := &fakeCritic{concerns: []phase.Concern{{Severity: "high", Category: "risk"}}}
	h := newApplyCriticHarness(t, fc, mustApplyEnvelope(t, envelope.StatusDone, 0.85))

	h.run(t, nil) // no overrides → critic_enabled defaults to false

	require.Equal(t, 0, fc.callCount(), "critic must NOT be called when opted out")
	p := h.savedPhase(t)
	require.Equal(t, phase.PhaseStatusDone, p.Status())
	require.Empty(t, p.Concerns())
	require.Contains(t, h.events.types(), contract.EventPhaseCompleted)
	require.NotContains(t, h.events.types(), contract.EventPhaseCompletedWithConcerns)

	// Opted-out payload byte-identical: plain slim apply payload, no concerns.
	var completed *inbound.Event
	for i := range h.events.published {
		if h.events.published[i].Type == contract.EventPhaseCompleted {
			completed = &h.events.published[i]
		}
	}
	require.NotNil(t, completed)
	payload, ok := completed.Payload.(inbound.PhaseCompletedFromApplyPayload)
	require.True(t, ok, "payload type %T", completed.Payload)
	require.Empty(t, payload.Concerns, "opted-out apply payload must carry no concerns")
	require.Equal(t, string(envelope.StatusDone), payload.EnvelopeStatus)
}

func TestRunApply_Critic_NilCritic_OptedInIsNoOp(t *testing.T) {
	h := newApplyCriticHarness(t, nil, mustApplyEnvelope(t, envelope.StatusDone, 0.85))
	h.run(t, criticEnabledOverrides())

	p := h.savedPhase(t)
	require.Equal(t, phase.PhaseStatusDone, p.Status())
	require.Empty(t, p.Concerns())
	require.Contains(t, h.events.types(), contract.EventPhaseCompleted)
}

// --- opted-in apply with concerns → DONE_WITH_CONCERNS + concerns in payload, still advances ---

func TestRunApply_Critic_OptedIn_ConcernsCoerceAndRideApplyPayload(t *testing.T) {
	fc := &fakeCritic{concerns: []phase.Concern{
		{Severity: "high", Category: "risk", Message: "danger", Evidence: "risks[0].level=high"},
	}}
	h := newApplyCriticHarness(t, fc, mustApplyEnvelope(t, envelope.StatusDone, 0.9))

	h.run(t, criticEnabledOverrides())

	require.Equal(t, 1, fc.callCount())
	p := h.savedPhase(t)
	require.Equal(t, phase.PhaseStatusDoneWithConcerns, p.Status())
	require.Len(t, p.Concerns(), 1)
	require.True(t, p.Status().AdvanceAllowed(), "done_with_concerns must still advance")

	// Concerns ride the apply payload only on completed_with_concerns.
	var completed *inbound.Event
	for i := range h.events.published {
		if h.events.published[i].Type == contract.EventPhaseCompletedWithConcerns {
			completed = &h.events.published[i]
		}
	}
	require.NotNil(t, completed, "expected a phase.completed_with_concerns event")
	payload, ok := completed.Payload.(inbound.PhaseCompletedFromApplyPayload)
	require.True(t, ok, "payload type %T", completed.Payload)
	require.Len(t, payload.Concerns, 1)
	require.Equal(t, "high", payload.Concerns[0].Severity)
	require.Equal(t, "risk", payload.Concerns[0].Category)
	require.Equal(t, string(envelope.StatusDoneWithConcerns), payload.EnvelopeStatus)
}

// --- apply phase that BLOCKED is never downgraded by concerns ---

func TestRunApply_Critic_BlockedNeverDowngraded(t *testing.T) {
	fc := &fakeCritic{concerns: []phase.Concern{{Severity: "high", Category: "risk"}}}
	h := newApplyCriticHarness(t, fc, mustApplyEnvelope(t, envelope.StatusBlocked, 0.2))

	h.run(t, criticEnabledOverrides())

	p := h.savedPhase(t)
	require.Equal(t, phase.PhaseStatusBlocked, p.Status(),
		"a BLOCKED apply phase must never be coerced to done_with_concerns")
	require.NotContains(t, h.events.types(), contract.EventPhaseCompletedWithConcerns)
}

// --- critic error on apply path is swallowed; phase completes DONE ---

func TestRunApply_Critic_ReviewError_Swallowed(t *testing.T) {
	fc := &fakeCritic{err: errCriticBoom}
	h := newApplyCriticHarness(t, fc, mustApplyEnvelope(t, envelope.StatusDone, 0.85))

	h.run(t, criticEnabledOverrides())

	require.Equal(t, 1, fc.callCount())
	p := h.savedPhase(t)
	require.Equal(t, phase.PhaseStatusDone, p.Status(), "critic error must never break the apply phase")
	require.Empty(t, p.Concerns())
	require.Contains(t, h.events.types(), contract.EventPhaseCompleted)
}

// --- opted-in with zero concerns stays plain DONE (no spurious upgrade) ---

func TestRunApply_Critic_OptedIn_ZeroConcerns_StaysDone(t *testing.T) {
	fc := &fakeCritic{concerns: nil}
	h := newApplyCriticHarness(t, fc, mustApplyEnvelope(t, envelope.StatusDone, 0.95))

	h.run(t, criticEnabledOverrides())

	require.Equal(t, 1, fc.callCount())
	p := h.savedPhase(t)
	require.Equal(t, phase.PhaseStatusDone, p.Status())
	require.Empty(t, p.Concerns())
	require.Contains(t, h.events.types(), contract.EventPhaseCompleted)
	require.NotContains(t, h.events.types(), contract.EventPhaseCompletedWithConcerns)
}
