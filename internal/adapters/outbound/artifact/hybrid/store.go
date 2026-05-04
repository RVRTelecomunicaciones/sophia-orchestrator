// Package hybrid composes memoryengine + openspec stores: writes both,
// reads memory-engine first with openspec fallback. Used when Change.
// ArtifactStoreMode == hybrid.
package hybrid

import (
	"context"
	"errors"
	"fmt"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// Store implements outbound.ArtifactStore by writing to BOTH a primary
// (memory-engine) and a secondary (openspec). Reads prefer the primary.
type Store struct {
	primary   outbound.ArtifactStore
	secondary outbound.ArtifactStore
}

// New constructs a hybrid Store. Both backends must be non-nil.
func New(primary, secondary outbound.ArtifactStore) *Store {
	if primary == nil || secondary == nil {
		panic("hybrid.Store: nil backend")
	}
	return &Store{primary: primary, secondary: secondary}
}

// Mode reports change.ArtifactStoreHybrid.
func (s *Store) Mode() change.ArtifactStoreMode { return change.ArtifactStoreHybrid }

// Save writes the artifact to BOTH backends. If primary fails, the call
// fails (memory-engine is the system of record). If secondary fails after
// primary succeeded, the call returns the secondary error wrapped — the
// caller can decide whether to ignore (acceptable: primary saved).
func (s *Store) Save(ctx context.Context, in outbound.SaveArtifactInput) error {
	if err := s.primary.Save(ctx, in); err != nil {
		return fmt.Errorf("hybrid.Save primary: %w", err)
	}
	if err := s.secondary.Save(ctx, in); err != nil {
		return fmt.Errorf("hybrid.Save secondary: %w", err)
	}
	return nil
}

// Load tries the primary first; on outbound.ErrNotFound, falls back to
// secondary. Other primary errors propagate.
func (s *Store) Load(ctx context.Context, topicKey string) (*outbound.Artifact, error) {
	a, err := s.primary.Load(ctx, topicKey)
	if err == nil {
		return a, nil
	}
	if !errors.Is(err, outbound.ErrNotFound) {
		return nil, err
	}
	return s.secondary.Load(ctx, topicKey)
}

// Compile-time interface check.
var _ outbound.ArtifactStore = (*Store)(nil)
