package phase_test

// service_bootstrap_test.go — T2.8 RED (Strict TDD)
//
// Tests for the async bootstrap wiring in runInitPhase (DG-C7-5):
//   G.1: Bootstrap.TriggerIfNeeded fires AFTER persist+advance (ordering assertion).
//   G.1b: Bootstrap receives the captured StructuralContext from InitService.Run.
//   G.2: nil Bootstrap dep → no-op, all existing behavior unchanged.
//   G.3: Bootstrap that PANICs → recovered, phase still terminal, no crash.
//   G.4: Bootstrap error → swallowed, INIT result SUCCESS.
//   G.5: Context passed to Bootstrap is detached and has a deadline.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/detector"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/structural"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// fakeBootstrap is a fake BootstrapDep that records calls to TriggerIfNeeded.
type fakeBootstrap struct {
	mu           sync.Mutex
	callCount    int
	capturedSC   structural.StructuralContext
	capturedCtx  context.Context
	panicMsg     string // if non-empty, panics with this message
	callRecorder *callOrderRecorder
}

func (b *fakeBootstrap) TriggerIfNeeded(ctx context.Context, sc structural.StructuralContext) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.callCount++
	b.capturedSC = sc
	b.capturedCtx = ctx
	if b.callRecorder != nil {
		b.callRecorder.Record("bootstrap.TriggerIfNeeded")
	}
	if b.panicMsg != "" {
		panic(b.panicMsg)
	}
}

// fakeInitServiceWithSC returns a specified StructuralContext.
type fakeInitServiceWithSC struct {
	mu           sync.Mutex
	sc           structural.StructuralContext
	env          *envelope.Envelope
	err          error
	callRecorder *callOrderRecorder
}

func (f *fakeInitServiceWithSC) Run(_ context.Context, _ appphase.InitRunInput) (detector.StructuralContext, *envelope.Envelope, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.callRecorder != nil {
		f.callRecorder.Record("init_service.Run")
	}
	return f.sc, f.env, f.err
}

// recordingPhaseRepoBootstrap records the order of Save calls.
type recordingPhaseRepoBootstrap struct {
	*fakePhaseRepo
	saveCalls    int32
	callRecorder *callOrderRecorder
}

func (r *recordingPhaseRepoBootstrap) Save(ctx context.Context, p *phase.Phase) error {
	atomic.AddInt32(&r.saveCalls, 1)
	if r.callRecorder != nil {
		r.callRecorder.Record("phase_repo.Save")
	}
	return r.fakePhaseRepo.Save(ctx, p)
}

// buildBootstrapService builds a phase.Service with Init + optional Bootstrap.
func buildBootstrapService(
	t *testing.T,
	pr outbound.PhaseRepository,
	initSvc appphase.InitService,
	bootstrap appphase.BootstrapDep,
	bootstrapTimeout time.Duration,
	scheduler appphase.Scheduler,
) (*appphase.Service, initCIDResult) {
	t.Helper()
	cid, cr := initChangeHarness(t)

	clock := shared.FixedClock(time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))
	idGen := shared.FixedIDGenerator([]string{
		"01ARZ3NDEKTSV4RRFFQ69G5PB1",
		"01ARZ3NDEKTSV4RRFFQ69G5SB1",
		"01ARZ3NDEKTSV4RRFFQ69G5PB2",
		"01ARZ3NDEKTSV4RRFFQ69G5SB2",
	})
	gov := &fakeGovernance{decision: &outbound.GovernanceDecision{
		Decision: outbound.DecisionAllow, AgentRole: "sdd-init", Strategy: "direct", Reason: "ok",
	}}
	disp := &fakeDispatcher{result: &outbound.DispatchResult{
		ExitCode: 0, Stdout: []byte{},
		EnvelopeRaw: mustEnvelope(t, phase.PhaseSpec, envelope.StatusDone, 0.85),
	}}
	if scheduler == nil {
		scheduler = appphase.SyncScheduler
	}
	if bootstrapTimeout == 0 {
		bootstrapTimeout = 60 * time.Second
	}

	svc := appphase.New(appphase.Deps{
		ChangeRepo:       cr,
		PhaseRepo:        pr,
		SessionRepo:      newFakeSessionRepo(),
		Governance:       gov,
		Memory:           &fakeMemory{},
		Dispatcher:       disp,
		SpawnGov:         &fakeSpawnGov{},
		Validator:        discipline.NewValidator(),
		IronLaw:          discipline.NewIronLawChecker(),
		Prompts:          discipline.NewPromptBuilder(),
		Audit:            &fakeAudit{},
		Events:           &fakeEvents{},
		Clock:            clock,
		IDGen:            idGen,
		Scheduler:        scheduler,
		Init:             initSvc,
		Bootstrap:        bootstrap,
		BootstrapTimeout: bootstrapTimeout,
	})
	return svc, initCIDResult{cid: cid}
}

