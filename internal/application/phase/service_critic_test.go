package phase_test

import (
	"context"
	"sync"
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

// fakeCritic is a configurable test double for outbound.CriticPort. It records
// whether Review was called and returns the canned concerns/error.
type fakeCritic struct {
	mu       sync.Mutex
	calls    int
	concerns []phase.Concern
	err      error
}

func (c *fakeCritic) Review(_ context.Context, _ outbound.CriticInput) ([]phase.Concern, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return c.concerns, c.err
}

func (c *fakeCritic) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// criticHarness wires a phase.Service with an injectable Critic and a
// configurable dispatch envelope so the runAsync critic insertion point
// (design D-GA-5) can be exercised end to end through Run.
type criticHarness struct {
	svc       *appphase.Service
	phaseRepo *fakePhaseRepo
	events    *fakeEvents
	changeID  ids.ChangeID
}

// newCriticHarness builds a service whose dispatcher returns the given
// envelope. critic may be nil (to assert the nil-tolerant default-off path).
func newCriticHarness(t *testing.T, critic outbound.CriticPort, envRaw []byte) *criticHarness {
	t.Helper()
	cr := newFakeChangeRepo()
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	clock := shared.FixedClock(time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC))
	advanced := domainchange.Hydrate(cid, "feat-x", "proj",
		domainchange.StatusActive, phase.PhaseProposal,
		domainchange.ArtifactStoreMemoryEngine, "main",
		clock.Now(), clock.Now())
	cr.byID[cid.String()] = advanced

	gov := &fakeGovernance{decision: &outbound.GovernanceDecision{
		Decision: outbound.DecisionAllow, AgentRole: "sdd-spec", Strategy: "direct", Reason: "ok",
	}}
	disp := &fakeDispatcher{result: &outbound.DispatchResult{ExitCode: 0, EnvelopeRaw: envRaw}}
	idGen := shared.FixedIDGenerator([]string{
		"01ARZ3NDEKTSV4RRFFQ69G5P01",
		"01ARZ3NDEKTSV4RRFFQ69G5S01",
	})

	h := &criticHarness{
		phaseRepo: newFakePhaseRepo(),
		events:    &fakeEvents{},
		changeID:  cid,
	}
	h.svc = appphase.New(appphase.Deps{
		ChangeRepo:  cr,
		PhaseRepo:   h.phaseRepo,
		SessionRepo: newFakeSessionRepo(),
		Governance:  gov,
		Memory:      &fakeMemory{},
		Dispatcher:  disp,
		SpawnGov:    &fakeSpawnGov{},
		Validator:   discipline.NewValidator(),
		IronLaw:     discipline.NewIronLawChecker(),
		Prompts:     discipline.NewPromptBuilder(),
		Audit:       &fakeAudit{},
		Events:      h.events,
		Clock:       clock,
		IDGen:       idGen,
		Scheduler:   appphase.SyncScheduler,
		Critic:      critic,
	})
	return h
}

func criticEnabledOverrides() map[string]any {
	return map[string]any{"scope": map[string]any{"critic_enabled": true}}
}

func (h *criticHarness) run(t *testing.T, overrides map[string]any) {
	t.Helper()
	_, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:         h.changeID,
		PhaseType:        phase.PhaseSpec,
		ContextOverrides: overrides,
	})
	require.NoError(t, err)
}

// savedPhase returns the single persisted phase.
func (h *criticHarness) savedPhase(t *testing.T) *phase.Phase {
	t.Helper()
	h.phaseRepo.mu.Lock()
	defer h.phaseRepo.mu.Unlock()
	require.Len(t, h.phaseRepo.byID, 1)
	for _, p := range h.phaseRepo.byID {
		return p
	}
	return nil
}

func (h *criticHarness) completedEventTypes() []string {
	return h.events.types()
}

// --- F.1: opt-out / nil default-off path is byte-identical ---

func TestRun_Critic_DefaultOff_NeverCalled(t *testing.T) {
	fc := &fakeCritic{concerns: []phase.Concern{{Severity: "high", Category: "risk"}}}
	h := newCriticHarness(t, fc, mustEnvelope(t, phase.PhaseSpec, envelope.StatusDone, 0.85))

	// No ContextOverrides → critic_enabled defaults to false.
	h.run(t, nil)

	require.Equal(t, 0, fc.callCount(), "critic must NOT be called when opted out")
	p := h.savedPhase(t)
	require.Equal(t, phase.PhaseStatusDone, p.Status())
	require.Empty(t, p.Concerns())
	require.Contains(t, h.completedEventTypes(), contract.EventPhaseCompleted)
	require.NotContains(t, h.completedEventTypes(), contract.EventPhaseCompletedWithConcerns)
}

func TestRun_Critic_NilCritic_OptedInIsNoOp(t *testing.T) {
	// Opted in but no critic wired → nil-tolerant, byte-identical to today.
	h := newCriticHarness(t, nil, mustEnvelope(t, phase.PhaseSpec, envelope.StatusDone, 0.85))
	h.run(t, criticEnabledOverrides())

	p := h.savedPhase(t)
	require.Equal(t, phase.PhaseStatusDone, p.Status())
	require.Empty(t, p.Concerns())
	require.Contains(t, h.completedEventTypes(), contract.EventPhaseCompleted)
}

// --- F.1: opted-in concerns coerce DONE -> DONE_WITH_CONCERNS, still advances ---

