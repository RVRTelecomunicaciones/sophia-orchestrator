package outbound

import (
	"context"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
)

// ArtifactStore is the outbound port for SDD artifact persistence.
// Concrete adapters (memoryengine, openspec, hybrid) are wired based on
// Change.ArtifactStoreMode. See ADR-0003.
type ArtifactStore interface {
	Mode() change.ArtifactStoreMode

	// Save persists an artifact under topicKey. Idempotent: re-saving the
	// same topicKey upserts the content (memory-engine semantics).
	Save(ctx context.Context, in SaveArtifactInput) error

	// Load fetches the most recent artifact for topicKey. Returns
	// outbound.ErrNotFound if no artifact exists.
	Load(ctx context.Context, topicKey string) (*Artifact, error)
}

// SaveArtifactInput is the wire shape for ArtifactStore.Save.
type SaveArtifactInput struct {
	TopicKey    string
	Type        string // "sdd_proposal" | "sdd_spec" | ...
	Content     []byte // markdown or JSON
	ContentType string // "text/markdown" | "application/json"
	Summary     string
	Tags        []string
	Scope       MemoryScope      // for memory-engine
	Provenance  MemoryProvenance // for memory-engine
	OpenspecPath string          // optional override for openspec store
}

// Artifact is the loaded artifact body.
type Artifact struct {
	TopicKey    string
	Type        string
	Content     []byte
	ContentType string
	Summary     string
	Tags        []string
}
