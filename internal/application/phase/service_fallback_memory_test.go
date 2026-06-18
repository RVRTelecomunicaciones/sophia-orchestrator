package phase_test

// Tests for the fallbackToMemory recovery path (A4 fix).
//
// Scenario: dispatcher returns an empty EnvelopeRaw (network flake, timeout, etc.)
// but a valid envelope was previously persisted to the memory-engine under the
// canonical topic key "sdd/<changeName>/<phaseType>". The recovery path should
// read that record and return its Content as the raw envelope bytes, allowing
// envelope validation to succeed and the phase to complete normally.
//
// Bug (A4): before the fix, fallbackToMemory returned []byte("") regardless of
// the fetched record, making the recovery path non-functional.

import (
	"context"
	"testing"
	"time"

	appphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	domainchange "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// memoryClientStub is a MemoryClient that returns a fixed record from Get,
// delegating all other methods to no-ops. It is separate from fakeMemory to
// let tests configure Get returns without touching the shared fake.
type memoryClientStub struct {
	getRecord *outbound.MemoryRecord
	getErr    error
}

func (m *memoryClientStub) Ingest(_ context.Context, _ outbound.IngestMemoryInput) (*outbound.MemoryRecord, error) {
	return nil, nil
}
func (m *memoryClientStub) Get(_ context.Context, _ string) (*outbound.MemoryRecord, error) {
	return m.getRecord, m.getErr
}
func (m *memoryClientStub) GetByTopicKey(_ context.Context, _ outbound.MemoryScope, _ string) (*outbound.MemoryRecord, error) {
	return nil, nil
}
func (m *memoryClientStub) Archive(_ context.Context, _, _, _ string) error { return nil }
func (m *memoryClientStub) Search(_ context.Context, _ outbound.SearchQuery) (*outbound.SearchResults, error) {
	return nil, nil
}
func (m *memoryClientStub) BuildContext(_ context.Context, _ outbound.ContextRequest) (*outbound.ContextBundle, error) {
	return nil, nil
}
func (m *memoryClientStub) RecordDecision(_ context.Context, _ outbound.RecordDecisionInput) (*outbound.MemoryRecord, error) {
	return nil, nil
}
func (m *memoryClientStub) RecordRelation(_ context.Context, _ outbound.RecordRelationInput) error {
	return nil
}

var _ outbound.MemoryClient = (*memoryClientStub)(nil)

// newHarnessWithMemoryClient clones the standard harness but replaces the
// MemoryClient with the provided implementation. All other deps are identical
// to newHarness so governance, dispatcher, and phase-repo behaviour is shared.
func newHarnessWithMemoryClient(t *testing.T, mem outbound.MemoryClient) *harness {
	t.Helper()
	h := newHarness(t)

	h.svc = appphase.New(appphase.Deps{
		ChangeRepo:  h.changeRepo,
		PhaseRepo:   h.phaseRepo,
		SessionRepo: h.sessRepo,
		Governance:  h.governance,
		Memory:      mem,
		Dispatcher:  h.dispatcher,
		SpawnGov:    h.spawn,
		Validator:   discipline.NewValidator(),
		IronLaw:     discipline.NewIronLawChecker(),
		Prompts:     discipline.NewPromptBuilder(),
		Audit:       h.audit,
		Events:      h.events,
		Clock:       h.clock,
		IDGen: shared.FixedIDGenerator([]string{
			"01ARZ3NDEKTSV4RRFFQ69G5P01",
			"01ARZ3NDEKTSV4RRFFQ69G5S01",
		}),
		Scheduler: appphase.SyncScheduler,
	})
	return h
}

// newFallbackChange wires a change in the changeRepo at PhaseProposal so that
// PhaseSpec is a valid next transition (mirrors the newHarness default).
// Needed when the test must pre-set the change state explicitly.
func newFallbackChange(t *testing.T, h *harness) ids.ChangeID {
	t.Helper()
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	clock := shared.FixedClock(time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC))
	advanced := domainchange.Hydrate(cid, "feat-x", "proj",
		domainchange.StatusActive, phase.PhaseProposal,
		domainchange.ArtifactStoreMemoryEngine, "main",
		clock.Now(), clock.Now())
	h.changeRepo.byID[cid.String()] = advanced
	return cid
}

// TestFallbackToMemory_RecoversPhasWhenDispatcherReturnsNoEnvelope is the
// canonical RED → GREEN test for bug A4.
//
// Before fix: fallbackToMemory returns []byte("") → envelope validation fails
// → phase ends BLOCKED even though memory holds a valid envelope.
//
// After fix: fallbackToMemory returns []byte(rec.Content) → envelope validation
// passes → phase ends DONE (or DONE_WITH_CONCERNS).
func TestFallbackToMemory_RecoversPhasWhenDispatcherReturnsNoEnvelope(t *testing.T) {
	// Build a structurally valid spec envelope to store in the fake memory record.
	validEnvelope := mustEnvelope(t, phase.PhaseSpec, envelope.StatusDone, 0.85)

	mem := &memoryClientStub{
		getRecord: &outbound.MemoryRecord{
			ID:       "mem-001",
			Type:     "sdd_spec",
			Status:   "active",
			TopicKey: "sdd/feat-x/spec",
			Content:  string(validEnvelope),
		},
	}

	h := newHarnessWithMemoryClient(t, mem)
	cid := newFallbackChange(t, h)

	// Dispatcher returns empty EnvelopeRaw to trigger the fallback path.
	h.dispatcher.result.EnvelopeRaw = nil

	out, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:        cid,
		PhaseType:       phase.PhaseSpec,
		TaskDescription: "draft spec",
		RetryBudget:     3,
	})
	require.NoError(t, err)

	stored, err := h.phaseRepo.FindByID(context.Background(), out.PhaseID)
	require.NoError(t, err)

	// Before the fix this assertion fails: []byte("") fails validation → BLOCKED.
	// After the fix: memory content is used → validation passes → DONE.
	require.NotEqual(t, phase.PhaseStatusBlocked, stored.Status(),
		"recovery path must succeed when memory holds a valid envelope: got %s", stored.Status())
	require.True(t,
		stored.Status() == phase.PhaseStatusDone ||
			stored.Status() == phase.PhaseStatusDoneWithConcerns,
		"expected DONE or DONE_WITH_CONCERNS after memory recovery, got %s", stored.Status())
}

// TestFallbackToMemory_ReturnsNilWhenMemoryRecordMissing guards against
// over-correction: when memory has no record, the phase must remain BLOCKED
// (same behaviour as before the fix for the nil-record path).
func TestFallbackToMemory_ReturnsNilWhenMemoryRecordMissing(t *testing.T) {
	mem := &memoryClientStub{
		getRecord: nil, // no record in memory
	}

	h := newHarnessWithMemoryClient(t, mem)
	cid := newFallbackChange(t, h)
	h.dispatcher.result.EnvelopeRaw = nil

	out, _ := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:  cid,
		PhaseType: phase.PhaseSpec,
	})

	stored, _ := h.phaseRepo.FindByID(context.Background(), out.PhaseID)
	require.Equal(t, phase.PhaseStatusBlocked, stored.Status(),
		"phase must be BLOCKED when neither dispatcher nor memory can provide an envelope")
}
