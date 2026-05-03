package inbound

import (
	"context"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
)

// ApplyService exposes apply-phase introspection: read-only access to the
// task board for SSE events and HTTP GETs. Apply execution itself is
// triggered via PhaseService.Run with PhaseType=apply.
type ApplyService interface {
	GetBoard(ctx context.Context, phaseID ids.PhaseID) (*apply.Board, error)
}
