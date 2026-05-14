package phase_test

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/worktree"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// --- fakes ---

type fakeChangeRepo struct {
	mu      sync.Mutex
	byID    map[string]*domainchange.Change
	saveErr error
	findErr error
}

func newFakeChangeRepo() *fakeChangeRepo {
	return &fakeChangeRepo{byID: map[string]*domainchange.Change{}}
}

func (r *fakeChangeRepo) Save(_ context.Context, c *domainchange.Change) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.saveErr != nil {
		return r.saveErr
	}
	r.byID[c.ID().String()] = c
	return nil
}

func (r *fakeChangeRepo) FindByID(_ context.Context, id ids.ChangeID) (*domainchange.Change, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.findErr != nil {
		return nil, r.findErr
	}
	c, ok := r.byID[id.String()]
	if !ok {
		return nil, outbound.ErrNotFound
	}
	return c, nil
}

func (r *fakeChangeRepo) FindByProjectName(_ context.Context, _, _ string) (*domainchange.Change, error) {
	return nil, outbound.ErrNotFound
}

func (r *fakeChangeRepo) List(_ context.Context, _, _ string, _, _ int) ([]*domainchange.Change, error) {
	return nil, nil
}

type fakePhaseRepo struct {
	mu          sync.Mutex
	byID        map[string]*phase.Phase
	byChangeAndType map[string]*phase.Phase // "<changeID>|<type>"
	running     *phase.Phase
	saveErr     error
	lockErr     error
}

func newFakePhaseRepo() *fakePhaseRepo {
	return &fakePhaseRepo{
		byID:            map[string]*phase.Phase{},
		byChangeAndType: map[string]*phase.Phase{},
	}
}

func (r *fakePhaseRepo) Save(_ context.Context, p *phase.Phase) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.saveErr != nil {
		return r.saveErr
	}
	r.byID[p.ID().String()] = p
	r.byChangeAndType[p.ChangeID().String()+"|"+string(p.Type())] = p
	return nil
}

func (r *fakePhaseRepo) FindByID(_ context.Context, id ids.PhaseID) (*phase.Phase, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.byID[id.String()]
	if !ok {
		return nil, outbound.ErrNotFound
	}
	return p, nil
}

func (r *fakePhaseRepo) FindByChangeAndType(_ context.Context, c ids.ChangeID, pt phase.PhaseType) (*phase.Phase, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.byChangeAndType[c.String()+"|"+string(pt)]
	if !ok {
		return nil, outbound.ErrNotFound
	}
	return p, nil
}

func (r *fakePhaseRepo) FindRunningByChange(_ context.Context, _ ids.ChangeID) (*phase.Phase, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running == nil {
		return nil, outbound.ErrNotFound
	}
	return r.running, nil
}

func (r *fakePhaseRepo) LockByChange(_ context.Context, _ ids.ChangeID) error { return r.lockErr }

type fakeSessionRepo struct {
	mu      sync.Mutex
	byID    map[string]*session.Session
	saveErr error
}

func newFakeSessionRepo() *fakeSessionRepo {
	return &fakeSessionRepo{byID: map[string]*session.Session{}}
}

func (r *fakeSessionRepo) Save(_ context.Context, s *session.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.saveErr != nil {
		return r.saveErr
	}
	r.byID[s.ID().String()] = s
	return nil
}

func (r *fakeSessionRepo) FindByID(_ context.Context, id ids.SessionID) (*session.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.byID[id.String()]
	if !ok {
		return nil, outbound.ErrNotFound
	}
	return s, nil
}

func (r *fakeSessionRepo) FindByPhaseID(_ context.Context, _ ids.PhaseID) ([]*session.Session, error) {
	return nil, nil
}

type fakeGovernance struct {
	decision *outbound.GovernanceDecision
	err      error
}

func (g *fakeGovernance) EvaluatePhase(_ context.Context, _ outbound.EvaluatePhaseInput) (*outbound.GovernanceDecision, error) {
	if g.err != nil {
		return nil, g.err
	}
	return g.decision, nil
}

func (g *fakeGovernance) AwaitApproval(_ context.Context, _ ids.ChangeID, _ ids.PhaseID) error {
	return nil
}

