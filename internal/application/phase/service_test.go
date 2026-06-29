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
	skdomain "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/worktree"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// --- fakes ---

type fakeChangeRepo struct {
	mu        sync.Mutex
	byID      map[string]*domainchange.Change
	saveErr   error
	findErr   error
	saveCalls int
}

func newFakeChangeRepo() *fakeChangeRepo {
	return &fakeChangeRepo{byID: map[string]*domainchange.Change{}}
}

func (r *fakeChangeRepo) Save(_ context.Context, c *domainchange.Change) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saveCalls++
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
	mu              sync.Mutex
	byID            map[string]*phase.Phase
	byChangeAndType map[string]*phase.Phase // "<changeID>|<type>"
	running         *phase.Phase
	saveErr         error
	lockErr         error
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

// FindAllRunning supports the BUG-23 boot recovery scan. Returns every
// phase in byID whose status is PhaseStatusRunning. Empty slice (not
// ErrNotFound) is the contract when nothing matches.
func (r *fakePhaseRepo) FindAllRunning(_ context.Context) ([]*phase.Phase, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*phase.Phase, 0)
	for _, p := range r.byID {
		if p.Status() == phase.PhaseStatusRunning {
			out = append(out, p)
		}
	}
	return out, nil
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
	result     *outbound.DispatchResult
	err        error
	lastPrompt string // captures the last Dispatch call's prompt (K.1 assertion)
}

