package phase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// capturingMemory stubs MemoryClient and records every Ingest call so the
// persist tests can assert on the exact wire shape (scope, type,
// topic_key, summary). Other methods return zero-value or are unused.
type capturingMemory struct {
	ingestCalls []outbound.IngestMemoryInput
	ingestErr   error // when non-nil, every Ingest returns this
}

func (m *capturingMemory) Ingest(_ context.Context, in outbound.IngestMemoryInput) (*outbound.MemoryRecord, error) {
	m.ingestCalls = append(m.ingestCalls, in)
	if m.ingestErr != nil {
		return nil, m.ingestErr
	}
	return &outbound.MemoryRecord{ID: "memrec-" + in.TopicKey, TopicKey: in.TopicKey, Type: in.Type}, nil
}

func (m *capturingMemory) Get(_ context.Context, _ string) (*outbound.MemoryRecord, error) {
	return nil, errors.New("not used")
}
func (m *capturingMemory) GetByTopicKey(_ context.Context, _ outbound.MemoryScope, _ string) (*outbound.MemoryRecord, error) {
	return nil, errors.New("not used")
}
func (m *capturingMemory) Archive(_ context.Context, _, _, _ string) error { return nil }
func (m *capturingMemory) Search(_ context.Context, _ outbound.SearchQuery) (*outbound.SearchResults, error) {
	return nil, nil
}
func (m *capturingMemory) BuildContext(_ context.Context, _ outbound.ContextRequest) (*outbound.ContextBundle, error) {
	return nil, nil
}
func (m *capturingMemory) RecordDecision(_ context.Context, _ outbound.RecordDecisionInput) (*outbound.MemoryRecord, error) {
	return nil, nil
}
func (m *capturingMemory) RecordRelation(_ context.Context, _ outbound.RecordRelationInput) error {
	return nil
}

// minimalDeps builds a Service with ONLY the dependencies persistArtifactsToMemory
// touches: Memory, Events (for the failure event), and Clock for
// publishEvent. Everything else is left nil — the helper purposely
// stays narrow so a regression in unrelated wiring does not flake
// these tests.
func minimalServiceForPersist(t *testing.T, mem outbound.MemoryClient) *Service {
	t.Helper()
	return &Service{
		d: Deps{
			Memory: mem,
			Events: stubEventStream{},
			Clock:  shared.FixedClock(time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)),
		},
	}
}

// stubEventStream swallows publishes — persist_artifacts only emits on
// failure path and the assertion there is on the memory call count, not
// on the event itself (covered separately in the IngestError test).
type stubEventStream struct{}

func (stubEventStream) Publish(_ context.Context, _ ids.PhaseID, _ inbound.Event) error {
	return nil
}
func (stubEventStream) Subscribe(_ context.Context, _ ids.PhaseID) (<-chan inbound.Event, func(), error) {
	return nil, func() {}, nil
}

// mkChange + mkPhase + mkEnv build the minimal aggregate set the persist
// helper needs. Kept tiny on purpose — these are NOT testing the
// aggregates themselves, they're just feeding shape to the helper.
func mkChange(t *testing.T, name, project string) *change.Change {
	t.Helper()
	cid, err := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	require.NoError(t, err)
	c, err := change.New(cid, name, project, change.ArtifactStoreMemoryEngine, "", time.Now())
	require.NoError(t, err)
	return c
}

func mkPhase(t *testing.T, c *change.Change, ptype phase.PhaseType) *phase.Phase {
	t.Helper()
	pid, err := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5FAW")
	require.NoError(t, err)
	p, err := phase.New(pid, c.ID(), ptype, 3) // retry_budget=3, the standard default
	require.NoError(t, err)
	return p
}

func mkEnv(refs []envelope.ArtifactRef, summary string) *envelope.Envelope {
	return &envelope.Envelope{
		SchemaVersion:    envelope.SchemaVersionV1,
		Phase:            "explore",
		ChangeName:       "x",
		Project:          "x",
		Status:           envelope.StatusDone,
		Confidence:       0.9,
		ExecutiveSummary: summary,
		ArtifactsSaved:   refs,
	}
}

// --- tests ---

func TestPersistArtifactsToMemory_NoArtifacts_NoIngestCall(t *testing.T) {
	mem := &capturingMemory{}
	s := minimalServiceForPersist(t, mem)
	c := mkChange(t, "no-art", "proj-a")
	p := mkPhase(t, c, phase.PhaseExplore)

	s.persistArtifactsToMemory(context.Background(), c, p, mkEnv(nil, ""))
	require.Empty(t, mem.ingestCalls, "no artifacts → must not call Memory.Ingest")
}

func TestPersistArtifactsToMemory_NilEnvelope_NoIngestCall(t *testing.T) {
	mem := &capturingMemory{}
	s := minimalServiceForPersist(t, mem)
	c := mkChange(t, "nil-env", "proj-a")
	p := mkPhase(t, c, phase.PhaseExplore)

	s.persistArtifactsToMemory(context.Background(), c, p, nil)
	require.Empty(t, mem.ingestCalls, "nil envelope → must not call Memory.Ingest")
}

