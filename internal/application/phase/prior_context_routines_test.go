package phase_test

// prior_context_routines_test.go — Group I RED tests (M4 PR2)
//
// TDD cycle: tests written FIRST against unexported buildPriorContext.
// Tests exercise via Run() and inspect the dispatched prompt for routine
// content. All tests MUST fail RED until I-2 implements the population logic.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	appphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/phase"
	domainchange "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/structural"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
	"time"
)

// ---------------------------------------------------------------------------
// graphMemory: a MemoryClient that returns a configurable StructuralContext
// (with GraphSummary) from GetByTopicKey and otherwise is a no-op stub.
// ---------------------------------------------------------------------------

type graphMemory struct {
	structuralCtx *structural.StructuralContext
}

func (m *graphMemory) Ingest(_ context.Context, _ outbound.IngestMemoryInput) (*outbound.MemoryRecord, error) {
	return nil, nil
}
func (m *graphMemory) Get(_ context.Context, _ string) (*outbound.MemoryRecord, error) {
	return nil, nil
}
func (m *graphMemory) GetByTopicKey(_ context.Context, _ outbound.MemoryScope, _ string) (*outbound.MemoryRecord, error) {
	if m.structuralCtx == nil {
		return nil, nil
	}
	raw, err := json.Marshal(m.structuralCtx)
	if err != nil {
		return nil, err
	}
	return &outbound.MemoryRecord{Content: string(raw)}, nil
}
func (m *graphMemory) Archive(_ context.Context, _, _, _ string) error { return nil }
func (m *graphMemory) RecordDecision(_ context.Context, _ outbound.RecordDecisionInput) (*outbound.MemoryRecord, error) {
	return nil, nil
}
func (m *graphMemory) RecordRelation(_ context.Context, _ outbound.RecordRelationInput) error {
	return nil
}
func (m *graphMemory) BuildContext(_ context.Context, _ outbound.ContextRequest) (*outbound.ContextBundle, error) {
	return nil, nil
}
func (m *graphMemory) Search(_ context.Context, _ outbound.SearchQuery) (*outbound.SearchResults, error) {
	return nil, nil
}

var _ outbound.MemoryClient = (*graphMemory)(nil)

// newHarnessWithGraphMemory creates a harness wired with graphMemory and the
// given phase type pre-set as the change's current phase.
// When nextPhase is PhaseApply, a completed tasks phase is seeded in the
// phaseRepo so that IronLaw IL2 is satisfied and the dispatcher is reached.
func newHarnessWithGraphMemory(t *testing.T, mem *graphMemory, currentPhase, nextPhase phase.PhaseType) *harness {
	t.Helper()
	h := newHarness(t)

	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	advanced := domainchange.Hydrate(cid, "feat-x", "proj",
		domainchange.StatusActive, currentPhase,
		domainchange.ArtifactStoreMemoryEngine, "main",
		time.Now(), time.Now())
	h.changeRepo.byID[cid.String()] = advanced
	h.dispatcher.result.EnvelopeRaw = mustEnvelope(t, nextPhase, envelope.StatusDone, 0.85)

	// IL2: APPLY requires a done tasks phase. Seed one so the IronLaw check
	// passes and buildPriorContext (and the dispatcher) is actually reached.
	if nextPhase == phase.PhaseApply {
		tasksPhaseID, _ := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5T01")
		doneTasksPhase := phase.Hydrate(
			tasksPhaseID, cid, phase.PhaseTasks,
			phase.PhaseStatusDone, nil, 0.85,
			3, 1, nil, nil,
		)
		h.phaseRepo.byID[tasksPhaseID.String()] = doneTasksPhase
		h.phaseRepo.byChangeAndType[cid.String()+"|"+string(phase.PhaseTasks)] = doneTasksPhase
	}

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

// ---------------------------------------------------------------------------
// I-1: graph_stats routine present on all phase types
// ---------------------------------------------------------------------------

// TestBuildPriorContext_GraphStats_AllPhases verifies that a non-nil GraphSummary
// causes buildPriorContext to emit a "graphify.graph_stats" routine for every
// phase type. The routine content must appear in the dispatched prompt.
// RED: fails until I-2 populates Routines from GraphSummary.
func TestBuildPriorContext_GraphStats_AllPhases(t *testing.T) {
	gs := &structural.GraphSummary{TotalNodes: 50, TotalEdges: 120, CommunityCount: 6}
	sc := &structural.StructuralContext{
		SchemaVersion: structural.SchemaV1,
		ProjectID:     "proj",
		ChangeName:    "feat-x",
		GraphSummary:  gs,
	}

	// Run 5 distinct phase types through the dispatcher path (INIT short-circuits
	// before prompt dispatch so it is excluded; coverage uses EXPLORE through APPLY).
	cases := []struct {
		current phase.PhaseType
		next    phase.PhaseType
	}{
		{phase.PhaseInit, phase.PhaseExplore},
		{phase.PhaseExplore, phase.PhaseProposal},
		{phase.PhaseProposal, phase.PhaseSpec},
		{phase.PhaseDesign, phase.PhaseTasks},
		{phase.PhaseTasks, phase.PhaseApply},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(string(tc.next), func(t *testing.T) {
			mem := &graphMemory{structuralCtx: sc}
			h := newHarnessWithGraphMemory(t, mem, tc.current, tc.next)

			cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
			_, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
				ChangeID:  cid,
				PhaseType: tc.next,
			})
			require.NoError(t, err)

			require.Contains(t, h.dispatcher.lastPrompt,
				"Graph: 50 nodes, 120 edges, 6 communities",
				"graph_stats routine content must appear in prompt for phase %s", tc.next)
		})
	}
}

