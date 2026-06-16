package outbound

import (
	"context"
	"errors"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skillusage"
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

// PhaseRepository persists Phase aggregates. Save uses
// ON CONFLICT (change_id, phase_type, attempts) DO UPDATE so retrying a
// blocked or failed phase — where the orchestrator increments attempts
// before calling Save — replaces the prior row in place rather than
// inserting a duplicate and crashing with a UNIQUE violation (Spec #49).
type PhaseRepository interface {
	Save(ctx context.Context, p *phase.Phase) error
	FindByID(ctx context.Context, id ids.PhaseID) (*phase.Phase, error)
	FindByChangeAndType(ctx context.Context, changeID ids.ChangeID, pt phase.PhaseType) (*phase.Phase, error)
	FindRunningByChange(ctx context.Context, changeID ids.ChangeID) (*phase.Phase, error)
	// FindAllRunning returns every Phase currently in PhaseStatusRunning,
	// across ALL Changes. Used by the boot-time recovery scan (Spec #68 /
	// BUG-23) to find phases stranded by an orchestrator crash so the
	// recovery service can mark them interrupted and surface them to the
	// operator for explicit Resume. Returns an empty slice (NOT
	// ErrNotFound) when nothing is running.
	FindAllRunning(ctx context.Context) ([]*phase.Phase, error)
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

// SkillUsageRepository persists SkillUsage records (migration 011).
// Insert is idempotent: ON CONFLICT DO NOTHING on (change_id, phase_type, skill_id, skill_version).
// UpdateOutcome sets the outcome column for a specific row.
// FindByChange returns all rows for a given change_id (ordered by injected_at asc).
// FindBySkill returns all rows for a given skill_id (ordered by injected_at desc).
// SumApplyAttemptsByChange returns SUM(tasks.attempts) for the change's apply
// tasks (joined tasks→groups→apply_boards→phases), 0 when none (D-LH-2).
type SkillUsageRepository interface {
	Insert(ctx context.Context, su *skillusage.SkillUsage) error
	UpdateOutcome(ctx context.Context, id ids.SkillUsageID, outcome skillusage.Outcome) error
	FindByChange(ctx context.Context, changeID ids.ChangeID) ([]*skillusage.SkillUsage, error)
	FindBySkill(ctx context.Context, skillID ids.SkillID) ([]*skillusage.SkillUsage, error)
	SumApplyAttemptsByChange(ctx context.Context, changeID ids.ChangeID) (int, error)
}

// ReevalRun is one persisted reeval-run audit record: the immutable snapshot of
// a `reeval --apply --confirm` (mode "apply") or a `reeval --revert` (mode
// "revert") operation. RevertsRunID is set only for revert runs and names the
// apply-run whose transitions were inverted. Items carry the per-skill
// prior/new status pair that defines the inverse operation (D1 loop-hardening).
type ReevalRun struct {
	ID           string
	Mode         string // "apply" | "revert"
	RevertsRunID string // empty for apply runs
	CreatedAt    time.Time
	Items        []ReevalRunItem
}

// ReevalRunItem is one per-skill transition inside a ReevalRun: the status the
// skill held before the transition (PriorStatus) and the status it moved to
// (NewStatus). Revert applies the inverse (NewStatus → PriorStatus) by walking
// the legal transition chain.
type ReevalRunItem struct {
	ID          string
	SkillID     string
	PriorStatus string
	NewStatus   string
}

// ReevalAuditRepository persists the append-only reeval-run audit trail
// (migration 013). Save writes a run plus its items in one transaction.
// FindByID and FindLatest read a run back so the revert path can compute the
// inverse transitions. Returns ErrNotFound when no run matches.
type ReevalAuditRepository interface {
	Save(ctx context.Context, run ReevalRun) error
	FindByID(ctx context.Context, runID string) (ReevalRun, error)
	FindLatest(ctx context.Context) (ReevalRun, error)
}

// SkillRepository persists Skill aggregates with V4.1 §5.2 lifecycle fields.
//
// FindByPhase returns every Skill whose phases array contains pt AND
// status='active'. The status filter is a hard-coded invariant (D-M1-6):
// FindByPhase callers MUST NEVER receive non-active skills. New code uses
// SkillMatcher.SkillsForContext for typed queries with explicit status semantics.
// The result slice is empty (not ErrNotFound) when no active Skill matches.
//
// Upsert inserts or fully replaces a Skill row on CONFLICT (name, version) per
// migration 010 constraint swap (D-M1-1). Idempotent: calling Upsert twice
// with the same name+version is a no-op for unchanged content.
//
// InsertIfAbsent inserts the Skill only when no row with the same (name, version)
// exists. It is a no-op (returns nil) when a matching name+version is already
// present. This is the legacy seeder operation; new code uses Upsert.
//
// List returns all persisted Skills in no guaranteed order.
//
// FindByID returns the skill with the given ID. Returns ErrNotFound when absent.
//
// PatchMetrics atomically applies additive deltas to the metrics JSONB column
// and sets last_used_at to now. Uses SELECT FOR UPDATE within a transaction.
// Returns ErrNotFound when no skill with that ID exists.
//
// PatchStatus updates the skill's status column and conditionally sets
// last_validated_at (when new status is "validated"). Returns ErrNotFound when
// no skill with that ID exists.
type SkillRepository interface {
	FindByPhase(ctx context.Context, pt phase.PhaseType) ([]*skill.Skill, error)
	Upsert(ctx context.Context, s *skill.Skill) error
	InsertIfAbsent(ctx context.Context, s *skill.Skill) error
	List(ctx context.Context) ([]*skill.Skill, error)
	FindByID(ctx context.Context, id ids.SkillID) (*skill.Skill, error)
	PatchMetrics(ctx context.Context, id ids.SkillID, delta skill.Metrics, now time.Time) error
	PatchStatus(ctx context.Context, id ids.SkillID, status skill.Status, now time.Time) error
}