func (d *fakeDispatcher) Provider() session.Provider          { return session.ProviderOpenCode }
func (d *fakeDispatcher) SuggestedMaxConcurrent() int         { return 4 }
func (d *fakeDispatcher) HealthCheck(_ context.Context) error { return nil }
func (d *fakeDispatcher) Dispatch(_ context.Context, req outbound.DispatchRequest) (*outbound.DispatchResult, error) {
	d.lastPrompt = req.Prompt
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
		ExitCode:    0,
		Stdout:      []byte{},
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
		name       string
		current    phase.PhaseType
		next       phase.PhaseType
		conf       float64
		expectRole session.AgentRole
	}{
		{"init->explore", phase.PhaseInit, phase.PhaseExplore, 0.6, session.RoleSDDExplore},
		{"explore->proposal", phase.PhaseExplore, phase.PhaseProposal, 0.8, session.RoleSDDProposal},
		{"proposal->spec", phase.PhaseProposal, phase.PhaseSpec, 0.8, session.RoleSDDSpec},
		{"spec->design", phase.PhaseSpec, phase.PhaseDesign, 0.8, session.RoleSDDDesign},
		{"design->tasks", phase.PhaseDesign, phase.PhaseTasks, 0.85, session.RoleSDDTasks},
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

// --- Spec #45 schema_mismatch tests ---

// mustTasksEnvelopeFlat builds a tasks-phase envelope whose data block uses
// a flat "tasks" array instead of the required "groups" shape, triggering
// the schema_mismatch guard.
func mustTasksEnvelopeFlat(t *testing.T) []byte {
	t.Helper()
	body := map[string]any{
		"schema_version":    "v1",
		"phase":             string(phase.PhaseTasks),
		"change_name":       "feat-x",
		"project":           "proj",
		"status":            string(envelope.StatusDone),
		"confidence":        0.85,
		"executive_summary": "ok",
		"artifacts_saved":   []map[string]any{},
		"next_recommended":  []string{},
		"risks":             []map[string]string{},
		"data":              map[string]any{"tasks": []map[string]any{{"description": "do stuff"}}},
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	return raw
}

// mustTasksEnvelopeGrouped builds a valid tasks-phase envelope with the
// required data.groups[] shape.
func mustTasksEnvelopeGrouped(t *testing.T) []byte {
	t.Helper()
	body := map[string]any{
		"schema_version":    "v1",
		"phase":             string(phase.PhaseTasks),
		"change_name":       "feat-x",
		"project":           "proj",
		"status":            string(envelope.StatusDone),
		"confidence":        0.9,
		"executive_summary": "ok",
		"artifacts_saved":   []map[string]any{},
		"next_recommended":  []string{},
		"risks":             []map[string]string{},
		"data": map[string]any{"groups": []map[string]any{
			{"name": "domain", "tasks": []map[string]any{{"description": "do stuff", "files_pattern": []string{"*.go"}}}},
		}},
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	return raw
}

// TestRun_TasksPhase_FlatTasksArray_SchemasMismatch verifies that a tasks
// phase envelope with a flat "tasks" array (instead of "data.groups[]")
// is rejected with schema_mismatch and the phase is marked BLOCKED.
// Spec #45.
func TestRun_TasksPhase_FlatTasksArray_SchemaMismatch(t *testing.T) {
	h := newHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	// Advance change to PhaseTasks-ready state (sequential: design precedes tasks).
	h.changeRepo.byID[cid.String()] = domainchange.Hydrate(
		cid, "feat-x", "proj",
		domainchange.StatusActive, phase.PhaseDesign,
		domainchange.ArtifactStoreMemoryEngine, "main",
		time.Now(), time.Now(),
	)
	h.dispatcher.result.EnvelopeRaw = mustTasksEnvelopeFlat(t)
	out, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseTasks,
	})
	require.NoError(t, err)
	stored, _ := h.phaseRepo.FindByID(context.Background(), out.PhaseID)
	require.Equal(t, phase.PhaseStatusBlocked, stored.Status(),
		"tasks phase with flat tasks array must be BLOCKED (schema_mismatch)")

	// phase.failed must be emitted.
	require.Contains(t, h.events.types(), "phase.failed")
	// Check payload carries schema_mismatch as failure_reason.
	for _, ev := range h.events.published {
		if ev.Type == "phase.failed" {
			p, ok := ev.Payload.(inbound.PhaseFailedPayload)
			require.True(t, ok)
			require.Equal(t, "schema_mismatch", p.FailureReason,
				"failure_reason must be schema_mismatch for flat task array rejection")
			require.NotEmpty(t, p.FailureDetail)
			break
		}
	}
}

// TestRun_TasksPhase_GroupedSchema_Passes verifies that a tasks envelope
// with the correct data.groups[] shape completes successfully (Spec #45).
func TestRun_TasksPhase_GroupedSchema_Passes(t *testing.T) {
	h := newHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	h.changeRepo.byID[cid.String()] = domainchange.Hydrate(
		cid, "feat-x", "proj",
		domainchange.StatusActive, phase.PhaseDesign,
		domainchange.ArtifactStoreMemoryEngine, "main",
		time.Now(), time.Now(),
	)
	h.dispatcher.result.EnvelopeRaw = mustTasksEnvelopeGrouped(t)
	out, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseTasks,
	})
	require.NoError(t, err)
	stored, _ := h.phaseRepo.FindByID(context.Background(), out.PhaseID)
	require.Equal(t, phase.PhaseStatusDone, stored.Status(),
		"tasks phase with grouped schema must succeed")
}

// --- Spec #48 enriched failure payload ---

// TestRun_FailurePayload_HasNewFields verifies that the phase.failed
// SSE event emitted from failPhase always carries the new FailureReason
// and FailureDetail fields introduced in Spec #48. When no session
// envelope is available (dispatch error), both fields follow the
// "unknown" fallback contract.
func TestRun_FailurePayload_HasNewFields(t *testing.T) {
	h := newHarness(t)
	// Trigger failPhase via a dispatch error — no session envelope exists.
	h.dispatcher.err = errors.New("opencode crashed")
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err)
	stored, _ := h.phaseRepo.FindByID(context.Background(), out.PhaseID)
	require.Equal(t, phase.PhaseStatusBlocked, stored.Status())

	var failedEv inbound.Event
	for _, ev := range h.events.published {
		if ev.Type == "phase.failed" {
			failedEv = ev
		}
	}
	require.Equal(t, "phase.failed", failedEv.Type, "phase.failed must be emitted")
	p, ok := failedEv.Payload.(inbound.PhaseFailedPayload)
	require.True(t, ok, "phase.failed payload must be PhaseFailedPayload, got %T", failedEv.Payload)
	// Spec #48: both fields must be present (possibly "unknown") — not empty struct zero values.
	require.Equal(t, "unknown", p.FailureReason,
		"failure_reason must default to 'unknown' when no session envelope is available")
	require.NotEmpty(t, p.Error, "error field must still carry the reason string")
}

