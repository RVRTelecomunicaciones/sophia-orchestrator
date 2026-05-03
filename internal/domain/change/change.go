// Package change models the SDD Change aggregate root: the unit of work
// driven through the 9 SDD phases. The Change owns lifecycle (active →
// completed/aborted) and the CurrentPhase pointer; Phase aggregates
// (separate package) own per-phase state.
package change

import (
	"fmt"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
)

// Change is the SDD aggregate root.
type Change struct {
	id            ids.ChangeID
	name          string
	project       string
	status        Status
	currentPhase  phase.PhaseType
	artifactStore ArtifactStoreMode
	baseRef       string
	createdAt     time.Time
	updatedAt     time.Time
}

// New constructs a fresh Change in StatusActive starting at PhaseInit. Inputs
// are validated; pass them through Parse* boundary types where applicable.
func New(id ids.ChangeID, name, project string, store ArtifactStoreMode, baseRef string, createdAt time.Time) (*Change, error) {
	if name == "" {
		return nil, ErrEmptyName
	}
	if project == "" {
		return nil, ErrEmptyProject
	}
	if !store.IsValid() {
		return nil, fmt.Errorf("%w: %q", ErrInvalidArtifactStore, store)
	}
	return &Change{
		id:            id,
		name:          name,
		project:       project,
		status:        StatusActive,
		currentPhase:  phase.PhaseInit,
		artifactStore: store,
		baseRef:       baseRef,
		createdAt:     createdAt,
		updatedAt:     createdAt,
	}, nil
}

// Hydrate reconstructs a Change from persisted fields. Used by repositories;
// does not run the same validation as New.
func Hydrate(
	id ids.ChangeID,
	name, project string,
	status Status,
	currentPhase phase.PhaseType,
	store ArtifactStoreMode,
	baseRef string,
	createdAt, updatedAt time.Time,
) *Change {
	return &Change{
		id: id, name: name, project: project,
		status: status, currentPhase: currentPhase,
		artifactStore: store, baseRef: baseRef,
		createdAt: createdAt, updatedAt: updatedAt,
	}
}

// ID returns the Change identifier.
func (c *Change) ID() ids.ChangeID { return c.id }

// Name returns the unique-per-project change name.
func (c *Change) Name() string { return c.name }

// Project returns the project namespace.
func (c *Change) Project() string { return c.project }

// Status returns the current lifecycle status.
func (c *Change) Status() Status { return c.status }

// CurrentPhase returns the pointer to the active phase type.
func (c *Change) CurrentPhase() phase.PhaseType { return c.currentPhase }

// ArtifactStore returns the artifact-store mode.
func (c *Change) ArtifactStore() ArtifactStoreMode { return c.artifactStore }

// BaseRef returns the optional git ref the change branched from.
func (c *Change) BaseRef() string { return c.baseRef }

// CreatedAt returns the creation timestamp.
func (c *Change) CreatedAt() time.Time { return c.createdAt }

// UpdatedAt returns the last-mutation timestamp.
func (c *Change) UpdatedAt() time.Time { return c.updatedAt }

// AdvancePhase moves the CurrentPhase pointer to next, if next is a valid
// successor of the current phase. Returns ErrInvalidTransition otherwise.
// Returns ErrAlreadyTerminal if the Change has already reached a terminal
// status.
func (c *Change) AdvancePhase(next phase.PhaseType, now time.Time) error {
	if c.status.IsTerminal() {
		return ErrAlreadyTerminal
	}
	for _, v := range c.currentPhase.NextValid() {
		if v == next {
			c.currentPhase = next
			c.updatedAt = now
			return nil
		}
	}
	return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, c.currentPhase, next)
}

// MarkCompleted moves the Change to StatusCompleted. Allowed only after the
// archive phase has finished. Caller is responsible for verifying that
// Phase.archive.Status == DONE.
func (c *Change) MarkCompleted(now time.Time) error {
	if c.status.IsTerminal() {
		return ErrAlreadyTerminal
	}
	c.status = StatusCompleted
	c.updatedAt = now
	return nil
}

// Abort moves the Change to StatusAborted with a recorded reason.
func (c *Change) Abort(_ string, now time.Time) error {
	if c.status.IsTerminal() {
		return ErrAlreadyTerminal
	}
	c.status = StatusAborted
	c.updatedAt = now
	return nil
}