func (g *fakeGovernance) EvaluateSensitiveAction(_ context.Context, _ ids.ChangeID, _ string, _ []byte) (*outbound.GovernanceDecision, error) {
	return g.decision, g.err
}

type fakeMemory struct {
	bundle    *outbound.ContextBundle
	getErr    error
	ingestErr error
}

func (m *fakeMemory) Ingest(_ context.Context, _ outbound.IngestMemoryInput) (*outbound.MemoryRecord, error) {
	return nil, m.ingestErr
}

func (m *fakeMemory) Get(_ context.Context, _ string) (*outbound.MemoryRecord, error) {
	return nil, m.getErr
}

func (m *fakeMemory) GetByTopicKey(_ context.Context, _ outbound.MemoryScope, _ string) (*outbound.MemoryRecord, error) {
	return nil, m.getErr
}

func (m *fakeMemory) Archive(_ context.Context, _, _, _ string) error {
	return nil
}

func (m *fakeMemory) Search(_ context.Context, _ outbound.SearchQuery) (*outbound.SearchResults, error) {
	return nil, nil
}

func (m *fakeMemory) BuildContext(_ context.Context, _ outbound.ContextRequest) (*outbound.ContextBundle, error) {
	return m.bundle, nil
}

func (m *fakeMemory) RecordDecision(_ context.Context, _ outbound.RecordDecisionInput) (*outbound.MemoryRecord, error) {
	return nil, nil
}

func (m *fakeMemory) RecordRelation(_ context.Context, _ outbound.RecordRelationInput) error {
	return nil
}

type fakeDispatcher struct {
	result *outbound.DispatchResult
	err    error
}

func (d *fakeDispatcher) Provider() session.Provider                    { return session.ProviderOpenCode }
func (d *fakeDispatcher) SuggestedMaxConcurrent() int                    { return 4 }
func (d *fakeDispatcher) HealthCheck(_ context.Context) error            { return nil }
func (d *fakeDispatcher) Dispatch(_ context.Context, _ outbound.DispatchRequest) (*outbound.DispatchResult, error) {
	if d.err != nil {
		return nil, d.err
	}
	return d.result, nil
}

type fakeSpawnGov struct {
	acquireErr error
	releaseErr error
	acquired   int
	released   int
}

func (s *fakeSpawnGov) Acquire(_ context.Context) error {
	s.acquired++
	return s.acquireErr
}

func (s *fakeSpawnGov) Release(_ context.Context) error {
	s.released++
	return s.releaseErr
}

type fakeAudit struct {
	mu     sync.Mutex
	events []outbound.AuditEvent
	err    error
}

func (a *fakeAudit) Append(_ context.Context, e outbound.AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	a.events = append(a.events, e)
	return nil
}

func (a *fakeAudit) eventTypes() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, 0, len(a.events))
	for _, e := range a.events {
		out = append(out, e.EventType)
	}
	return out
}

func (a *fakeAudit) HasEventForPhase(_ context.Context, phaseID ids.PhaseID, eventType string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range a.events {
		if e.EventType == eventType && e.PhaseID != nil && *e.PhaseID == phaseID {
			return true, nil
		}
	}
	return false, nil
}

type fakeEvents struct {
	mu        sync.Mutex
	published []inbound.Event
}

func (e *fakeEvents) Subscribe(_ context.Context, _ ids.PhaseID) (<-chan inbound.Event, func(), error) {
	ch := make(chan inbound.Event)
	return ch, func() { close(ch) }, nil
}

func (e *fakeEvents) Publish(_ context.Context, _ ids.PhaseID, ev inbound.Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.published = append(e.published, ev)
	return nil
}

func (e *fakeEvents) types() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.published))
	for _, ev := range e.published {
		out = append(out, ev.Type)
	}
	return out
}

// --- fixtures ---