// ---------------------------------------------------------------------------
// I-1: god_nodes routine present on EXPLORE and APPLY only
// ---------------------------------------------------------------------------

// TestBuildPriorContext_GodNodes_ExploreApplyOnly verifies that god_nodes content
// appears in the prompt for EXPLORE and APPLY phases but NOT for INIT, DESIGN, VERIFY.
// RED: fails until I-2 implements phase gating.
func TestBuildPriorContext_GodNodes_ExploreApplyOnly(t *testing.T) {
	gs := &structural.GraphSummary{
		TotalNodes:     10,
		TotalEdges:     20,
		CommunityCount: 2,
		GodNodes:       []string{"pkg/core", "pkg/domain"},
	}
	sc := &structural.StructuralContext{
		SchemaVersion: structural.SchemaV1,
		ProjectID:     "proj",
		ChangeName:    "feat-x",
		GraphSummary:  gs,
	}

	godNodesContent := "Top blast-radius nodes: pkg/core, pkg/domain"

	// EXPLORE and APPLY are the two phases that get god_nodes.
	// p.Type() is the phase being RUN — so next=PhaseExplore means the explore
	// phase is executing. current must be a valid predecessor.
	wantGodNodes := []struct {
		current phase.PhaseType
		next    phase.PhaseType
	}{
		{phase.PhaseInit, phase.PhaseExplore},
		{phase.PhaseTasks, phase.PhaseApply},
	}
	for _, tc := range wantGodNodes {
		tc := tc
		t.Run("want_god_nodes_"+string(tc.next), func(t *testing.T) {
			mem := &graphMemory{structuralCtx: sc}
			h := newHarnessWithGraphMemory(t, mem, tc.current, tc.next)

			cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
			_, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
				ChangeID: cid, PhaseType: tc.next,
			})
			require.NoError(t, err)
			require.Contains(t, h.dispatcher.lastPrompt, godNodesContent,
				"god_nodes must appear in prompt for phase %s", tc.next)
		})
	}

	// All other phases must NOT include god_nodes.
	noGodNodes := []struct {
		current phase.PhaseType
		next    phase.PhaseType
	}{
		{phase.PhaseExplore, phase.PhaseProposal},
		{phase.PhaseProposal, phase.PhaseSpec},
		{phase.PhaseSpec, phase.PhaseDesign},
		{phase.PhaseDesign, phase.PhaseTasks},
	}
	for _, tc := range noGodNodes {
		tc := tc
		t.Run("no_god_nodes_"+string(tc.next), func(t *testing.T) {
			mem := &graphMemory{structuralCtx: sc}
			h := newHarnessWithGraphMemory(t, mem, tc.current, tc.next)

			cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
			_, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
				ChangeID: cid, PhaseType: tc.next,
			})
			require.NoError(t, err)
			require.NotContains(t, h.dispatcher.lastPrompt, godNodesContent,
				"god_nodes must NOT appear in prompt for non-explore/apply phase %s", tc.next)
		})
	}
}

// ---------------------------------------------------------------------------
// I-1: nil GraphSummary → empty routines, no panic
// ---------------------------------------------------------------------------

// TestBuildPriorContext_NilGraphSummary_EmptyRoutines verifies that when
// StructuralContext.GraphSummary is nil, no routine content appears in the prompt.
// RED: passes vacuously until I-2 (nil guard already covers this; kept for spec coverage).
func TestBuildPriorContext_NilGraphSummary_EmptyRoutines(t *testing.T) {
	sc := &structural.StructuralContext{
		SchemaVersion: structural.SchemaV1,
		ProjectID:     "proj",
		ChangeName:    "feat-x",
		GraphSummary:  nil, // explicitly nil
	}
	mem := &graphMemory{structuralCtx: sc}
	h := newHarnessWithGraphMemory(t, mem, phase.PhaseProposal, phase.PhaseSpec)

	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	_, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err, "nil GraphSummary must not cause error")
	require.NotContains(t, h.dispatcher.lastPrompt, "Graph:",
		"no graph_stats content must appear when GraphSummary is nil")
	require.NotContains(t, h.dispatcher.lastPrompt, "blast-radius",
		"no god_nodes content must appear when GraphSummary is nil")
}

// ---------------------------------------------------------------------------
// I-1: nil StructuralCtx → empty routines, no panic
// ---------------------------------------------------------------------------

// TestBuildPriorContext_NilStructuralCtx_EmptyRoutines verifies that when
// GetByTopicKey returns nil (no structural record), no routines are populated.
// RED: already passes vacuously; kept for explicit spec coverage and regression.
func TestBuildPriorContext_NilStructuralCtx_EmptyRoutines(t *testing.T) {
	mem := &graphMemory{structuralCtx: nil} // GetByTopicKey returns nil, nil
	h := newHarnessWithGraphMemory(t, mem, phase.PhaseProposal, phase.PhaseSpec)

	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	_, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err, "nil StructuralCtx must not cause error or panic")
	require.NotContains(t, h.dispatcher.lastPrompt, "Graph:",
		"no graph_stats when StructuralCtx is nil")
}
