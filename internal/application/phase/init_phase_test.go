package phase_test

// init_phase_test.go — F.6–F.9 (Strict TDD: RED tests first)
//
// Tests for the runInitPhase branch in phase.Service:
//   F.6: No LLM dispatch when PhaseInit runs.
//   F.7: No governance + no IronLaw calls when PhaseInit runs.
//   F.8: InitService.Run called BEFORE PhaseRepo.Save (Iron Law D1.2 ordering).
//   F.9: Non-PhaseInit still routes to LLM dispatcher (regression guard).
//
// These tests FAIL TO COMPILE until:
//   - appphase.InitService interface is defined in service.go
//   - appphase.Deps.Init field is added to service.go

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/detector"
	domainchange "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// callOrderRecorder records the order in which named operations are called.
type callOrderRecorder struct {
	mu      sync.Mutex
	counter int64
	calls   map[string]int64
}

func newCallOrderRecorder() *callOrderRecorder {
	return &callOrderRecorder{calls: map[string]int64{}}
}

func (r *callOrderRecorder) Record(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counter++
	r.calls[name] = r.counter
}

func (r *callOrderRecorder) Order(name string) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[name]
}

// --- fakes for INIT phase tests ---

// fakeInitService is a fake that satisfies appphase.InitService.
// The Run method's signature must match the interface defined in service.go.
type fakeInitService struct {
	mu           sync.Mutex
	runCalls     int
	sc           detector.StructuralContext
	env          *envelope.Envelope
	err          error
	callRecorder *callOrderRecorder
}

func (f *fakeInitService) Run(_ context.Context, _ appphase.InitRunInput) (detector.StructuralContext, *envelope.Envelope, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runCalls++
	if f.callRecorder != nil {
		f.callRecorder.Record("init_service.Run")
	}
	return f.sc, f.env, f.err
}

// recordingGovernanceInit wraps fakeGovernance and counts EvaluatePhase calls.
type recordingGovernanceInit struct {
	*fakeGovernance
	evaluateCalls int32
}

func (r *recordingGovernanceInit) EvaluatePhase(ctx context.Context, in outbound.EvaluatePhaseInput) (*outbound.GovernanceDecision, error) {
	atomic.AddInt32(&r.evaluateCalls, 1)
	return r.fakeGovernance.EvaluatePhase(ctx, in)
}

// recordingDispatcherInit wraps fakeDispatcher and counts Dispatch calls.
type recordingDispatcherInit struct {
	*fakeDispatcher
	dispatchCalls int32
}

func (r *recordingDispatcherInit) Dispatch(ctx context.Context, req outbound.DispatchRequest) (*outbound.DispatchResult, error) {
	atomic.AddInt32(&r.dispatchCalls, 1)
	return r.fakeDispatcher.Dispatch(ctx, req)
}

// recordingPhaseRepoInit wraps fakePhaseRepo and records Save calls.
type recordingPhaseRepoInit struct {
	*fakePhaseRepo
	saveCalls    int32
	callRecorder *callOrderRecorder
}

func (r *recordingPhaseRepoInit) Save(ctx context.Context, p *phase.Phase) error {
	atomic.AddInt32(&r.saveCalls, 1)
	if r.callRecorder != nil {
		r.callRecorder.Record("phase_repo.Save")
	}
	return r.fakePhaseRepo.Save(ctx, p)
}

// makeInitEnvelope builds a valid init envelope for fake InitService.
func makeInitEnvelope(t *testing.T) *envelope.Envelope {
	t.Helper()
	return &envelope.Envelope{
		SchemaVersion:    envelope.SchemaVersionV1,
		Phase:            string(phase.PhaseInit),
		ChangeName:       "init-test",
		Project:          "proj",
		Status:           envelope.StatusDone,
		Confidence:       1.0,
		ExecutiveSummary: "INIT complete",
	}
}