type harness struct {
	svc        *appphase.Service
	changeRepo *fakeChangeRepo
	phaseRepo  *fakePhaseRepo
	sessRepo   *fakeSessionRepo
	governance *fakeGovernance
	memory     *fakeMemory
	dispatcher *fakeDispatcher
	spawn      *fakeSpawnGov
	audit      *fakeAudit
	events     *fakeEvents
	clock      shared.Clock
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	cr := newFakeChangeRepo()
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	c, err := domainchange.New(cid, "feat-x", "proj", domainchange.ArtifactStoreMemoryEngine, "main", time.Now())
	require.NoError(t, err)
	cr.byID[cid.String()] = c

	gov := &fakeGovernance{decision: &outbound.GovernanceDecision{
		Decision:  outbound.DecisionAllow,
		AgentRole: "sdd-spec",
		Strategy:  "direct",
		Reason:    "ok",
	}}

	disp := &fakeDispatcher{result: &outbound.DispatchResult{
		ExitCode: 0,
		Stdout:   []byte{},
		EnvelopeRaw: mustEnvelope(t, phase.PhaseSpec, envelope.StatusDone, 0.85),
	}}

	clock := shared.FixedClock(time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC))
	idGen := shared.FixedIDGenerator([]string{
		"01ARZ3NDEKTSV4RRFFQ69G5P01", // phase id for first Run
		"01ARZ3NDEKTSV4RRFFQ69G5S01", // session id
		"01ARZ3NDEKTSV4RRFFQ69G5P02",
		"01ARZ3NDEKTSV4RRFFQ69G5S02",
	})

	val := discipline.NewValidator()
	il := discipline.NewIronLawChecker()
	pb := discipline.NewPromptBuilder()

	h := &harness{
		changeRepo: cr,
		phaseRepo:  newFakePhaseRepo(),
		sessRepo:   newFakeSessionRepo(),
		governance: gov,
		memory:     &fakeMemory{},
		dispatcher: disp,
		spawn:      &fakeSpawnGov{},
		audit:      &fakeAudit{},
		events:     &fakeEvents{},
		clock:      clock,
	}

	// Advance change to PhaseExplore so PhaseSpec is a valid next transition
	// (proposal -> spec). Set CurrentPhase via a hydrated change for clarity.
	advanced := domainchange.Hydrate(cid, "feat-x", "proj",
		domainchange.StatusActive, phase.PhaseProposal,
		domainchange.ArtifactStoreMemoryEngine, "main",
		clock.Now(), clock.Now())
	cr.byID[cid.String()] = advanced

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

func mustEnvelope(t *testing.T, pt phase.PhaseType, status envelope.Status, conf float64) []byte {
	t.Helper()
	body := map[string]any{
		"schema_version":    "v1",
		"phase":             string(pt),
		"change_name":       "feat-x",
		"project":           "proj",
		"status":            string(status),
		"confidence":        conf,
		"executive_summary": "ok",
		"artifacts_saved":   []map[string]any{},
		"next_recommended":  []string{},
		"risks":             []map[string]string{},
		"data":              map[string]any{},
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	return raw
}

// --- tests ---

func TestNew_PanicsOnNilDep(t *testing.T) {
	require.Panics(t, func() { appphase.New(appphase.Deps{}) })
}

func TestNew_DefaultsConfig(t *testing.T) {
	h := newHarness(t)
	require.NotNil(t, h.svc)
}

func TestSchedulers(t *testing.T) {
	called := false
	appphase.SyncScheduler(func() { called = true })
	require.True(t, called)
	// AsyncScheduler: just verify it doesn't panic; goroutine completion not asserted.
	done := make(chan struct{})
	appphase.AsyncScheduler(func() { close(done) })
	<-done
}

func TestRun_HappyPath_PersistsAndCompletes(t *testing.T) {
	h := newHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:        cid,
		PhaseType:       phase.PhaseSpec,
		TaskDescription: "draft spec",
		RetryBudget:     3,
	})
	require.NoError(t, err)
	require.NotEmpty(t, out.PhaseID.String())

	// SyncScheduler ran the goroutine inline; verify completion.
	stored, err := h.phaseRepo.FindByID(context.Background(), out.PhaseID)
	require.NoError(t, err)
	require.Equal(t, phase.PhaseStatusDone, stored.Status())
	require.NotNil(t, stored.Envelope())

	// Audit + events fired.
	require.Contains(t, h.audit.eventTypes(), "phase.started")
	require.Contains(t, h.audit.eventTypes(), "phase.completed")
	require.Contains(t, h.events.types(), "phase.started")
	require.Contains(t, h.events.types(), "governance.decision")
	require.Contains(t, h.events.types(), "agent.dispatched")
	require.Contains(t, h.events.types(), "agent.envelope.received")
	require.Contains(t, h.events.types(), "phase.completed")

	// Spawn governor balanced.
	require.Equal(t, 1, h.spawn.acquired)
	require.Equal(t, 1, h.spawn.released)
}

