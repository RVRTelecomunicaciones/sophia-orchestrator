// Package apply implements the ApplyService inbound port. V1 exposes only
// read-only board introspection; write paths (RunApply 18-step parallel
// flow) live in a sibling RunService scheduled for the next milestone.
package apply

import (
	"context"
	"fmt"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// Service implements inbound.ApplyService for read-only board introspection.
type Service struct {
	repo outbound.BoardRepository
}

// New constructs a Service. repo is required.
func New(repo outbound.BoardRepository) *Service {
	if repo == nil {
		panic("apply.Service: nil repo")
	}
	return &Service{repo: repo}
}

// GetBoard returns the apply Board for the given Phase, or
// outbound.ErrNotFound if no board exists (e.g. the phase isn't apply or
// hasn't reached the building state yet).
func (s *Service) GetBoard(ctx context.Context, phaseID ids.PhaseID) (*apply.Board, error) {
	b, err := s.repo.FindBoardByPhaseID(ctx, phaseID)
	if err != nil {
		return nil, fmt.Errorf("find board: %w", err)
	}
	return b, nil
}