func TestPersistArtifactsToMemory_SingleArtifact_IngestsWithExpectedShape(t *testing.T) {
	mem := &capturingMemory{}
	s := minimalServiceForPersist(t, mem)
	c := mkChange(t, "feat-x", "proj-a")
	p := mkPhase(t, c, phase.PhaseExplore)
	env := mkEnv([]envelope.ArtifactRef{
		{TopicKey: "sdd/feat-x/explore", Type: "explore"},
	}, "explored the auth module")

	s.persistArtifactsToMemory(context.Background(), c, p, env)
	require.Len(t, mem.ingestCalls, 1)
	got := mem.ingestCalls[0]
	require.Equal(t, "sdd/feat-x/explore", got.TopicKey)
	require.Equal(t, "semantic", got.Type, "type MUST match memory-engine's MemoryType enum (episodic|semantic) — SDD outputs are semantic")
	require.Contains(t, got.Tags, "sdd", "tags MUST include 'sdd' for downstream query filtering")
	require.Contains(t, got.Tags, "explore", "tags MUST include the phase type")
	require.Equal(t, "explored the auth module", got.Summary)
	require.Equal(t, "proj-a", got.Scope.ProjectID)
	require.Equal(t, "sophia-orchestator", got.Scope.AgentID)
	require.Equal(t, c.ID().String(), got.Scope.SessionID, "session must carry change_id for trace-back")
	require.Equal(t, "sophia-orchestator", got.Provenance.Source)
	require.Equal(t, "direct", got.Provenance.Method, "method MUST match memory-engine's IngestMethod enum (direct|derived|imported|worker_generated)")
	require.Contains(t, got.Provenance.SourceURI, c.ID().String())
	require.Contains(t, got.Provenance.SourceURI, p.ID().String())
	require.NotEmpty(t, got.Content, "Content must carry the full envelope JSON")
}

func TestPersistArtifactsToMemory_MultipleArtifacts_OneIngestPerRef(t *testing.T) {
	mem := &capturingMemory{}
	s := minimalServiceForPersist(t, mem)
	c := mkChange(t, "feat-y", "proj-a")
	p := mkPhase(t, c, phase.PhaseSpec)
	env := mkEnv([]envelope.ArtifactRef{
		{TopicKey: "sdd/feat-y/spec", Type: "spec"},
		{TopicKey: "sdd/feat-y/spec-appendix", Type: "spec"},
	}, "sum")

	s.persistArtifactsToMemory(context.Background(), c, p, env)
	require.Len(t, mem.ingestCalls, 2)
	require.Equal(t, "sdd/feat-y/spec", mem.ingestCalls[0].TopicKey)
	require.Equal(t, "sdd/feat-y/spec-appendix", mem.ingestCalls[1].TopicKey)
}

func TestPersistArtifactsToMemory_EmptyTopicKey_Skipped(t *testing.T) {
	// An ArtifactRef without a topic_key cannot be upserted — memory-
	// engine's idempotency relies on the key (partial unique index from
	// memory-engine migration 004). Skipping is safer than persisting a
	// topic-less row that nothing can find later.
	mem := &capturingMemory{}
	s := minimalServiceForPersist(t, mem)
	c := mkChange(t, "feat-z", "proj-a")
	p := mkPhase(t, c, phase.PhaseExplore)
	env := mkEnv([]envelope.ArtifactRef{
		{TopicKey: "", Type: "explore"},
		{TopicKey: "sdd/feat-z/explore", Type: "explore"},
	}, "")

	s.persistArtifactsToMemory(context.Background(), c, p, env)
	require.Len(t, mem.ingestCalls, 1, "empty topic_key entry skipped; the populated one persists")
	require.Equal(t, "sdd/feat-z/explore", mem.ingestCalls[0].TopicKey)
}

func TestPersistArtifactsToMemory_IngestError_DoesNotPanicAndContinues(t *testing.T) {
	// Memory failure is fail-soft per the contract (Iron Law #1: phase
	// is already saved). The helper must NOT panic, NOT return an error
	// (the func has no return), and SHOULD attempt subsequent refs even
	// if an earlier one failed.
	mem := &capturingMemory{ingestErr: errors.New("memory: 503 service unavailable")}
	s := minimalServiceForPersist(t, mem)
	c := mkChange(t, "feat-w", "proj-a")
	p := mkPhase(t, c, phase.PhaseExplore)
	env := mkEnv([]envelope.ArtifactRef{
		{TopicKey: "sdd/feat-w/explore", Type: "explore"},
		{TopicKey: "sdd/feat-w/explore-2", Type: "explore"},
	}, "")

	require.NotPanics(t, func() {
		s.persistArtifactsToMemory(context.Background(), c, p, env)
	})
	require.Len(t, mem.ingestCalls, 2, "first failure must NOT short-circuit subsequent refs")
}

