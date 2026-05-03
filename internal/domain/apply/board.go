package apply

import "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"

// Board is the Apply Phase task board: groups + tasks + lifecycle.
type Board struct {
	id      ids.BoardID
	phaseID ids.PhaseID
	status  BoardStatus
	groups  []*Group
}

// NewBoard creates a Board in BoardStatusBuilding.
func NewBoard(id ids.BoardID, phaseID ids.PhaseID) *Board {
	return &Board{id: id, phaseID: phaseID, status: BoardStatusBuilding}
}

// HydrateBoard reconstructs a Board with pre-existing groups.
func HydrateBoard(id ids.BoardID, phaseID ids.PhaseID, status BoardStatus, groups []*Group) *Board {
	return &Board{id: id, phaseID: phaseID, status: status, groups: groups}
}

// ID returns the board id.
func (b *Board) ID() ids.BoardID { return b.id }

// PhaseID returns the parent phase id.
func (b *Board) PhaseID() ids.PhaseID { return b.phaseID }

// Status returns the current board status.
func (b *Board) Status() BoardStatus { return b.status }

// Groups returns the groups in insertion order.
func (b *Board) Groups() []*Group { return b.groups }

// AddGroup appends a group during BoardStatusBuilding.
func (b *Board) AddGroup(g *Group) error {
	if b.status != BoardStatusBuilding {
		return ErrInvalidBoardTransition
	}
	b.groups = append(b.groups, g)
	return nil
}

// Start transitions Building → Running.
func (b *Board) Start() error {
	if b.status != BoardStatusBuilding {
		return ErrInvalidBoardTransition
	}
	b.status = BoardStatusRunning
	return nil
}

// Complete transitions Running → Completed.
func (b *Board) Complete() error {
	if b.status != BoardStatusRunning {
		return ErrInvalidBoardTransition
	}
	b.status = BoardStatusCompleted
	return nil
}

// Fail transitions Running → Failed (or Building → Failed for early aborts).
func (b *Board) Fail() error {
	if b.status == BoardStatusCompleted || b.status == BoardStatusFailed {
		return ErrInvalidBoardTransition
	}
	b.status = BoardStatusFailed
	return nil
}