func TestRun_RejectsChangeNotFound(t *testing.T) {
	h := newHarness(t)
	missing, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5MIS")
	_, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:  missing,
		PhaseType: phase.PhaseSpec,
	})
	require.ErrorIs(t, err, outbound.ErrNotFound)
}

func TestRun_RejectsInvalidTransition(t *testing.T) {
	h := newHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	_, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:  cid,
		PhaseType: phase.PhaseApply, // not next-valid from proposal
	})
	require.ErrorIs(t, err, appphase.ErrInvalidTransition)
}

func TestRun_RejectsRunningPhase(t *testing.T) {
	h := newHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	pid, _ := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5RUN")
	running, _ := phase.New(pid, cid, phase.PhaseSpec, 3)
	_ = running.Start(time.Now())
	h.phaseRepo.running = running

	_, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:  cid,
		PhaseType: phase.PhaseSpec,
	})
	require.ErrorIs(t, err, appphase.ErrPhaseRunning)
}

func TestRun_PropagatesLockError(t *testing.T) {
	h := newHarness(t)
	h.phaseRepo.lockErr = errors.New("advisory lock failed")
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	_, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:  cid,
		PhaseType: phase.PhaseSpec,
	})
	require.Error(t, err)
}

func TestRun_GovernanceDeny_FailsPhase(t *testing.T) {
	h := newHarness(t)
	h.governance.decision = &outbound.GovernanceDecision{
		Decision: outbound.DecisionDeny,
		Reason:   "policy violation",
	}
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:  cid,
		PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err)
	stored, _ := h.phaseRepo.FindByID(context.Background(), out.PhaseID)
	require.Equal(t, phase.PhaseStatusBlocked, stored.Status())
	require.Contains(t, h.events.types(), "phase.failed")
}

func TestRun_GovernanceRequireApproval_PausesPhase(t *testing.T) {
	h := newHarness(t)
	h.governance.decision = &outbound.GovernanceDecision{
		Decision: outbound.DecisionRequireApproval,
		Approval: &outbound.ApprovalGate{URL: "/approve/abc"},
	}
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:  cid,
		PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err)
	stored, _ := h.phaseRepo.FindByID(context.Background(), out.PhaseID)
	require.Equal(t, phase.PhaseStatusRunning, stored.Status())
	require.Contains(t, h.events.types(), "approval.required")
}

func TestRun_DispatcherErrorFailsPhase(t *testing.T) {
	h := newHarness(t)
	h.dispatcher.err = errors.New("opencode crashed")
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:  cid,
		PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err)
	stored, _ := h.phaseRepo.FindByID(context.Background(), out.PhaseID)
	require.Equal(t, phase.PhaseStatusBlocked, stored.Status())
}

func TestRun_EnvelopeValidationError_FailsPhase(t *testing.T) {
	h := newHarness(t)
	h.dispatcher.result.EnvelopeRaw = []byte(`{"schema_version":"v0"}`)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:  cid,
		PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err)
	stored, _ := h.phaseRepo.FindByID(context.Background(), out.PhaseID)
	require.Equal(t, phase.PhaseStatusBlocked, stored.Status())
}

func TestRun_DoneBelowThreshold_Coerces(t *testing.T) {
	h := newHarness(t)
	// spec threshold is 0.8; agent claims DONE with 0.5 — validator coerces to DONE_WITH_CONCERNS.
	h.dispatcher.result.EnvelopeRaw = mustEnvelope(t, phase.PhaseSpec, envelope.StatusDone, 0.5)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, _ := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:  cid,
		PhaseType: phase.PhaseSpec,
	})
	stored, _ := h.phaseRepo.FindByID(context.Background(), out.PhaseID)
	require.Equal(t, phase.PhaseStatusDoneWithConcerns, stored.Status())
}