// initChangeHarness builds a Change at PhaseInit.
func initChangeHarness(t *testing.T) (ids.ChangeID, *fakeChangeRepo) {
	t.Helper()
	cr := newFakeChangeRepo()
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5I01")
	// New Change starts at PhaseInit — isNextValidTransition("init","init")==true.
	c := domainchange.Hydrate(
		cid, "init-test", "proj",
		domainchange.StatusActive, phase.PhaseInit,
		domainchange.ArtifactStoreMemoryEngine, "main",
		time.Now(), time.Now(),
	)
	cr.byID[cid.String()] = c
	return cid, cr
}

// buildInitPhaseService builds a phase.Service with a fakeInitService injected.
func buildInitPhaseService(
	t *testing.T,
	cr *fakeChangeRepo,
	pr outbound.PhaseRepository,
	gov outbound.GovernanceClient,
	disp outbound.AgentDispatcher,
	initSvc appphase.InitService,
) *appphase.Service {
	t.Helper()
	clock := shared.FixedClock(time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))
	idGen := shared.FixedIDGenerator([]string{
		"01ARZ3NDEKTSV4RRFFQ69G5PI1",
		"01ARZ3NDEKTSV4RRFFQ69G5SI1",
		"01ARZ3NDEKTSV4RRFFQ69G5PI2",
		"01ARZ3NDEKTSV4RRFFQ69G5SI2",
	})
	return appphase.New(appphase.Deps{
		ChangeRepo:  cr,
		PhaseRepo:   pr,
		SessionRepo: newFakeSessionRepo(),
		Governance:  gov,
		Memory:      &fakeMemory{},
		Dispatcher:  disp,
		SpawnGov:    &fakeSpawnGov{},
		Validator:   discipline.NewValidator(),
		IronLaw:     discipline.NewIronLawChecker(),
		Prompts:     discipline.NewPromptBuilder(),
		Audit:       &fakeAudit{},
		Events:      &fakeEvents{},
		Clock:       clock,
		IDGen:       idGen,
		Scheduler:   appphase.SyncScheduler,
		Init:        initSvc,
	})
}

// --- F.6: No LLM dispatch for PhaseInit ---

// TestRunInitPhase_DoesNotDispatchToLLM asserts Dispatcher.Dispatch == 0 for PhaseInit.
func TestRunInitPhase_DoesNotDispatchToLLM(t *testing.T) {
	cid, cr := initChangeHarness(t)
	gov := &recordingGovernanceInit{
		fakeGovernance: &fakeGovernance{decision: &outbound.GovernanceDecision{
			Decision: outbound.DecisionAllow, AgentRole: "sdd-init", Strategy: "direct", Reason: "ok",
		}},
	}
	disp := &recordingDispatcherInit{
		fakeDispatcher: &fakeDispatcher{result: &outbound.DispatchResult{
			ExitCode: 0, Stdout: []byte{},
			EnvelopeRaw: mustEnvelope(t, phase.PhaseSpec, envelope.StatusDone, 0.85),
		}},
	}
	initSvc := &fakeInitService{
		sc:  detector.StructuralContext{SchemaVersion: detector.StructuralContextSchemaV1},
		env: makeInitEnvelope(t),
	}

	svc := buildInitPhaseService(t, cr, newFakePhaseRepo(), gov, disp, initSvc)

	_, err := svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:  cid,
		PhaseType: phase.PhaseInit,
	})
	require.NoError(t, err)

	// F.6: No LLM dispatch.
	require.Equal(t, int32(0), atomic.LoadInt32(&disp.dispatchCalls),
		"Dispatcher.Dispatch must NOT be called for PhaseInit")
}

// --- F.7: No governance for PhaseInit ---

