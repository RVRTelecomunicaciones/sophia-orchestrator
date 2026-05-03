// Package worktree models the lifecycle of a git worktree assigned to an
// AgentSession in the apply phase: created → locked → released → cleaned.
package worktree

import (
	"errors"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
)

// Status is the lifecycle of a Worktree.
type Status string

// Worktree statuses.
const (
	StatusCreated  Status = "created"
	StatusLocked   Status = "locked"
	StatusReleased Status = "released"
	StatusCleaned  Status = "cleaned"
)

// IsValid reports whether s is a known status.
func (s Status) IsValid() bool {
	switch s {
	case StatusCreated, StatusLocked, StatusReleased, StatusCleaned:
		return true
	}
	return false
}

// Sentinel errors raised by the Worktree aggregate.
var (
	ErrInvalidTransition = errors.New("worktree: invalid transition")
	ErrEmptyPath         = errors.New("worktree: empty path")
	ErrEmptyBranch       = errors.New("worktree: empty branch")
)

// Worktree captures a checked-out git working tree assigned to an
// AgentSession (typically a team-lead or implement agent).
type Worktree struct {
	id        ids.WorktreeID
	sessionID *ids.SessionID
	path      string
	branch    string
	status    Status
	createdAt time.Time
	cleanedAt *time.Time
}

// New constructs a Worktree in StatusCreated.
func New(id ids.WorktreeID, sessionID *ids.SessionID, path, branch string, createdAt time.Time) (*Worktree, error) {
	if path == "" {
		return nil, ErrEmptyPath
	}
	if branch == "" {
		return nil, ErrEmptyBranch
	}
	return &Worktree{
		id: id, sessionID: sessionID,
		path: path, branch: branch,
		status: StatusCreated, createdAt: createdAt,
	}, nil
}

// Hydrate reconstructs a Worktree from persisted fields.
func Hydrate(id ids.WorktreeID, sessionID *ids.SessionID, path, branch string, status Status, createdAt time.Time, cleanedAt *time.Time) *Worktree {
	return &Worktree{
		id: id, sessionID: sessionID,
		path: path, branch: branch,
		status: status, createdAt: createdAt, cleanedAt: cleanedAt,
	}
}

// ID returns the worktree id.
func (w *Worktree) ID() ids.WorktreeID { return w.id }

// SessionID returns the session id (or nil if unassigned).
func (w *Worktree) SessionID() *ids.SessionID { return w.sessionID }

// Path returns the absolute filesystem path.
func (w *Worktree) Path() string { return w.path }

// Branch returns the git branch name checked out in the worktree.
func (w *Worktree) Branch() string { return w.branch }

// Status returns the current worktree status.
func (w *Worktree) Status() Status { return w.status }

// CreatedAt returns the creation timestamp.
func (w *Worktree) CreatedAt() time.Time { return w.createdAt }

// CleanedAt returns the cleanup timestamp (nil if still active).
func (w *Worktree) CleanedAt() *time.Time { return w.cleanedAt }

// Lock transitions Created → Locked.
func (w *Worktree) Lock() error {
	if w.status != StatusCreated {
		return ErrInvalidTransition
	}
	w.status = StatusLocked
	return nil
}

// Release transitions Locked or Created → Released.
func (w *Worktree) Release() error {
	if w.status != StatusLocked && w.status != StatusCreated {
		return ErrInvalidTransition
	}
	w.status = StatusReleased
	return nil
}

// Clean transitions any non-cleaned status to Cleaned with timestamp.
func (w *Worktree) Clean(cleanedAt time.Time) error {
	if w.status == StatusCleaned {
		return ErrInvalidTransition
	}
	w.status = StatusCleaned
	w.cleanedAt = &cleanedAt
	return nil
}