func TestRun_SpawnGovernorSaturatedFailsPhase(t *testing.T) {
	h := newHarness(t)
	h.spawn.acquireErr = errors.New("saturated")
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, _ := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:  cid,
		PhaseType: phase.PhaseSpec,
	})
	stored, _ := h.phaseRepo.FindByID(context.Background(), out.PhaseID)
	require.Equal(t, phase.PhaseStatusBlocked, stored.Status())
}

func TestRun_DefaultsRetryBudgetTo3(t *testing.T) {
	h := newHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, _ := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:  cid,
		PhaseType: phase.PhaseSpec,
		// RetryBudget intentionally 0
	})
	stored, _ := h.phaseRepo.FindByID(context.Background(), out.PhaseID)
	require.Equal(t, 3, stored.RetryBudget())
}

func TestRun_InvalidIDFromGenerator(t *testing.T) {
	h := newHarness(t)
	// Replace IDGen with a broken one.
	disp := h.dispatcher
	cr := h.changeRepo
	pr := newFakePhaseRepo()
	clock := shared.FixedClock(time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC))
	svc := appphase.New(appphase.Deps{
		ChangeRepo:  cr,
		PhaseRepo:   pr,
		SessionRepo: newFakeSessionRepo(),
		Governance:  h.governance,
		Memory:      h.memory,
		Dispatcher:  disp,
		SpawnGov:    h.spawn,
		Validator:   discipline.NewValidator(),
		IronLaw:     discipline.NewIronLawChecker(),
		Prompts:     discipline.NewPromptBuilder(),
		Audit:       h.audit,
		Events:      h.events,
		Clock:       clock,
		IDGen:       shared.FixedIDGenerator([]string{"not-a-ulid"}),
		Scheduler:   appphase.SyncScheduler,
	})

	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	_, err := svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	require.ErrorIs(t, err, ids.ErrInvalidID)
}

func TestGet_Found(t *testing.T) {
	h := newHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, _ := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	got, err := h.svc.Get(context.Background(), out.PhaseID)
	require.NoError(t, err)
	require.Equal(t, out.PhaseID, got.ID())
}

func TestGet_NotFound(t *testing.T) {
	h := newHarness(t)
	id, _ := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5MIS")
	_, err := h.svc.Get(context.Background(), id)
	require.ErrorIs(t, err, outbound.ErrNotFound)
}

func TestApprove_NotFound(t *testing.T) {
	h := newHarness(t)
	id, _ := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5MIS")
	err := h.svc.Approve(context.Background(), id, "alice", "ok")
	require.ErrorIs(t, err, outbound.ErrNotFound)
}

func TestApprove_RejectsTerminal(t *testing.T) {
	h := newHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, _ := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	err := h.svc.Approve(context.Background(), out.PhaseID, "alice", "ok")
	require.ErrorIs(t, err, appphase.ErrAlreadyTerminal)
}

func TestReject_NotFound(t *testing.T) {
	h := newHarness(t)
	id, _ := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5MIS")
	err := h.svc.Reject(context.Background(), id, "alice", "no")
	require.ErrorIs(t, err, outbound.ErrNotFound)
}

func TestReject_RejectsTerminal(t *testing.T) {
	h := newHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, _ := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	err := h.svc.Reject(context.Background(), out.PhaseID, "alice", "no")
	require.ErrorIs(t, err, appphase.ErrAlreadyTerminal)
}

func TestResume_NotFound(t *testing.T) {
	h := newHarness(t)
	id, _ := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5MIS")
	_, err := h.svc.Resume(context.Background(), id)
	require.ErrorIs(t, err, outbound.ErrNotFound)
}

func TestResume_RejectsTerminal(t *testing.T) {
	h := newHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, _ := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	_, err := h.svc.Resume(context.Background(), out.PhaseID)
	require.ErrorIs(t, err, appphase.ErrAlreadyTerminal)
}

func TestResume_FromInterrupted(t *testing.T) {
	h := newHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	pid, _ := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5INT")
	p, _ := phase.New(pid, cid, phase.PhaseSpec, 3)
	_ = p.Start(time.Now())
	_ = p.MarkInterrupted()
	_ = h.phaseRepo.Save(context.Background(), p)

	out, err := h.svc.Resume(context.Background(), pid)
	require.NoError(t, err)
	require.NotEmpty(t, out.PhaseID.String())
}