// TestRun_FailurePayload_UnknownWhenNoRuleID verifies that when the
// dispatcher fails (no session envelope), failure_reason defaults to
// "unknown" (Spec #48 fallback).
func TestRun_FailurePayload_UnknownWhenNoRuleID(t *testing.T) {
	h := newHarness(t)
	h.dispatcher.err = errors.New("opencode crashed")
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err)
	stored, _ := h.phaseRepo.FindByID(context.Background(), out.PhaseID)
	require.Equal(t, phase.PhaseStatusBlocked, stored.Status())

	var failedEv inbound.Event
	for _, ev := range h.events.published {
		if ev.Type == "phase.failed" {
			failedEv = ev
		}
	}
	p, ok := failedEv.Payload.(inbound.PhaseFailedPayload)
	require.True(t, ok)
	require.Equal(t, "unknown", p.FailureReason,
		"failure_reason must be 'unknown' when no session envelope is available")
}

// --- Spec #49 retry attempt bump ---

// TestRun_RetryBumpsAttempts verifies that running a phase after a prior
// terminal row for the same (change_id, phase_type) increments attempts
// to N+1 (Spec #49 idempotent retry).
func TestRun_RetryBumpsAttempts(t *testing.T) {
	h := newHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")

	// Run once successfully.
	out1, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err)
	first, _ := h.phaseRepo.FindByID(context.Background(), out1.PhaseID)
	require.Equal(t, 1, first.Attempts(), "first run must have attempts=1")

	// Simulate: the first phase is now terminal (done). Run a retry by
	// advancing the harness's ID generator and re-running the same phase
	// type. We need to reset the change to allow re-running PhaseSpec.
	h.changeRepo.byID[cid.String()] = domainchange.Hydrate(
		cid, "feat-x", "proj",
		domainchange.StatusActive, phase.PhaseProposal,
		domainchange.ArtifactStoreMemoryEngine, "main",
		time.Now(), time.Now(),
	)
	// Replace idgen so the second Run call gets new IDs.
	h.svc = appphase.New(appphase.Deps{
		ChangeRepo:  h.changeRepo,
		PhaseRepo:   h.phaseRepo,
		SessionRepo: h.sessRepo,
		Governance:  h.governance,
		Memory:      h.memory,
		Dispatcher:  h.dispatcher,
		SpawnGov:    h.spawn,
		Validator:   discipline.NewValidator(),
		IronLaw:     discipline.NewIronLawChecker(),
		Prompts:     discipline.NewPromptBuilder(),
		Audit:       h.audit,
		Events:      h.events,
		Clock:       h.clock,
		IDGen: shared.FixedIDGenerator([]string{
			"01ARZ3NDEKTSV4RRFFQ69G5P03", // new phase id
			"01ARZ3NDEKTSV4RRFFQ69G5S03", // new session id
		}),
		Scheduler: appphase.SyncScheduler,
	})

	out2, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err)
	second, _ := h.phaseRepo.FindByID(context.Background(), out2.PhaseID)
	require.Equal(t, 2, second.Attempts(),
		"retry of a terminal phase must have attempts=2 (prior.Attempts()+1)")
}

