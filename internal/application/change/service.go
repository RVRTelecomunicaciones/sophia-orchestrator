// Package change implements the ChangeService inbound port. It owns the
// SDD Change lifecycle use cases: Create, Get, List, Abort. Phase execution
// and apply coordination live in sibling packages (phase, apply).
package change

import (
	"context"
	"errors"
	"fmt"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// ErrAlreadyExists is returned when Create is called for a (project, name)
// pair that already has a Change.
var ErrAlreadyExists = errors.New("change: already exists")

// Service implements inbound.ChangeService. Constructed by bootstrap/wire.go
// with concrete repository, clock, and ID generator implementations.
type Service struct {
	repo  outbound.ChangeRepository
	clock shared.Clock
	idGen shared.IDGenerator
}

// New constructs a Service. All dependencies are required (panic on nil).
func New(repo outbound.ChangeRepository, clock shared.Clock, idGen shared.IDGenerator) *Service {
	if repo == nil || clock == nil || idGen == nil {
		panic("change.Service: nil dependency")
	}
	return &Service{repo: repo, clock: clock, idGen: idGen}
}

// Create persists a new SDD Change in StatusActive at PhaseInit.
// Returns ErrAlreadyExists if a Change with the same (project, name) exists.
func (s *Service) Create(ctx context.Context, in inbound.CreateChangeInput) (*change.Change, error) {
	existing, err := s.repo.FindByProjectName(ctx, in.Project, in.Name)
	if err != nil && !errors.Is(err, outbound.ErrNotFound) {
		return nil, fmt.Errorf("find existing change: %w", err)
	}
	if existing != nil {
		return nil, fmt.Errorf("%w: project=%q name=%q", ErrAlreadyExists, in.Project, in.Name)
	}

	id, err := ids.ParseChangeID(s.idGen.NewID())
	if err != nil {
		return nil, fmt.Errorf("generate change id: %w", err)
	}

	c, err := change.New(id, in.Name, in.Project, in.ArtifactStoreMode, in.BaseRef, s.clock.Now())
	if err != nil {
		return nil, err //nolint:wrapcheck // domain sentinel surfaced as-is
	}

	if err := s.repo.Save(ctx, c); err != nil {
		return nil, fmt.Errorf("save change: %w", err)
	}
	return c, nil
}

// Get returns the Change identified by id, or outbound.ErrNotFound.
func (s *Service) Get(ctx context.Context, id ids.ChangeID) (*change.Change, error) {
	c, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("find change: %w", err)
	}
	return c, nil
}

// List returns Changes for a project filtered by status. limit defaults to
// 50 if non-positive; offset clamped to 0.
func (s *Service) List(ctx context.Context, project, status string, limit, offset int) ([]*change.Change, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	cs, err := s.repo.List(ctx, project, status, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list changes: %w", err)
	}
	return cs, nil
}

// Abort transitions a Change to StatusAborted. Errors if the Change is
// already terminal (completed or aborted).
func (s *Service) Abort(ctx context.Context, id ids.ChangeID, reason string) error {
	c, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("find change: %w", err)
	}
	if err := c.Abort(reason, s.clock.Now()); err != nil {
		return err //nolint:wrapcheck // domain sentinel surfaced as-is
	}
	if err := s.repo.Save(ctx, c); err != nil {
		return fmt.Errorf("save change: %w", err)
	}
	return nil
}