func TestRunHydrate_PhaseTypeMismatchInEnvelope(t *testing.T) {
	h := newHarness(t)
	// Dispatcher returns envelope claiming a different phase type.
	h.dispatcher.result.EnvelopeRaw = mustEnvelope(t, phase.PhaseDesign, envelope.StatusDone, 0.85)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, _ := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	stored, _ := h.phaseRepo.FindByID(context.Background(), out.PhaseID)
	require.Equal(t, phase.PhaseStatusBlocked, stored.Status())
}

func TestRun_AdvancesChangeOnDone(t *testing.T) {
	h := newHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	_, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err)
	c, _ := h.changeRepo.FindByID(context.Background(), cid)
	// CurrentPhase points at the last completed phase. After running
	// PhaseSpec from PhaseProposal, CurrentPhase becomes PhaseSpec.
	require.Equal(t, phase.PhaseSpec, c.CurrentPhase())
}

// Worktree types are referenced indirectly via session imports to ensure tests compile.
var _ = worktree.New

// --- additional coverage tests ---

// seedApprovalGate primes the audit log with an approval.required event
// for the given phase so Approve/Reject see the phase as gated. Production
// emits this event from the dispatch path when governance returns
// require_approval; tests bypass that path.
func seedApprovalGate(t *testing.T, h *harness, phaseID ids.PhaseID) {
	t.Helper()
	pid := phaseID
	require.NoError(t, h.audit.Append(context.Background(), outbound.AuditEvent{
		PhaseID:    &pid,
		EventType:  contract.EventApprovalRequired,
		OccurredAt: time.Now(),
	}))
}

func TestReject_HappyPath(t *testing.T) {
	h := newHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	pid, _ := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5INT")
	p, _ := phase.New(pid, cid, phase.PhaseSpec, 3)
	_ = p.Start(time.Now())
	_ = p.MarkInterrupted()
	_ = h.phaseRepo.Save(context.Background(), p)
	seedApprovalGate(t, h, pid)

	require.NoError(t, h.svc.Reject(context.Background(), pid, "alice", "bad spec"))

	stored, _ := h.phaseRepo.FindByID(context.Background(), pid)
	require.Equal(t, phase.PhaseStatusBlocked, stored.Status())
	require.Contains(t, h.events.types(), "approval.resolved")
}

func TestApprove_HappyPath(t *testing.T) {
	h := newHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	pid, _ := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5INT")
	p, _ := phase.New(pid, cid, phase.PhaseSpec, 3)
	_ = p.Start(time.Now())
	_ = p.MarkInterrupted()
	_ = h.phaseRepo.Save(context.Background(), p)
	seedApprovalGate(t, h, pid)

	require.NoError(t, h.svc.Approve(context.Background(), pid, "alice", "ok"))
	require.Contains(t, h.events.types(), "approval.resolved")
}

// --- Phase 3.8: required error code coverage (sophia-wire-v1 §9.2) ---

func TestApprove_ApproverRequired(t *testing.T) {
	h := newHarness(t)
	pid, _ := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5INT")
	err := h.svc.Approve(context.Background(), pid, "", "missing approver")
	require.ErrorIs(t, err, appphase.ErrApproverRequired)
}

func TestReject_ApproverRequired(t *testing.T) {
	h := newHarness(t)
	pid, _ := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5INT")
	err := h.svc.Reject(context.Background(), pid, "", "missing approver")
	require.ErrorIs(t, err, appphase.ErrApproverRequired)
}

func TestApprove_PhaseNotGated(t *testing.T) {
	h := newHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	pid, _ := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5INT")
	p, _ := phase.New(pid, cid, phase.PhaseSpec, 3)
	_ = p.Start(time.Now())
	_ = p.MarkInterrupted()
	_ = h.phaseRepo.Save(context.Background(), p)
	// No seedApprovalGate — phase has never been gated.
	err := h.svc.Approve(context.Background(), pid, "alice", "ok")
	require.ErrorIs(t, err, appphase.ErrPhaseNotGated)
}