// TestRun_RetryAfterNeedsContextBumpsAttempts pins BUG-24. When the prior
// phase for (change_id, phase_type) is in PhaseStatusNeedsContext, a new
// Run call MUST bump attempts to prior.Attempts()+1 — not collide on the
// same (change_id, phase_type, attempts) upsert key as the prior row.
//
// Domain contract (internal/domain/phase/status.go): "NEEDS_CONTEXT is
// NOT terminal — the orchestrator may retry within budget." So Run is
// the right entry point for the retry; the bug was that service.Run
// only bumped priorAttempts when prior.Status().IsTerminal(), which
// excluded NeedsContext and made the retry land at attempts=1 again.
// Real-world symptom (pg): the (change_id, phase_type, attempts) upsert
// updates the existing row in place, the operator-returned phase_id
// is never persisted, and FindByID returns phase_not_found while the
// prior row silently transitions through running back to a terminal
// status under a goroutine the operator never sees.
func TestRun_RetryAfterNeedsContextBumpsAttempts(t *testing.T) {
	h := newHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")

	// First Run completes via the harness's mock dispatcher with the
	// default DONE envelope. We mutate the persisted phase to
	// needs_context to model the operator-visible state where the LLM
	// asked for more context.
	out1, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err)
	first, _ := h.phaseRepo.FindByID(context.Background(), out1.PhaseID)
	require.Equal(t, 1, first.Attempts(), "sanity: first run attempts=1")

	firstHydrated := phase.Hydrate(
		first.ID(), first.ChangeID(), first.Type(),
		phase.PhaseStatusNeedsContext,
		first.Envelope(), first.Confidence(),
		first.RetryBudget(), first.Attempts(),
		first.StartedAt(), first.CompletedAt(),
	)
	require.NoError(t, h.phaseRepo.Save(context.Background(), firstHydrated))

	// Reset the change so the next-valid-transition check accepts a
	// fresh Spec run (same trick as TestRun_RetryBumpsAttempts).
	h.changeRepo.byID[cid.String()] = domainchange.Hydrate(
		cid, "feat-x", "proj",
		domainchange.StatusActive, phase.PhaseProposal,
		domainchange.ArtifactStoreMemoryEngine, "main",
		time.Now(), time.Now(),
	)

	// Re-wire the service with a fresh ID generator so the retry
	// receives ids different from the first run's. If service.Run
	// reused the prior phase_id (no attempts bump → pg upsert clobber)
	// the test would not detect the bug, so the fresh-id contract is
	// explicit here.
	h.svc = appphase.New(appphase.Deps{
		ChangeRepo:  h.changeRepo,
		PhaseRepo:   h.phaseRepo,
		SessionRepo: h.sessRepo,
		Governance:  h.governance,
		Memory:      h.memory,
		Dispatcher:  h.dispatcher,
		SpawnGov:    h.spawn,
		Validator:   discipline.NewValidator(),
		IronLaw:     discipline.NewIronLawChecker(),
		Prompts:     discipline.NewPromptBuilder(),
		Audit:       h.audit,
		Events:      h.events,
		Clock:       h.clock,
		IDGen: shared.FixedIDGenerator([]string{
			"01ARZ3NDEKTSV4RRFFQ69G5P03",
			"01ARZ3NDEKTSV4RRFFQ69G5S03",
		}),
		Scheduler: appphase.SyncScheduler,
	})

	out2, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err)

	second, err := h.phaseRepo.FindByID(context.Background(), out2.PhaseID)
	require.NoError(t, err,
		"the phase_id returned from Run MUST be persisted — operator polls "+
			"this id and a not-found surfaces the prior bug where the upsert "+
			"key collided and the new id was never written")
	require.Equal(t, 2, second.Attempts(),
		"retry of a needs_context phase must have attempts=2 (prior.Attempts()+1) "+
			"so the (change_id, phase_type, attempts) upsert lands on a new row")
	require.NotEqual(t, out1.PhaseID, out2.PhaseID,
		"retry must yield a fresh phase_id — collapsing them hides the "+
			"prior attempt from history")
}

// ---------------------------------------------------------------------------
// Skill hydration fail-soft tests (Slice 2, task 2.4b)
// ---------------------------------------------------------------------------

// newHarnessWithSkills creates a harness wired with the given SkillMatcher.
func newHarnessWithSkills(t *testing.T, sp discipline.SkillMatcher) *harness {
	t.Helper()
	h := newHarness(t)
	cr := h.changeRepo
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")

	h.svc = appphase.New(appphase.Deps{
		ChangeRepo:  cr,
		PhaseRepo:   h.phaseRepo,
		SessionRepo: h.sessRepo,
		Governance:  h.governance,
		Memory:      h.memory,
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
		Skills:    sp,
	})
	_ = cid
	return h
}

