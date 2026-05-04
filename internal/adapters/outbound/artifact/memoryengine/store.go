// Package memoryengine implements outbound.ArtifactStore by ingesting/
// fetching SDD artifacts via outbound.MemoryClient against
// sophia-memory-engine. topic_key is the upsert key (sdd/{change}/{phase}).
// See ADR-0003.
package memoryengine

import (
	"context"
	"fmt"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// Store implements outbound.ArtifactStore atop a MemoryClient.
type Store struct {
	memory outbound.MemoryClient
}

// New constructs a Store. memory must be non-nil.
func New(memory outbound.MemoryClient) *Store {
	if memory == nil {
		panic("memoryengine.Store: nil MemoryClient")
	}
	return &Store{memory: memory}
}

// Mode reports change.ArtifactStoreMemoryEngine.
func (s *Store) Mode() change.ArtifactStoreMode { return change.ArtifactStoreMemoryEngine }

// Save ingests the artifact via MemoryClient.Ingest.
func (s *Store) Save(ctx context.Context, in outbound.SaveArtifactInput) error {
	if in.TopicKey == "" {
		return fmt.Errorf("memoryengine.Save: empty topic_key")
	}
	contentType := in.ContentType
	if contentType == "" {
		contentType = "text/markdown"
	}
	tags := append([]string{}, in.Tags...)
	tags = append(tags, "content_type:"+contentType)

	_, err := s.memory.Ingest(ctx, outbound.IngestMemoryInput{
		Type:       in.Type,
		Content:    string(in.Content),
		Summary:    in.Summary,
		Tags:       tags,
		TopicKey:   in.TopicKey,
		Scope:      in.Scope,
		Provenance: in.Provenance,
	})
	if err != nil {
		return fmt.Errorf("memoryengine.Save: %w", err)
	}
	return nil
}

// Load looks up an artifact by topic_key. NOTE: sophia-memory-engine's
// /api/v1/memories/{id} addresses by record id, NOT topic_key. The proper
// path is /api/v1/search with TopicKey-equality, but the V1 MemoryClient
// only exposes Get(id). For V1, Load returns outbound.ErrNotFound and the
// orchestrator falls back to its local Postgres state. V2 will add a
// SearchByTopicKey shortcut to the MemoryClient.
func (s *Store) Load(ctx context.Context, _ string) (*outbound.Artifact, error) {
	_ = ctx
	return nil, outbound.ErrNotFound
}

// Compile-time interface check.
var _ outbound.ArtifactStore = (*Store)(nil)