func TestReject_PhaseNotGated(t *testing.T) {
	h := newHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	pid, _ := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5INT")
	p, _ := phase.New(pid, cid, phase.PhaseSpec, 3)
	_ = p.Start(time.Now())
	_ = p.MarkInterrupted()
	_ = h.phaseRepo.Save(context.Background(), p)
	err := h.svc.Reject(context.Background(), pid, "alice", "no")
	require.ErrorIs(t, err, appphase.ErrPhaseNotGated)
}

func TestApprove_GateAlreadyDecided(t *testing.T) {
	h := newHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	pid, _ := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5INT")
	p, _ := phase.New(pid, cid, phase.PhaseSpec, 3)
	_ = p.Start(time.Now())
	_ = p.MarkInterrupted()
	_ = h.phaseRepo.Save(context.Background(), p)
	seedApprovalGate(t, h, pid)
	require.NoError(t, h.svc.Approve(context.Background(), pid, "alice", "ok"))

	// Second approval call → gate already decided.
	err := h.svc.Approve(context.Background(), pid, "alice", "again")
	require.ErrorIs(t, err, appphase.ErrGateAlreadyDecided)
}

func TestReject_GateAlreadyDecided(t *testing.T) {
	h := newHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	pid, _ := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5INT")
	p, _ := phase.New(pid, cid, phase.PhaseSpec, 3)
	_ = p.Start(time.Now())
	_ = p.MarkInterrupted()
	_ = h.phaseRepo.Save(context.Background(), p)
	seedApprovalGate(t, h, pid)
	require.NoError(t, h.svc.Approve(context.Background(), pid, "alice", "ok"))

	// Now Reject → gate already decided (Approve resolved it first).
	err := h.svc.Reject(context.Background(), pid, "bob", "second guessing")
	require.ErrorIs(t, err, appphase.ErrGateAlreadyDecided)
}

// --- Phase 3.8: lifecycle event payload assertions (sophia-wire-v1 §5.3) ---

func TestRun_PhaseStartedPayloadShape(t *testing.T) {
	h := newHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err)
	// Find the phase.started event and assert required fields.
	var started inbound.Event
	for _, ev := range h.events.published {
		if ev.Type == contract.EventPhaseStarted {
			started = ev
			break
		}
	}
	require.Equal(t, contract.EventPhaseStarted, started.Type, "phase.started event must be published")
	payload, ok := started.Payload.(inbound.PhaseStartedPayload)
	require.True(t, ok, "phase.started payload must be inbound.PhaseStartedPayload, got %T", started.Payload)
	require.Equal(t, out.PhaseID.String(), payload.PhaseID)
	require.Equal(t, string(phase.PhaseSpec), payload.PhaseType)
	require.Equal(t, cid.String(), payload.ChangeID)
	require.False(t, payload.StartedAt.IsZero(), "phase.started must include started_at")
}

func TestRun_PhaseCompletedPayloadShape(t *testing.T) {
	h := newHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err)
	var completed inbound.Event
	for _, ev := range h.events.published {
		if ev.Type == contract.EventPhaseCompleted {
			completed = ev
		}
	}
	require.Equal(t, contract.EventPhaseCompleted, completed.Type, "phase.completed event must be published")
	payload, ok := completed.Payload.(inbound.PhaseCompletedPayload)
	require.True(t, ok, "phase.completed payload must be inbound.PhaseCompletedPayload, got %T", completed.Payload)
	require.Equal(t, out.PhaseID.String(), payload.PhaseID)
	require.Equal(t, string(phase.PhaseSpec), payload.PhaseType)
	require.False(t, payload.EndedAt.IsZero(), "phase.completed must include ended_at")
	require.NotZero(t, payload.Confidence, "phase.completed must include confidence")
}

func TestRun_PhaseFailedPayloadShape(t *testing.T) {
	h := newHarness(t)
	// Force dispatch failure → failPhase → phase.failed.
	h.dispatcher.err = errors.New("dispatch boom")
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, _ := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	var failed inbound.Event
	for _, ev := range h.events.published {
		if ev.Type == contract.EventPhaseFailed {
			failed = ev
		}
	}
	require.Equal(t, contract.EventPhaseFailed, failed.Type, "phase.failed event must be published")
	payload, ok := failed.Payload.(inbound.PhaseFailedPayload)
	require.True(t, ok, "phase.failed payload must be inbound.PhaseFailedPayload, got %T", failed.Payload)
	require.Equal(t, out.PhaseID.String(), payload.PhaseID)
	require.Equal(t, string(phase.PhaseSpec), payload.PhaseType)
	require.False(t, payload.EndedAt.IsZero())
	require.NotEmpty(t, payload.Error)
}