// TestRunInitPhase_SkipsGovernance asserts EvaluatePhase == 0 for PhaseInit.
func TestRunInitPhase_SkipsGovernance(t *testing.T) {
	cid, cr := initChangeHarness(t)
	gov := &recordingGovernanceInit{
		fakeGovernance: &fakeGovernance{decision: &outbound.GovernanceDecision{
			Decision: outbound.DecisionAllow, AgentRole: "sdd-init", Strategy: "direct", Reason: "ok",
		}},
	}
	disp := &recordingDispatcherInit{
		fakeDispatcher: &fakeDispatcher{result: &outbound.DispatchResult{
			ExitCode: 0, Stdout: []byte{},
			EnvelopeRaw: mustEnvelope(t, phase.PhaseSpec, envelope.StatusDone, 0.85),
		}},
	}
	initSvc := &fakeInitService{
		sc:  detector.StructuralContext{SchemaVersion: detector.StructuralContextSchemaV1},
		env: makeInitEnvelope(t),
	}

	svc := buildInitPhaseService(t, cr, newFakePhaseRepo(), gov, disp, initSvc)

	_, err := svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:  cid,
		PhaseType: phase.PhaseInit,
	})
	require.NoError(t, err)

	// F.7: governance NOT called.
	require.Equal(t, int32(0), atomic.LoadInt32(&gov.evaluateCalls),
		"Governance.EvaluatePhase must NOT be called for PhaseInit")
}

// --- F.8: InitService.Run BEFORE PhaseRepo.Save (Iron Law D1.2) ---

// TestRunInitPhase_PersistBeforeSave asserts InitService.Run is called before
// the final PhaseRepo.Save (artifact before state change — Iron Law D1.2).
func TestRunInitPhase_PersistBeforeSave(t *testing.T) {
	recorder := newCallOrderRecorder()
	cid, cr := initChangeHarness(t)

	pr := &recordingPhaseRepoInit{
		fakePhaseRepo: newFakePhaseRepo(),
		callRecorder:  recorder,
	}
	gov := &fakeGovernance{decision: &outbound.GovernanceDecision{
		Decision: outbound.DecisionAllow, AgentRole: "sdd-init", Strategy: "direct", Reason: "ok",
	}}
	disp := &fakeDispatcher{result: &outbound.DispatchResult{
		ExitCode: 0, Stdout: []byte{},
		EnvelopeRaw: mustEnvelope(t, phase.PhaseSpec, envelope.StatusDone, 0.85),
	}}
	initSvc := &fakeInitService{
		sc:           detector.StructuralContext{SchemaVersion: detector.StructuralContextSchemaV1},
		env:          makeInitEnvelope(t),
		callRecorder: recorder,
	}

	svc := buildInitPhaseService(t, cr, pr, gov, disp, initSvc)

	_, err := svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:  cid,
		PhaseType: phase.PhaseInit,
	})
	require.NoError(t, err)

	// F.8: InitService.Run (which includes Persist) must happen BEFORE the
	// final PhaseRepo.Save (the state change). Iron Law D1.2.
	initOrder := recorder.Order("init_service.Run")
	// PhaseRepo.Save is called multiple times (once for Start, once for Complete).
	// We want the LAST Save (the complete-phase one). Since calls are recorded in
	// order, and we only care that init happened before the terminal Save, we can
	// check initOrder < the final Save order by checking any Save > initOrder.
	require.Greater(t, initOrder, int64(0), "InitService.Run must be called")

	// Find the Save that happened AFTER init.Run.
	saveOrder := recorder.Order("phase_repo.Save")
	require.Greater(t, saveOrder, initOrder,
		"PhaseRepo.Save (order=%d) must happen AFTER InitService.Run (order=%d)",
		saveOrder, initOrder)
}

// --- F.9: Non-PhaseInit still dispatches to LLM (regression guard) ---

// TestRun_NonInitPhase_StillDispatchesToLLM asserts that PhaseSpec still goes
// through the LLM dispatcher path (regression guard for the INIT branch).
func TestRun_NonInitPhase_StillDispatchesToLLM(t *testing.T) {
	h := newHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")

	_, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:        cid,
		PhaseType:       phase.PhaseSpec,
		TaskDescription: "draft spec",
		RetryBudget:     3,
	})
	require.NoError(t, err)

	// The standard LLM flow must have fired SpawnGov.Acquire once
	// (reliable proxy for the dispatch path being reached).
	require.Equal(t, 1, h.spawn.acquired,
		"SpawnGovernor.Acquire must be called once for non-INIT phases")
}
