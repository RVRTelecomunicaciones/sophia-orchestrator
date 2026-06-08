package phase_test

// archive_event_test.go — B.1 through B.4 (Strict TDD: RED tests first)
//
// Tests for the phase.archived SSE event emitted by advanceChange when the
// archive phase completes. Tested indirectly through Service.Run because
// advanceChange is unexported.
//
// Design ref: D-PRE-2 (phase/service.go ~L911, design.md lines 30-72, 246-286)
// Spec ref: phase-archived-event/spec.md — Exactly-Once Emission at Archive Completion

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

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

// archiveHarness builds a Service wired for a Change that is ready to
// execute the PhaseArchive step (currentPhase = PhaseVerify).
//
// Iron Law 3 requires PhaseVerify DONE (confidence >= 0.9) before archive
// is allowed. We seed a completed PhaseVerify phase in the fake repo to
// satisfy the IL3 pre-flight check.
func archiveHarness(t *testing.T) *harness {
	t.Helper()

	cr := newFakeChangeRepo()
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C02")

	// Change is in StatusActive at PhaseVerify — PhaseArchive is the valid next.
	advanced := domainchange.Hydrate(
		cid, "archive-test", "proj",
		domainchange.StatusActive, phase.PhaseVerify,
		domainchange.ArtifactStoreMemoryEngine, "main",
		time.Now(), time.Now(),
	)
	cr.byID[cid.String()] = advanced

	gov := &fakeGovernance{decision: &outbound.GovernanceDecision{
		Decision:  outbound.DecisionAllow,
		AgentRole: "sdd-archive",
		Strategy:  "direct",
		Reason:    "ok",
	}}

	clock := shared.FixedClock(time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC))
	idGen := shared.FixedIDGenerator([]string{
		"01ARZ3NDEKTSV4RRFFQ69G5PA1", // archive phase ID
		"01ARZ3NDEKTSV4RRFFQ69G5SA1", // session ID
		"01ARZ3NDEKTSV4RRFFQ69G5PA2",
		"01ARZ3NDEKTSV4RRFFQ69G5SA2",
	})

	disp := &fakeDispatcher{result: &outbound.DispatchResult{
		ExitCode:    0,
		Stdout:      []byte{},
		EnvelopeRaw: mustArchiveEnvelope(t),
	}}

	pr := newFakePhaseRepo()

	// Seed a completed PhaseVerify to satisfy Iron Law 3 (IL3: no archive
	// without verify DONE, confidence >= 0.9).
	verifyPID, _ := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5PV1")
	verifyPhase := phase.Hydrate(
		verifyPID, cid, phase.PhaseVerify,
		phase.PhaseStatusDone, nil, 0.92, 3, 1, nil, nil,
	)
	pr.byID[verifyPID.String()] = verifyPhase
	pr.byChangeAndType[cid.String()+"|"+string(phase.PhaseVerify)] = verifyPhase

	h := &harness{
		changeRepo: cr,
		phaseRepo:  pr,
		sessRepo:   newFakeSessionRepo(),
		governance: gov,
		memory:     &fakeMemory{},
		dispatcher: disp,
		spawn:      &fakeSpawnGov{},
		audit:      &fakeAudit{},
		events:     &fakeEvents{},
		clock:      clock,
	}

	val := discipline.NewValidator()
	il := discipline.NewIronLawChecker()
	pb := discipline.NewPromptBuilder()

	h.svc = appphase.New(appphase.Deps{
		ChangeRepo:  cr,
		PhaseRepo:   h.phaseRepo,
		SessionRepo: h.sessRepo,
		Governance:  gov,
		Memory:      h.memory,
		Dispatcher:  disp,
		SpawnGov:    h.spawn,
		Validator:   val,
		IronLaw:     il,
		Prompts:     pb,
		Audit:       h.audit,
		Events:      h.events,
		Clock:       clock,
		IDGen:       idGen,
		Scheduler:   appphase.SyncScheduler,
	})
	return h
}