func TestRun_FallbackToMemoryWhenEnvelopeEmpty(t *testing.T) {
	h := newHarness(t)
	h.dispatcher.result.EnvelopeRaw = nil
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, _ := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	stored, _ := h.phaseRepo.FindByID(context.Background(), out.PhaseID)
	require.Equal(t, phase.PhaseStatusBlocked, stored.Status())
}

func TestBuildPriorContext_WithBundle(t *testing.T) {
	h := newHarness(t)
	h.memory.bundle = &outbound.ContextBundle{
		Sections: []outbound.ContextSection{
			{
				Type: "decisions",
				Records: []outbound.ContextRecord{
					{ID: "1", Type: "decision", Content: "use bcrypt", Score: 0.9},
				},
			},
		},
	}
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, _ := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	stored, _ := h.phaseRepo.FindByID(context.Background(), out.PhaseID)
	require.Equal(t, phase.PhaseStatusDone, stored.Status())
}

func TestRun_AllPhases_RoleAndActionMappings(t *testing.T) {
	for _, ptCase := range []struct {
		name        string
		current     phase.PhaseType
		next        phase.PhaseType
		conf        float64
		expectRole  session.AgentRole
	}{
		{"init->explore", phase.PhaseInit, phase.PhaseExplore, 0.6, session.RoleSDDExplore},
		{"explore->proposal", phase.PhaseExplore, phase.PhaseProposal, 0.8, session.RoleSDDProposal},
		{"proposal->design", phase.PhaseProposal, phase.PhaseDesign, 0.8, session.RoleSDDDesign},
		{"spec->tasks", phase.PhaseSpec, phase.PhaseTasks, 0.85, session.RoleSDDTasks},
		{"tasks->apply", phase.PhaseTasks, phase.PhaseApply, 0.85, session.RoleTeamLead},
		{"apply->verify", phase.PhaseApply, phase.PhaseVerify, 0.95, session.RoleSDDVerify},
		{"verify->archive", phase.PhaseVerify, phase.PhaseArchive, 0.95, session.RoleSDDArchive},
	} {
		t.Run(ptCase.name, func(t *testing.T) {
			h := newHarness(t)
			cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
			h.changeRepo.byID[cid.String()] = domainchange.Hydrate(
				cid, "feat-x", "proj",
				domainchange.StatusActive, ptCase.current,
				domainchange.ArtifactStoreMemoryEngine, "main",
				time.Now(), time.Now(),
			)
			h.dispatcher.result.EnvelopeRaw = mustEnvelope(t, ptCase.next, envelope.StatusDone, ptCase.conf)

			out, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
				ChangeID: cid, PhaseType: ptCase.next,
			})
			require.NoError(t, err)
			require.NotNil(t, out)

			stored, _ := h.phaseRepo.FindByID(context.Background(), out.PhaseID)
			require.NotNil(t, stored)

			for _, s := range h.sessRepo.byID {
				require.Equal(t, ptCase.expectRole, s.Role(), "phase=%s", ptCase.next)
				break
			}
		})
	}
}

func TestEventTypeForStatus_AllStatuses(t *testing.T) {
	for _, c := range []struct {
		name   string
		status envelope.Status
		conf   float64
		want   string
	}{
		{"done", envelope.StatusDone, 0.85, "phase.completed"},
		{"concerns", envelope.StatusDoneWithConcerns, 0.5, "phase.completed_with_concerns"},
		{"blocked", envelope.StatusBlocked, 0.0, "phase.failed"},
		{"needs_context", envelope.StatusNeedsContext, 0.3, "phase.needs_context"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
			h.dispatcher.result.EnvelopeRaw = mustEnvelope(t, phase.PhaseSpec, c.status, c.conf)
			_, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
				ChangeID: cid, PhaseType: phase.PhaseSpec,
			})
			require.NoError(t, err)
			require.Contains(t, h.events.types(), c.want)
		})
	}
}