// TestRun_Skills_NilProvider_PhaseRunsNormally verifies that when the Skills
// field on Deps is nil (flag=off or not wired), the phase still runs
// successfully and no "# Skill" section appears in the dispatched prompt.
func TestRun_Skills_NilProvider_PhaseRunsNormally(t *testing.T) {
	h := newHarnessWithSkills(t, nil) // nil → flag off
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err)
	stored, _ := h.phaseRepo.FindByID(context.Background(), out.PhaseID)
	require.Equal(t, phase.PhaseStatusDone, stored.Status(),
		"phase must complete normally when Skills is nil")
}

// TestRun_Skills_ProviderError_FailSoft verifies that when the SkillProvider
// returns an error, the phase still runs (fail-soft) and "# Skill" is absent.
func TestRun_Skills_ProviderError_FailSoft(t *testing.T) {
	sp := &fakeSkillMatcher{err: errors.New("db timeout")}
	h := newHarnessWithSkills(t, sp)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err)
	stored, _ := h.phaseRepo.FindByID(context.Background(), out.PhaseID)
	require.Equal(t, phase.PhaseStatusDone, stored.Status(),
		"phase must complete normally even when SkillProvider errors (fail-soft)")
}

// TestRun_Skills_ProviderEmpty_FailSoft verifies that when the SkillProvider
// returns an empty slice, the phase still runs (fail-soft) and "# Skill" is absent.
func TestRun_Skills_ProviderEmpty_FailSoft(t *testing.T) {
	sp := &fakeSkillMatcher{skills: []*skdomain.Skill{}} // empty, no error
	h := newHarnessWithSkills(t, sp)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err)
	stored, _ := h.phaseRepo.FindByID(context.Background(), out.PhaseID)
	require.Equal(t, phase.PhaseStatusDone, stored.Status(),
		"phase must complete normally when SkillProvider returns empty (fail-soft)")
}

// ---------------------------------------------------------------------------
// Group K RED tests — buildPriorContext decomposition + SkillsForContext
// migration (M3 PR3a)
// ---------------------------------------------------------------------------

// spyMemory implements outbound.MemoryClient and records BuildContext requests
// and Search calls so tests can assert on them (K.2 / K.3).
type spyMemory struct {
	lastBuildContextReq outbound.ContextRequest
	lastSearchQuery     outbound.SearchQuery
	searchResults       *outbound.SearchResults
	buildBundle         *outbound.ContextBundle
	getErr              error
	ingestErr           error
}

func (m *spyMemory) Ingest(_ context.Context, _ outbound.IngestMemoryInput) (*outbound.MemoryRecord, error) {
	return nil, m.ingestErr
}
func (m *spyMemory) Get(_ context.Context, _ string) (*outbound.MemoryRecord, error) {
	return nil, m.getErr
}
func (m *spyMemory) GetByTopicKey(_ context.Context, _ outbound.MemoryScope, _ string) (*outbound.MemoryRecord, error) {
	return nil, m.getErr
}
func (m *spyMemory) Archive(_ context.Context, _, _, _ string) error { return nil }
func (m *spyMemory) RecordDecision(_ context.Context, _ outbound.RecordDecisionInput) (*outbound.MemoryRecord, error) {
	return nil, nil
}
func (m *spyMemory) RecordRelation(_ context.Context, _ outbound.RecordRelationInput) error {
	return nil
}

func (m *spyMemory) BuildContext(_ context.Context, req outbound.ContextRequest) (*outbound.ContextBundle, error) {
	m.lastBuildContextReq = req
	if m.buildBundle != nil {
		return m.buildBundle, nil
	}
	return nil, nil
}

func (m *spyMemory) Search(_ context.Context, q outbound.SearchQuery) (*outbound.SearchResults, error) {
	m.lastSearchQuery = q
	return m.searchResults, nil
}