func mustArchiveEnvelope(t *testing.T) []byte {
	t.Helper()
	body := map[string]any{
		"schema_version":    "v1",
		"phase":             string(phase.PhaseArchive),
		"change_name":       "archive-test",
		"project":           "proj",
		"status":            string(envelope.StatusDone),
		"confidence":        0.95,
		"executive_summary": "archived",
		"artifacts_saved":   []map[string]any{},
		"next_recommended":  []string{},
		"risks":             []map[string]string{},
		"data":              map[string]any{},
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	return raw
}

// countPublishedByType returns the count of events matching eventType.
func countPublishedByType(events *fakeEvents, eventType string) int {
	events.mu.Lock()
	defer events.mu.Unlock()
	count := 0
	for _, ev := range events.published {
		if ev.Type == eventType {
			count++
		}
	}
	return count
}

// findEventByType returns the first published event matching eventType, or nil.
func findEventByType(events *fakeEvents, eventType string) *inbound.Event {
	events.mu.Lock()
	defer events.mu.Unlock()
	for i := range events.published {
		if events.published[i].Type == eventType {
			return &events.published[i]
		}
	}
	return nil
}

// B.1 — Happy path: advanceChange(PhaseArchive) emits exactly one
// EventPhaseArchived with correct PhaseArchivedPayload fields.
func TestAdvanceChange_PhaseArchive_EmitsExactlyOne_PhaseArchivedEvent(t *testing.T) {
	h := archiveHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C02")

	out, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:        cid,
		PhaseType:       phase.PhaseArchive,
		TaskDescription: "archive the change",
		RetryBudget:     3,
	})
	require.NoError(t, err)
	require.NotEmpty(t, out.PhaseID.String())

	// Assert exactly one EventPhaseArchived was emitted.
	count := countPublishedByType(h.events, inbound.EventPhaseArchived)
	require.Equal(t, 1, count, "expected exactly 1 phase.archived event, got %d", count)

	// Assert payload shape.
	ev := findEventByType(h.events, inbound.EventPhaseArchived)
	require.NotNil(t, ev, "phase.archived event not found in published events")

	payload, ok := ev.Payload.(inbound.PhaseArchivedPayload)
	require.True(t, ok, "expected PhaseArchivedPayload type, got %T", ev.Payload)
	require.Equal(t, cid.String(), payload.ChangeID)
	require.Equal(t, "archive-test", payload.ChangeName)
	require.Equal(t, string(phase.PhaseArchive), payload.PhaseType)
	require.Equal(t, h.clock.Now(), payload.ArchivedAt, "ArchivedAt must equal Clock.Now()")
}

// B.2 — Failure path: ChangeRepo.Save returns error after MarkCompleted →
// zero EventPhaseArchived emissions (Iron Law D1.2 guard).
func TestAdvanceChange_PhaseArchive_SaveError_NoEmission(t *testing.T) {
	h := archiveHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C02")

	// Inject a Save error. Note: the first Save (from AdvancePhase path) may
	// also fail, but we want the archive-specific Save to fail. We set saveErr
	// globally — this causes ChangeRepo.Save to always error.
	h.changeRepo.saveErr = errors.New("simulated db failure")

	_, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:        cid,
		PhaseType:       phase.PhaseArchive,
		TaskDescription: "archive the change",
		RetryBudget:     3,
	})
	require.NoError(t, err) // Run itself does not propagate advanceChange errors.

	// Assert zero EventPhaseArchived emissions when Save fails.
	count := countPublishedByType(h.events, inbound.EventPhaseArchived)
	require.Equal(t, 0, count, "expected 0 phase.archived events on Save error, got %d", count)
}

