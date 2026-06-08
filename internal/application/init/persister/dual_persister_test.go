package persister_test

// dual_persister_test.go — E.1–E.4 (Strict TDD: RED tests first)
//
// Tests for DualPersister: memory-first HARD + file SOFT + idempotent re-persist.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/detector"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/persister"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// --- fakes ---

// fakeMemoryClient records calls to Ingest.
type fakeMemoryClient struct {
	mu          sync.Mutex
	ingestCalls int
	ingestErr   error
}

func (f *fakeMemoryClient) Ingest(_ context.Context, in outbound.IngestMemoryInput) (*outbound.MemoryRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ingestCalls++
	if f.ingestErr != nil {
		return nil, f.ingestErr
	}
	return &outbound.MemoryRecord{TopicKey: in.TopicKey}, nil
}

// fakeMemoryClient must satisfy a narrow interface used by DualPersister.
// The full MemoryClient is too wide; DualPersister uses only Ingest.
// Implement the remaining methods as no-ops to satisfy the interface.
func (f *fakeMemoryClient) Get(_ context.Context, _ string) (*outbound.MemoryRecord, error) {
	return nil, nil
}
func (f *fakeMemoryClient) GetByTopicKey(_ context.Context, _ outbound.MemoryScope, _ string) (*outbound.MemoryRecord, error) {
	return nil, nil
}
func (f *fakeMemoryClient) Archive(_ context.Context, _, _, _ string) error { return nil }
func (f *fakeMemoryClient) Search(_ context.Context, _ outbound.SearchQuery) (*outbound.SearchResults, error) {
	return nil, nil
}
func (f *fakeMemoryClient) BuildContext(_ context.Context, _ outbound.ContextRequest) (*outbound.ContextBundle, error) {
	return nil, nil
}
func (f *fakeMemoryClient) RecordDecision(_ context.Context, _ outbound.RecordDecisionInput) (*outbound.MemoryRecord, error) {
	return nil, nil
}
func (f *fakeMemoryClient) RecordRelation(_ context.Context, _ outbound.RecordRelationInput) error {
	return nil
}

// fakeFileCache records Write calls and can simulate errors.
type fakeFileCache struct {
	mu         sync.Mutex
	writeCalls int
	writeErr   error
}

func (f *fakeFileCache) Write(_ context.Context, _ string, _ detector.StructuralContext) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writeCalls++
	return f.writeErr
}

func (f *fakeFileCache) Lookup(_ context.Context, _ string) (*detector.StructuralContext, bool, error) {
	return nil, false, nil
}

// --- helpers ---

func makeSCForPersist() detector.StructuralContext {
	return detector.StructuralContext{
		SchemaVersion:     detector.StructuralContextSchemaV1,
		ProjectID:         "proj-1",
		ChangeID:          "ch-1",
		ChangeName:        "my-change",
		SophiaDetectorVer: detector.SophiaDetectorVer,
	}
}

// --- tests ---

// E.1: DualPersister.Persist calls both Ingest AND FileCache.Write when both succeed.
func TestDualPersister_Persist_BothSucceed(t *testing.T) {
	mem := &fakeMemoryClient{}
	fc := &fakeFileCache{}
	p := persister.New(mem, fc, nil, "tenant-1", "dev")

	err := p.Persist(context.Background(), makeSCForPersist(), "key-1")
	require.NoError(t, err)
	require.Equal(t, 1, mem.ingestCalls, "expected Ingest called once")
	require.Equal(t, 1, fc.writeCalls, "expected FileCache.Write called once")
}

// E.2: DualPersister.Persist when MemoryClient.Ingest returns error → returns that
// error immediately (HARD); file write was NOT attempted.
func TestDualPersister_Persist_MemoryHard(t *testing.T) {
	hardErr := errors.New("memory-engine: 503")
	mem := &fakeMemoryClient{ingestErr: hardErr}
	fc := &fakeFileCache{}
	p := persister.New(mem, fc, nil, "tenant-1", "dev")

	err := p.Persist(context.Background(), makeSCForPersist(), "key-2")
	require.Error(t, err)
	require.ErrorIs(t, err, hardErr)
	require.Equal(t, 0, fc.writeCalls, "FileCache.Write must NOT be called when Ingest fails")
}

// E.3: DualPersister.Persist when FileCache.Write returns error → logs WARN but
// returns nil (SOFT); MemoryClient.Ingest was called and succeeded.
func TestDualPersister_Persist_FileSoft(t *testing.T) {
	mem := &fakeMemoryClient{}
	fc := &fakeFileCache{writeErr: errors.New("disk full")}
	p := persister.New(mem, fc, nil, "tenant-1", "dev")

	err := p.Persist(context.Background(), makeSCForPersist(), "key-3")
	require.NoError(t, err, "file cache failure must be SOFT (nil returned)")
	require.Equal(t, 1, mem.ingestCalls, "Ingest must have been called")
	require.Equal(t, 1, fc.writeCalls, "FileCache.Write must have been attempted")
}

// E.4: DualPersister.Persist with same topic_key called twice (idempotent) —
// both calls succeed; no panic or duplicate-key error from fake.
func TestDualPersister_Persist_Idempotent(t *testing.T) {
	mem := &fakeMemoryClient{}
	fc := &fakeFileCache{}
	p := persister.New(mem, fc, nil, "tenant-1", "dev")

	sc := makeSCForPersist()
	require.NoError(t, p.Persist(context.Background(), sc, "key-4"))
	require.NoError(t, p.Persist(context.Background(), sc, "key-4"))
	require.Equal(t, 2, mem.ingestCalls)
	require.Equal(t, 2, fc.writeCalls)
}
