// Package inbound declares the service interfaces consumed by the HTTP
// inbound layer (and, in V2, the MCP server). Application services
// (internal/application/*) implement these interfaces.
package inbound

import (
	"context"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
)

// CreateChangeInput is the inbound shape for ChangeService.Create.
type CreateChangeInput struct {
	Name              string
	Project           string
	ArtifactStoreMode change.ArtifactStoreMode
	BaseRef           string
}

// ChangeService is the application service for SDD Change lifecycle.
type ChangeService interface {
	Create(ctx context.Context, in CreateChangeInput) (*change.Change, error)
	Get(ctx context.Context, id ids.ChangeID) (*change.Change, error)
	List(ctx context.Context, project, status string, limit, offset int) ([]*change.Change, error)
	Abort(ctx context.Context, id ids.ChangeID, reason string) error
}