// B.3 — Idempotency: calling Run(PhaseArchive) a second time (after the
// Change is already terminal/Completed) must yield exactly ONE total
// EventPhaseArchived emission across both calls.
//
// Why: MarkCompleted returns ErrAlreadyTerminal on the second call (Change is
// already StatusCompleted), so the emission guard `if err == nil` prevents
// a second emission naturally. This test proves that natural idempotency.
func TestAdvanceChange_PhaseArchive_CalledTwice_ExactlyOneEmission(t *testing.T) {
	h := archiveHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C02")

	// First call.
	_, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:        cid,
		PhaseType:       phase.PhaseArchive,
		TaskDescription: "archive the change",
		RetryBudget:     3,
	})
	require.NoError(t, err)

	// Second call — Change is now terminal (StatusCompleted), Run must fail
	// fast with ErrAlreadyTerminal or ErrInvalidTransition before dispatching.
	// The second Run should produce zero additional EventPhaseArchived events
	// regardless of how it fails.
	_, _ = h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:        cid,
		PhaseType:       phase.PhaseArchive,
		TaskDescription: "archive the change (duplicate)",
		RetryBudget:     3,
	})

	// Assert exactly one total EventPhaseArchived across both calls.
	count := countPublishedByType(h.events, inbound.EventPhaseArchived)
	require.Equal(t, 1, count, "expected exactly 1 phase.archived event across 2 Run calls, got %d", count)
}

// B.4 — Non-archive phase: advanceChange(PhaseTasks) must NOT emit
// EventPhaseArchived. The emission is gated on completed == PhaseArchive.
func TestAdvanceChange_NonArchivePhase_NoPhaseArchivedEvent(t *testing.T) {
	// Build a harness with Change at PhaseTasks-ready state (currentPhase = PhaseDesign).
	cr := newFakeChangeRepo()
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C03")
	advanced := domainchange.Hydrate(
		cid, "tasks-test", "proj",
		domainchange.StatusActive, phase.PhaseDesign,
		domainchange.ArtifactStoreMemoryEngine, "main",
		time.Now(), time.Now(),
	)
	cr.byID[cid.String()] = advanced

	gov := &fakeGovernance{decision: &outbound.GovernanceDecision{
		Decision:  outbound.DecisionAllow,
		AgentRole: "sdd-tasks",
		Strategy:  "direct",
		Reason:    "ok",
	}}

	disp := &fakeDispatcher{result: &outbound.DispatchResult{
		ExitCode:    0,
		Stdout:      []byte{},
		EnvelopeRaw: mustEnvelope(t, phase.PhaseTasks, envelope.StatusDone, 0.90),
	}}

	clock := shared.FixedClock(time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC))
	idGen := shared.FixedIDGenerator([]string{
		"01ARZ3NDEKTSV4RRFFQ69G5PT1",
		"01ARZ3NDEKTSV4RRFFQ69G5ST1",
		"01ARZ3NDEKTSV4RRFFQ69G5PT2",
		"01ARZ3NDEKTSV4RRFFQ69G5ST2",
	})

	evs := &fakeEvents{}
	svc := appphase.New(appphase.Deps{
		ChangeRepo:  cr,
		PhaseRepo:   newFakePhaseRepo(),
		SessionRepo: newFakeSessionRepo(),
		Governance:  gov,
		Memory:      &fakeMemory{},
		Dispatcher:  disp,
		SpawnGov:    &fakeSpawnGov{},
		Validator:   discipline.NewValidator(),
		IronLaw:     discipline.NewIronLawChecker(),
		Prompts:     discipline.NewPromptBuilder(),
		Audit:       &fakeAudit{},
		Events:      evs,
		Clock:       clock,
		IDGen:       idGen,
		Scheduler:   appphase.SyncScheduler,
	})

	out, err := svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:        cid,
		PhaseType:       phase.PhaseTasks,
		TaskDescription: "plan tasks",
		RetryBudget:     3,
	})
	require.NoError(t, err)
	require.NotEmpty(t, out.PhaseID.String())

	// Assert zero EventPhaseArchived for non-archive phase.
	count := countPublishedByType(evs, inbound.EventPhaseArchived)
	require.Equal(t, 0, count, "expected 0 phase.archived events for non-archive phase, got %d", count)
}
