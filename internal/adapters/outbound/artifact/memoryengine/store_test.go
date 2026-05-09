package memoryengine_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/artifact/memoryengine"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

type fakeMemory struct {
	captured  outbound.IngestMemoryInput
	ingestErr error
}

func (m *fakeMemory) Ingest(_ context.Context, in outbound.IngestMemoryInput) (*outbound.MemoryRecord, error) {
	m.captured = in
	if m.ingestErr != nil {
		return nil, m.ingestErr
	}
	return &outbound.MemoryRecord{ID: "01ARZ3NDEKTSV4RRFFQ69G5MEM", Type: in.Type, TopicKey: in.TopicKey, CreatedAt: time.Now()}, nil
}
func (m *fakeMemory) Get(_ context.Context, _ string) (*outbound.MemoryRecord, error) {
	return nil, outbound.ErrNotFound
}
func (m *fakeMemory) GetByTopicKey(_ context.Context, _ outbound.MemoryScope, _ string) (*outbound.MemoryRecord, error) {
	return nil, outbound.ErrNotFound
}
func (m *fakeMemory) Archive(_ context.Context, _, _, _ string) error { return nil }
func (m *fakeMemory) Search(_ context.Context, _ outbound.SearchQuery) (*outbound.SearchResults, error) {
	return nil, nil
}
func (m *fakeMemory) BuildContext(_ context.Context, _ outbound.ContextRequest) (*outbound.ContextBundle, error) {
	return nil, nil
}
func (m *fakeMemory) RecordDecision(_ context.Context, _ outbound.RecordDecisionInput) (*outbound.MemoryRecord, error) {
	return nil, nil
}
func (m *fakeMemory) RecordRelation(_ context.Context, _ outbound.RecordRelationInput) error {
	return nil
}

func TestNew_PanicsOnNil(t *testing.T) {
	require.Panics(t, func() { _ = memoryengine.New(nil) })
}

func TestMode(t *testing.T) {
	s := memoryengine.New(&fakeMemory{})
	require.Equal(t, change.ArtifactStoreMemoryEngine, s.Mode())
}

func TestSave_Success(t *testing.T) {
	m := &fakeMemory{}
	s := memoryengine.New(m)
	require.NoError(t, s.Save(context.Background(), outbound.SaveArtifactInput{
		TopicKey: "sdd/feat-x/spec",
		Type:     "sdd_spec",
		Content:  []byte("# Spec\nbody"),
		Scope:    outbound.MemoryScope{ProjectID: "proj"},
		Provenance: outbound.MemoryProvenance{
			Source: "sophia-orchestator", Method: "sdd-phase-output",
		},
	}))
	require.Equal(t, "sdd/feat-x/spec", m.captured.TopicKey)
	require.Equal(t, "sdd_spec", m.captured.Type)
	require.Equal(t, "# Spec\nbody", m.captured.Content)
	// Default content_type tag injected.
	require.Contains(t, m.captured.Tags, "content_type:text/markdown")
}

func TestSave_CustomContentType(t *testing.T) {
	m := &fakeMemory{}
	s := memoryengine.New(m)
	require.NoError(t, s.Save(context.Background(), outbound.SaveArtifactInput{
		TopicKey: "k", Type: "t", Content: []byte("{}"), ContentType: "application/json",
	}))
	require.Contains(t, m.captured.Tags, "content_type:application/json")
}

func TestSave_RejectsEmptyTopicKey(t *testing.T) {
	s := memoryengine.New(&fakeMemory{})
	err := s.Save(context.Background(), outbound.SaveArtifactInput{TopicKey: ""})
	require.Error(t, err)
}

func TestSave_PropagatesError(t *testing.T) {
	m := &fakeMemory{ingestErr: errors.New("boom")}
	s := memoryengine.New(m)
	require.Error(t, s.Save(context.Background(), outbound.SaveArtifactInput{TopicKey: "k", Type: "t"}))
}

func TestLoad_V1ReturnsNotFound(t *testing.T) {
	s := memoryengine.New(&fakeMemory{})
	_, err := s.Load(context.Background(), "sdd/feat-x/spec")
	require.ErrorIs(t, err, outbound.ErrNotFound)
}