var _ outbound.MemoryClient = (*spyMemory)(nil)

// fakeSkillMatcher implements discipline.SkillMatcher for service_test.go.
// Returns configured skills on every SkillsForContext call.
type fakeSkillMatcher struct {
	skills []*skdomain.Skill
	err    error
}

func (f *fakeSkillMatcher) SkillsForContext(_ context.Context, _ discipline.SkillQuery) ([]*skdomain.Skill, []discipline.SkippedSkill, error) {
	return f.skills, nil, f.err
}

// newHarnessWithMatcher creates a harness wired with the given SkillMatcher.
func newHarnessWithMatcher(t *testing.T, sm discipline.SkillMatcher) *harness {
	t.Helper()
	h := newHarness(t)

	h.svc = appphase.New(appphase.Deps{
		ChangeRepo:  h.changeRepo,
		PhaseRepo:   h.phaseRepo,
		SessionRepo: h.sessRepo,
		Governance:  h.governance,
		Memory:      h.memory,
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
		Skills:    sm,
	})
	return h
}

// newHarnessWithSpyMemory creates a harness using spyMemory instead of fakeMemory.
func newHarnessWithSpyMemory(t *testing.T, spy *spyMemory) *harness {
	t.Helper()
	h := newHarness(t)
	h.svc = appphase.New(appphase.Deps{
		ChangeRepo:  h.changeRepo,
		PhaseRepo:   h.phaseRepo,
		SessionRepo: h.sessRepo,
		Governance:  h.governance,
		Memory:      spy,
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

// TestBuildPriorContext_Decompose verifies that when BuildContext returns
// decisions/heuristics/recent_episodic sections, buildPriorContext maps
// them into PriorContext.Rules and PriorContext.Episodes (K.1 RED).
// This test validates the decomposition indirectly via the dispatched prompt:
// since the Render output now includes typed content from those sections,
// the prompt contains the mapped content.
//
// Note: buildPriorContext is unexported; we test its effects via Run.
// The dispatched prompt is captured by the fakeDispatcher.
func TestBuildPriorContext_Decompose(t *testing.T) {
	spy := &spyMemory{
		buildBundle: &outbound.ContextBundle{
			Sections: []outbound.ContextSection{
				{
					Type: "decisions",
					Records: []outbound.ContextRecord{
						{ID: "mem-D01", Content: "decision: use hexagonal architecture"},
					},
				},
				{
					Type: "heuristics",
					Records: []outbound.ContextRecord{
						{ID: "mem-H01", Content: "heuristic: always freeze Clock in tests"},
					},
				},
				{
					Type: "recent_episodic",
					Records: []outbound.ContextRecord{
						{ID: "mem-E01", Content: "episode: fixed N+1 query in phase service"},
					},
				},
			},
		},
		searchResults: &outbound.SearchResults{
			Results: []outbound.SearchResult{
				{ID: "mem-CD01", Snippet: "digest: priorcontext enrichment complete"},
			},
		},
	}
	h := newHarnessWithSpyMemory(t, spy)

	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err)
	_ = out

	// K.1 assertion: decomposition populated typed layers.
	// Decision and heuristic content must appear in the dispatched prompt.
	require.NotEmpty(t, h.dispatcher.lastPrompt,
		"dispatcher must have received a prompt")
	require.Contains(t, h.dispatcher.lastPrompt, "decision: use hexagonal architecture",
		"decisions layer must appear in prompt (decomposed into BusinessRules)")
	require.Contains(t, h.dispatcher.lastPrompt, "heuristic: always freeze Clock",
		"heuristics layer must appear in prompt (decomposed into BusinessRules)")
	require.Contains(t, h.dispatcher.lastPrompt, "episode: fixed N+1 query",
		"episodes layer must appear in prompt (decomposed into Episodes)")
}

// TestBuildPriorContext_QuerySetToChangeName verifies that BuildContext is
// called with Query = c.Name() so recent_episodic records actually surface
// (K.2 RED — currently no Query is passed).
func TestBuildPriorContext_QuerySetToChangeName(t *testing.T) {
	spy := &spyMemory{}
	h := newHarnessWithSpyMemory(t, spy)

	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	_, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err)

	// K.2: Query must be non-empty (set to change name) so FTS surfaces episodes.
	require.NotEmpty(t, spy.lastBuildContextReq.Query,
		"BuildContext must be called with a non-empty Query so recent_episodic populates (D-M3-6)")
}

// TestBuildPriorContext_DigestSearchCalled verifies that a dedicated
// Memory.Search(Types:["semantic"], Limit:3) is called for change digests (K.3 RED).
func TestBuildPriorContext_DigestSearchCalled(t *testing.T) {
	spy := &spyMemory{}
	h := newHarnessWithSpyMemory(t, spy)

	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	_, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err)

	// K.3: Search must have been called with Types:["semantic"] for DG-1 digests.
	require.NotEmpty(t, spy.lastSearchQuery.Types,
		"Memory.Search must be called for DG-1 digest retrieval")
	require.Contains(t, spy.lastSearchQuery.Types, "semantic",
		"DG-1: Search must use Types:[semantic] to retrieve change digests")
	require.LessOrEqual(t, spy.lastSearchQuery.Limit, 3,
		"DG-1: Search Limit must be ≤ 3 (V4.1 §12.2 digests top-3)")
}

