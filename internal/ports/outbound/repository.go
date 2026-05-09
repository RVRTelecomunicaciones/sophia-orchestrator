package outbound

import (
	"context"
	"errors"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/worktree"
)

// ErrNotFound is the canonical not-found error returned by repositories
// and outbound clients (e.g., MemoryClient.Get / GetByTopicKey on 404).
// Adapters MUST wrap their backend-specific not-found error with this so
// application services can rely on errors.Is(err, outbound.ErrNotFound).
var ErrNotFound = errors.New("repository: not found")

// ErrInvalidRequest is returned by outbound clients when the caller passed
// a request that fails client-side validation (e.g., missing required
// query parameters). Surfacing this before the wire avoids spurious 4xx
// responses and makes the error checkable via errors.Is.
var ErrInvalidRequest = errors.New("outbound: invalid request")

// ChangeRepository persists Change aggregates. Save uses upsert semantics on
// the (project, name) UNIQUE constraint.
type ChangeRepository interface {
	Save(ctx context.Context, c *change.Change) error
	FindByID(ctx context.Context, id ids.ChangeID) (*change.Change, error)
	FindByProjectName(ctx context.Context, project, name string) (*change.Change, error)
	List(ctx context.Context, project, status string, limit, offset int) ([]*change.Change, error)
}

// PhaseRepository persists Phase aggregates. The (change_id, phase_type,
// attempts) UNIQUE constraint provides idempotency: re-saving the same
// triple replays the same row without inserting a duplicate.
type PhaseRepository interface {
	Save(ctx context.Context, p *phase.Phase) error
	FindByID(ctx context.Context, id ids.PhaseID) (*phase.Phase, error)
	FindByChangeAndType(ctx context.Context, changeID ids.ChangeID, pt phase.PhaseType) (*phase.Phase, error)
	FindRunningByChange(ctx context.Context, changeID ids.ChangeID) (*phase.Phase, error)
	// LockByChange acquires a Postgres advisory lock keyed by the change_id
	// so only one phase can be running per Change at a time. Released on
	// transaction commit/rollback.
	LockByChange(ctx context.Context, changeID ids.ChangeID) error
}

// BoardRepository persists ApplyBoard, Group, and Task. ClaimTask is
// transactional: it returns ok=true iff the task moved from pending to
// claimed under the caller's session id.
type BoardRepository interface {
	SaveBoard(ctx context.Context, b *apply.Board) error
	FindBoardByPhaseID(ctx context.Context, phaseID ids.PhaseID) (*apply.Board, error)
	SaveGroup(ctx context.Context, g *apply.Group) error
	SaveTask(ctx context.Context, t *apply.Task) error
	FindTaskByID(ctx context.Context, id ids.TaskID) (*apply.Task, error)
	ClaimTask(ctx context.Context, taskID ids.TaskID, sessionID ids.SessionID) (claimed bool, err error)
}

// SessionRepository persists AgentSession.
type SessionRepository interface {
	Save(ctx context.Context, s *session.Session) error
	FindByID(ctx context.Context, id ids.SessionID) (*session.Session, error)
	FindByPhaseID(ctx context.Context, phaseID ids.PhaseID) ([]*session.Session, error)
}

// WorktreeRepository persists Worktree.
type WorktreeRepository interface {
	Save(ctx context.Context, w *worktree.Worktree) error
	FindByID(ctx context.Context, id ids.WorktreeID) (*worktree.Worktree, error)
	FindBySessionID(ctx context.Context, sessionID ids.SessionID) (*worktree.Worktree, error)
}