func TestRun_Critic_OptedIn_ConcernsCoerceToDoneWithConcerns(t *testing.T) {
	fc := &fakeCritic{concerns: []phase.Concern{
		{Severity: "medium", Category: "confidence", Message: "low", Evidence: "confidence=0.4"},
	}}
	h := newCriticHarness(t, fc, mustEnvelope(t, phase.PhaseSpec, envelope.StatusDone, 0.85))

	h.run(t, criticEnabledOverrides())

	require.Equal(t, 1, fc.callCount())
	p := h.savedPhase(t)
	require.Equal(t, phase.PhaseStatusDoneWithConcerns, p.Status())
	require.Len(t, p.Concerns(), 1)
	// Non-blocking: done_with_concerns still allows advancement.
	require.True(t, p.Status().AdvanceAllowed())
}

// --- F.1: highest severity is still non-blocking, never escalates ---

func TestRun_Critic_HighSeverity_StillNonBlocking(t *testing.T) {
	fc := &fakeCritic{concerns: []phase.Concern{
		{Severity: "high", Category: "risk", Message: "danger", Evidence: "risks[0].level=high"},
	}}
	h := newCriticHarness(t, fc, mustEnvelope(t, phase.PhaseSpec, envelope.StatusDone, 0.9))

	h.run(t, criticEnabledOverrides())

	p := h.savedPhase(t)
	require.Equal(t, phase.PhaseStatusDoneWithConcerns, p.Status())
	require.True(t, p.Status().AdvanceAllowed(), "even high severity must not block")
}

// --- F.1: zero concerns when opted-in => plain DONE, no spurious upgrade ---

func TestRun_Critic_OptedIn_ZeroConcerns_StaysDone(t *testing.T) {
	fc := &fakeCritic{concerns: nil}
	h := newCriticHarness(t, fc, mustEnvelope(t, phase.PhaseSpec, envelope.StatusDone, 0.95))

	h.run(t, criticEnabledOverrides())

	require.Equal(t, 1, fc.callCount())
	p := h.savedPhase(t)
	require.Equal(t, phase.PhaseStatusDone, p.Status())
	require.Empty(t, p.Concerns())
	require.Contains(t, h.completedEventTypes(), contract.EventPhaseCompleted)
	require.NotContains(t, h.completedEventTypes(), contract.EventPhaseCompletedWithConcerns)
}

// --- F.1: Review error is swallowed, phase completes DONE ---

func TestRun_Critic_ReviewError_SwallowedPhaseCompletes(t *testing.T) {
	fc := &fakeCritic{err: errCriticBoom}
	h := newCriticHarness(t, fc, mustEnvelope(t, phase.PhaseSpec, envelope.StatusDone, 0.85))

	h.run(t, criticEnabledOverrides())

	require.Equal(t, 1, fc.callCount())
	p := h.savedPhase(t)
	require.Equal(t, phase.PhaseStatusDone, p.Status(), "critic error must never break the phase")
	require.Empty(t, p.Concerns())
}

// --- F.1: BLOCKED is never downgraded by concerns ---

func TestRun_Critic_BlockedNeverDowngraded(t *testing.T) {
	fc := &fakeCritic{concerns: []phase.Concern{{Severity: "high", Category: "risk"}}}
	// BLOCKED envelope: confidence threshold not relevant for blocked.
	h := newCriticHarness(t, fc, mustEnvelope(t, phase.PhaseSpec, envelope.StatusBlocked, 0.2))

	h.run(t, criticEnabledOverrides())

	p := h.savedPhase(t)
	require.Equal(t, phase.PhaseStatusBlocked, p.Status(),
		"a BLOCKED phase must never be coerced to done_with_concerns")
}

// --- G: SSE concerns ride only on completed_with_concerns ---

func TestRun_Critic_SSEConcernsOnCompletedWithConcerns(t *testing.T) {
	fc := &fakeCritic{concerns: []phase.Concern{
		{Severity: "high", Category: "risk", Message: "danger", Evidence: "risks[0].level=high"},
	}}
	h := newCriticHarness(t, fc, mustEnvelope(t, phase.PhaseSpec, envelope.StatusDone, 0.9))

	h.run(t, criticEnabledOverrides())

	var completed *inbound.Event
	for i := range h.events.published {
		if h.events.published[i].Type == contract.EventPhaseCompletedWithConcerns {
			completed = &h.events.published[i]
		}
	}
	require.NotNil(t, completed, "expected a phase.completed_with_concerns event")
	payload, ok := completed.Payload.(inbound.PhaseCompletedPayload)
	require.True(t, ok, "payload type %T", completed.Payload)
	require.Len(t, payload.Concerns, 1)
	require.Equal(t, "high", payload.Concerns[0].Severity)
	require.Equal(t, "risk", payload.Concerns[0].Category)
}

func TestRun_Critic_PlainCompleted_NoConcernsPayload(t *testing.T) {
	h := newCriticHarness(t, nil, mustEnvelope(t, phase.PhaseSpec, envelope.StatusDone, 0.85))
	h.run(t, nil)

	var completed *inbound.Event
	for i := range h.events.published {
		if h.events.published[i].Type == contract.EventPhaseCompleted {
			completed = &h.events.published[i]
		}
	}
	require.NotNil(t, completed)
	payload, ok := completed.Payload.(inbound.PhaseCompletedPayload)
	require.True(t, ok)
	require.Empty(t, payload.Concerns, "plain phase.completed must carry no concerns")
}

var errCriticBoom = &criticBoomError{}

type criticBoomError struct{}

func (*criticBoomError) Error() string { return "critic boom" }