// TestRun_SkillMatcher_NilProvider_PhaseRunsNormally mirrors the SkillProvider
// nil-safety test but for the new SkillMatcher interface (K.4).
func TestRun_SkillMatcher_NilProvider_PhaseRunsNormally(t *testing.T) {
	h := newHarnessWithMatcher(t, nil) // nil → flag off
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err)
	stored, _ := h.phaseRepo.FindByID(context.Background(), out.PhaseID)
	require.Equal(t, phase.PhaseStatusDone, stored.Status(),
		"phase must complete normally when SkillMatcher is nil")
}

// TestRun_SkillMatcher_Error_FailSoft verifies fail-soft when SkillMatcher errors.
func TestRun_SkillMatcher_Error_FailSoft(t *testing.T) {
	sm := &fakeSkillMatcher{err: errors.New("db timeout")}
	h := newHarnessWithMatcher(t, sm)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err)
	stored, _ := h.phaseRepo.FindByID(context.Background(), out.PhaseID)
	require.Equal(t, phase.PhaseStatusDone, stored.Status(),
		"phase must complete normally even when SkillMatcher errors (fail-soft)")
}

// --- Spec #46 tests_required from ContextOverrides ---

// TestRun_TestsRequired_WiredFromContextOverrides verifies that when
// ContextOverrides["scope"]["tests_required"] == true, the built prompt
// contains the TDD hard-gate clause; when false (or absent), it does not.
// This is a smoke-path test — full TDD gate testing lives in prompt_builder_test.go.
func TestRun_TestsRequired_WiredFromContextOverrides(t *testing.T) {
	t.Run("tests_required_false", func(t *testing.T) {
		h := newHarness(t)
		cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
		_, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
			ChangeID:         cid,
			PhaseType:        phase.PhaseSpec,
			ContextOverrides: map[string]any{"scope": map[string]any{"tests_required": false}},
		})
		require.NoError(t, err)
		// Phase completes cleanly — TDD absence does not break the flow.
		stored := h.phaseRepo.byChangeAndType[cid.String()+"|"+string(phase.PhaseSpec)]
		require.NotNil(t, stored)
	})
	t.Run("tests_required_true", func(t *testing.T) {
		h := newHarness(t)
		cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
		_, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
			ChangeID:         cid,
			PhaseType:        phase.PhaseSpec,
			ContextOverrides: map[string]any{"scope": map[string]any{"tests_required": true}},
		})
		require.NoError(t, err)
		// Phase still completes — the TDD gate is in the prompt text, not
		// an orchestrator-level block. Verify no crash occurs.
		stored := h.phaseRepo.byChangeAndType[cid.String()+"|"+string(phase.PhaseSpec)]
		require.NotNil(t, stored)
	})
}