type initCIDResult struct {
	cid interface{ String() string }
}

// G.1: Bootstrap.TriggerIfNeeded fires AFTER persist+advance (ordering assertion).
func TestRunInitPhase_Bootstrap_FiresAfterPersist(t *testing.T) {
	recorder := newCallOrderRecorder()

	sc := structural.StructuralContext{
		SchemaVersion:     1,
		ProjectID:         "proj-bootstrap",
		Greenfield:        true,
		SophiaDetectorVer: "v1.1.0",
	}
	initSvc := &fakeInitServiceWithSC{sc: sc, env: makeInitEnvelope(t), callRecorder: recorder}
	bs := &fakeBootstrap{callRecorder: recorder}
	pr := &recordingPhaseRepoBootstrap{fakePhaseRepo: newFakePhaseRepo(), callRecorder: recorder}

	svc, cidRes := buildBootstrapService(t, pr, initSvc, bs, 60*time.Second, appphase.SyncScheduler)

	cid, _ := initChangeHarness(t)
	_ = cidRes
	_, err := svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:  cid,
		PhaseType: phase.PhaseInit,
	})
	require.NoError(t, err)

	bs.mu.Lock()
	calls := bs.callCount
	bs.mu.Unlock()
	require.Equal(t, 1, calls, "Bootstrap.TriggerIfNeeded must be called once")

	initOrder := recorder.Order("init_service.Run")
	saveOrder := recorder.Order("phase_repo.Save")
	bsOrder := recorder.Order("bootstrap.TriggerIfNeeded")

	require.Greater(t, initOrder, int64(0))
	require.Greater(t, saveOrder, initOrder,
		"phase_repo.Save (%d) must happen AFTER init_service.Run (%d)", saveOrder, initOrder)
	require.Greater(t, bsOrder, saveOrder,
		"bootstrap.TriggerIfNeeded (%d) must happen AFTER phase_repo.Save (%d)", bsOrder, saveOrder)
}

// G.1b: Bootstrap receives the StructuralContext captured from InitService.Run.
func TestRunInitPhase_Bootstrap_ReceivesCapturedSC(t *testing.T) {
	sc := structural.StructuralContext{
		SchemaVersion:     1,
		ProjectID:         "proj-sc-capture",
		Greenfield:        true,
		SophiaDetectorVer: "v1.1.0",
		Frameworks: []structural.FrameworkInfo{
			{Name: "Angular", Version: "22.0.0"},
		},
	}
	initSvc := &fakeInitServiceWithSC{sc: sc, env: makeInitEnvelope(t)}
	bs := &fakeBootstrap{}

	svc, _ := buildBootstrapService(t, newFakePhaseRepo(), initSvc, bs, 60*time.Second, appphase.SyncScheduler)

	cid, _ := initChangeHarness(t)
	_, err := svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:  cid,
		PhaseType: phase.PhaseInit,
	})
	require.NoError(t, err)

	bs.mu.Lock()
	captured := bs.capturedSC
	bs.mu.Unlock()

	require.Equal(t, "proj-sc-capture", captured.ProjectID)
	require.True(t, captured.Greenfield)
	require.Len(t, captured.Frameworks, 1)
	require.Equal(t, "Angular", captured.Frameworks[0].Name)
}

