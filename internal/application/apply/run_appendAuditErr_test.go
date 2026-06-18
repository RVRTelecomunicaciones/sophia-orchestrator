package apply

// TestRunService_appendAuditErr_emitsEvent verifies that appendAuditErr
// calls AuditLog.Append with an event whose EventType is "apply.error.discarded"
// and whose Payload contains the "operation" key.
//
// This is an internal-package (white-box) test because appendAuditErr is unexported.
// TDD RED step: method does not exist yet — compilation fails, proving RED.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	applyDomain "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// internalFakeAudit is a local capture helper that does NOT require the
// external test package — keeps this file self-contained.
type internalFakeAudit struct {
	events []outbound.AuditEvent
}

func (a *internalFakeAudit) Append(_ context.Context, e outbound.AuditEvent) error {
	a.events = append(a.events, e)
	return nil
}
func (a *internalFakeAudit) HasEventForPhase(_ context.Context, _ ids.PhaseID, _ string) (bool, error) {
	return false, nil
}

func TestRunService_appendAuditErr_emitsEvent(t *testing.T) {
	audit := &internalFakeAudit{}

	clock := shared.FixedClock(time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC))
	idGen := shared.NewSystemIDGenerator(clock)

	// Minimal wiring — only Audit and Clock are used by appendAuditErr.
	s := NewRun(RunDeps{
		BoardRepo:   newMinimalBoardRepo(),
		SessionRepo: newMinimalSessionRepo(),
		Runtime:     &minimalRuntime{},
		Dispatcher:  &minimalDispatcher{},
		SpawnGov:    &minimalSpawnGov{},
		Validator:   discipline.NewValidator(),
		Prompts:     discipline.NewPromptBuilder(),
		Audit:       audit,
		Events:      &minimalEvents{},
		Memory:      &minimalMemory{},
		Clock:       clock,
		IDGen:       idGen,
		Config: RunConfig{
			MaxParallelGroups:             1,
			MaxParallelImplementsPerGroup: 1,
			DepWaitTimeout:                1,
			DispatchTimeoutMS:             1000,
		},
	})

	cid, err := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	require.NoError(t, err)
	pid, err := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5P01")
	require.NoError(t, err)

	s.appendAuditErr(context.Background(), cid, pid, "group.Fail", errTest)

	require.Len(t, audit.events, 1)
	got := audit.events[0]
	assert.Equal(t, "apply.error.discarded", got.EventType)
	require.NotNil(t, got.ChangeID)
	assert.Equal(t, cid, *got.ChangeID)
	require.NotNil(t, got.PhaseID)
	assert.Equal(t, pid, *got.PhaseID)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(got.Payload, &payload))
	assert.Equal(t, "group.Fail", payload["operation"])
	assert.NotEmpty(t, payload["error"])
}

// errTest is a sentinel error for the test.
var errTest = &sentinelErr{"sentinel test error"}

type sentinelErr struct{ msg string }

func (e *sentinelErr) Error() string { return e.msg }

// --- minimal stubs for NewRun wiring (internal test package only) ---

type minimalBoardRepo struct{}

func newMinimalBoardRepo() *minimalBoardRepo { return &minimalBoardRepo{} }

func (r *minimalBoardRepo) SaveBoard(_ context.Context, _ *applyDomain.Board) error { return nil }
func (r *minimalBoardRepo) FindBoardByPhaseID(_ context.Context, _ ids.PhaseID) (*applyDomain.Board, error) {
	return nil, outbound.ErrNotFound
}
func (r *minimalBoardRepo) SaveGroup(_ context.Context, _ *applyDomain.Group) error { return nil }
func (r *minimalBoardRepo) SaveTask(_ context.Context, _ *applyDomain.Task) error   { return nil }
func (r *minimalBoardRepo) FindTaskByID(_ context.Context, _ ids.TaskID) (*applyDomain.Task, error) {
	return nil, outbound.ErrNotFound
}
func (r *minimalBoardRepo) ClaimTask(_ context.Context, _ ids.TaskID, _ ids.SessionID) (bool, error) {
	return false, nil
}

type minimalSessionRepo struct{}

func newMinimalSessionRepo() *minimalSessionRepo { return &minimalSessionRepo{} }

func (r *minimalSessionRepo) Save(_ context.Context, _ *session.Session) error { return nil }
func (r *minimalSessionRepo) FindByID(_ context.Context, _ ids.SessionID) (*session.Session, error) {
	return nil, outbound.ErrNotFound
}
func (r *minimalSessionRepo) FindByPhaseID(_ context.Context, _ ids.PhaseID) ([]*session.Session, error) {
	return nil, nil
}

type minimalRuntime struct{}

func (r *minimalRuntime) Execute(_ context.Context, _ outbound.ExecutionRequest) (*outbound.ExecutionReceipt, error) {
	return &outbound.ExecutionReceipt{Status: outbound.ReceiptSuccess}, nil
}

type minimalDispatcher struct{}

func (d *minimalDispatcher) Provider() session.Provider          { return session.ProviderOpenCode }
func (d *minimalDispatcher) SuggestedMaxConcurrent() int         { return 1 }
func (d *minimalDispatcher) HealthCheck(_ context.Context) error { return nil }
func (d *minimalDispatcher) Dispatch(_ context.Context, _ outbound.DispatchRequest) (*outbound.DispatchResult, error) {
	return nil, outbound.ErrDispatchFailed
}

type minimalSpawnGov struct{}

func (g *minimalSpawnGov) Acquire(_ context.Context) error { return nil }
func (g *minimalSpawnGov) Release(_ context.Context) error { return nil }

type minimalEvents struct{}

func (e *minimalEvents) Subscribe(_ context.Context, _ ids.PhaseID) (<-chan inbound.Event, func(), error) {
	ch := make(chan inbound.Event)
	return ch, func() { close(ch) }, nil
}
func (e *minimalEvents) Publish(_ context.Context, _ ids.PhaseID, _ inbound.Event) error { return nil }

type minimalMemory struct{}

func (m *minimalMemory) Ingest(_ context.Context, _ outbound.IngestMemoryInput) (*outbound.MemoryRecord, error) {
	return nil, nil
}
func (m *minimalMemory) Get(_ context.Context, _ string) (*outbound.MemoryRecord, error) {
	return nil, outbound.ErrNotFound
}
func (m *minimalMemory) GetByTopicKey(_ context.Context, _ outbound.MemoryScope, _ string) (*outbound.MemoryRecord, error) {
	return nil, outbound.ErrNotFound
}
func (m *minimalMemory) Archive(_ context.Context, _, _, _ string) error { return nil }
func (m *minimalMemory) Search(_ context.Context, _ outbound.SearchQuery) (*outbound.SearchResults, error) {
	return nil, nil
}
func (m *minimalMemory) BuildContext(_ context.Context, _ outbound.ContextRequest) (*outbound.ContextBundle, error) {
	return nil, nil
}
func (m *minimalMemory) RecordDecision(_ context.Context, _ outbound.RecordDecisionInput) (*outbound.MemoryRecord, error) {
	return nil, nil
}
func (m *minimalMemory) RecordRelation(_ context.Context, _ outbound.RecordRelationInput) error {
	return nil
}