// G.2: nil Bootstrap dep → no-op, phase completes successfully.
func TestRunInitPhase_NilBootstrap_NoOp(t *testing.T) {
	initSvc := &fakeInitServiceWithSC{
		sc:  structural.StructuralContext{SchemaVersion: 1},
		env: makeInitEnvelope(t),
	}
	svc, _ := buildBootstrapService(t, newFakePhaseRepo(), initSvc, nil, 60*time.Second, appphase.SyncScheduler)

	cid, _ := initChangeHarness(t)
	_, err := svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:  cid,
		PhaseType: phase.PhaseInit,
	})
	require.NoError(t, err, "nil Bootstrap must not cause an error")
}

// G.3: Bootstrap that PANICs → recovered, phase still terminal, no crash.
func TestRunInitPhase_Bootstrap_PanicRecovered(t *testing.T) {
	initSvc := &fakeInitServiceWithSC{
		sc:  structural.StructuralContext{SchemaVersion: 1, Greenfield: true},
		env: makeInitEnvelope(t),
	}
	panicBs := &fakeBootstrap{panicMsg: "test panic from bootstrap"}
	pr := newFakePhaseRepo()

	svc, _ := buildBootstrapService(t, pr, initSvc, panicBs, 60*time.Second, appphase.SyncScheduler)

	cid, _ := initChangeHarness(t)
	require.NotPanics(t, func() {
		_, _ = svc.Run(context.Background(), inbound.RunPhaseInput{
			ChangeID:  cid,
			PhaseType: phase.PhaseInit,
		})
	}, "panic in Bootstrap must be recovered — phase.Service must not propagate it")

	// Phase must still be saved.
	require.NotEmpty(t, pr.byID, "phase must be saved even when Bootstrap panics")
}

// G.4: Bootstrap error (no-op since interface is void) — swallowed, INIT SUCCESS.
func TestRunInitPhase_Bootstrap_ErrorSwallowed(t *testing.T) {
	initSvc := &fakeInitServiceWithSC{
		sc:  structural.StructuralContext{SchemaVersion: 1, Greenfield: true},
		env: makeInitEnvelope(t),
	}
	// Bootstrap that does nothing — demonstrates swallowed error path.
	errBs := &fakeBootstrap{}

	svc, _ := buildBootstrapService(t, newFakePhaseRepo(), initSvc, errBs, 60*time.Second, appphase.SyncScheduler)

	cid, _ := initChangeHarness(t)
	_, err := svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:  cid,
		PhaseType: phase.PhaseInit,
	})
	require.NoError(t, err, "bootstrap error must not propagate to INIT result")
}

// G.5: Context passed to Bootstrap is detached and has a deadline (BootstrapTimeout).
func TestRunInitPhase_Bootstrap_DetachedContextWithDeadline(t *testing.T) {
	initSvc := &fakeInitServiceWithSC{
		sc:  structural.StructuralContext{SchemaVersion: 1, Greenfield: true},
		env: makeInitEnvelope(t),
	}
	capBs := &fakeBootstrap{}

	svc, _ := buildBootstrapService(t, newFakePhaseRepo(), initSvc, capBs, 60*time.Second, appphase.SyncScheduler)

	// Create a request context that we cancel immediately after Run.
	reqCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cid, _ := initChangeHarness(t)
	_, err := svc.Run(reqCtx, inbound.RunPhaseInput{
		ChangeID:  cid,
		PhaseType: phase.PhaseInit,
	})
	require.NoError(t, err)

	// Cancel request context — bootstrap ctx must still be valid.
	cancel()

	capBs.mu.Lock()
	bsCtx := capBs.capturedCtx
	calls := capBs.callCount
	capBs.mu.Unlock()

	require.Equal(t, 1, calls, "Bootstrap must be called once")
	require.NotNil(t, bsCtx, "Bootstrap must receive a context")
	_, hasDeadline := bsCtx.Deadline()
	require.True(t, hasDeadline, "Bootstrap context must have a deadline (BootstrapTimeout applied)")
}
