package pg

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// BoardRepo persists Apply boards, groups, and tasks. Group + Task rows are
// hydrated alongside the Board on FindBoardByPhaseID via separate queries.
type BoardRepo struct {
	pool *pgxpool.Pool
}

// NewBoardRepo constructs a BoardRepo.
func NewBoardRepo(pool *pgxpool.Pool) *BoardRepo {
	if pool == nil {
		panic("pg.BoardRepo: nil pool")
	}
	return &BoardRepo{pool: pool}
}

// SaveBoard upserts the apply_boards row.
func (r *BoardRepo) SaveBoard(ctx context.Context, b *apply.Board) error {
	const q = `
INSERT INTO apply_boards (id, phase_id, status)
VALUES ($1, $2, $3)
ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status`
	_, err := r.pool.Exec(ctx, q, b.ID().String(), b.PhaseID().String(), string(b.Status()))
	return wrapErr("BoardRepo.SaveBoard", err)
}

// SaveGroup upserts a groups row, including the new build-gate columns
// (build_status, build_attempts) added in migration 008.
func (r *BoardRepo) SaveGroup(ctx context.Context, g *apply.Group) error {
	depsOn := make([]string, 0, len(g.DependsOn()))
	for _, d := range g.DependsOn() {
		depsOn = append(depsOn, d.String())
	}
	const q = `
INSERT INTO groups (id, board_id, name, depends_on, status, worktree_path, branch_name, build_status, build_attempts)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (id) DO UPDATE SET
  status         = EXCLUDED.status,
  worktree_path  = EXCLUDED.worktree_path,
  branch_name    = EXCLUDED.branch_name,
  build_status   = EXCLUDED.build_status,
  build_attempts = EXCLUDED.build_attempts`
	_, err := r.pool.Exec(ctx, q, g.ID().String(), g.BoardID().String(),
		g.Name(), depsOn, string(g.Status()), g.WorktreePath(), g.BranchName(),
		string(g.BuildStatus()), g.BuildAttempts())
	return wrapErr("BoardRepo.SaveGroup", err)
}

// SaveTask upserts a tasks row.
func (r *BoardRepo) SaveTask(ctx context.Context, t *apply.Task) error {
	var envBytes []byte
	if t.Envelope() != nil {
		var err error
		envBytes, err = json.Marshal(t.Envelope())
		if err != nil {
			return wrapErr("BoardRepo.SaveTask.marshal", err)
		}
	}
	var claimedBy *string
	if t.ClaimedBy() != nil {
		s := t.ClaimedBy().String()
		claimedBy = &s
	}
	const q = `
INSERT INTO tasks (id, group_id, description, files_pattern, status, claimed_by, attempts, envelope)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (id) DO UPDATE SET
  status     = EXCLUDED.status,
  claimed_by = EXCLUDED.claimed_by,
  attempts   = EXCLUDED.attempts,
  envelope   = EXCLUDED.envelope`
	_, err := r.pool.Exec(ctx, q,
		t.ID().String(), t.GroupID().String(),
		t.Description(), t.FilesPattern(), string(t.Status()),
		claimedBy, t.Attempts(), envBytes,
	)
	return wrapErr("BoardRepo.SaveTask", err)
}

// FindBoardByPhaseID returns the Board for phaseID, hydrated with all
// groups and their tasks.
func (r *BoardRepo) FindBoardByPhaseID(ctx context.Context, phaseID ids.PhaseID) (*apply.Board, error) {
	const boardQ = `SELECT id, phase_id, status FROM apply_boards WHERE phase_id = $1`
	var bid, pid, status string
	err := r.pool.QueryRow(ctx, boardQ, phaseID.String()).Scan(&bid, &pid, &status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, outbound.ErrNotFound
		}
		return nil, wrapErr("BoardRepo.FindBoardByPhaseID", err)
	}
	boardID, err := ids.ParseBoardID(bid)
	if err != nil {
		slog.Default().ErrorContext(ctx, "pg.BoardRepo: corrupt board_id in FindBoardByPhaseID",
			"repo", "board_repo", "column", "id", "raw_id", bid, "error", err)
		return nil, wrapErr("BoardRepo.FindBoardByPhaseID.parseBoard", err)
	}
	pidParsed, err := ids.ParsePhaseID(pid)
	if err != nil {
		slog.Default().ErrorContext(ctx, "pg.BoardRepo: corrupt phase_id in FindBoardByPhaseID",
			"repo", "board_repo", "column", "phase_id", "raw_id", pid, "error", err)
		return nil, wrapErr("BoardRepo.FindBoardByPhaseID.parsePhase", err)
	}

	groups, err := r.findGroupsByBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	return apply.HydrateBoard(boardID, pidParsed, apply.BoardStatus(status), groups), nil
}

// scanGroupRow hydrates one apply.Group from a scannable row.
// Returns a non-nil error when any ULID column fails to parse; the caller
// (findGroupsByBoard) MUST skip that row and continue (scan-loop policy).
func scanGroupRow(s scannable) (*apply.Group, error) {
	var (
		gid, bid, name, status string
		deps                   []string
		worktreePath, branch   string
		buildStatus            string
		buildAttempts          int
	)
	if err := s.Scan(&gid, &bid, &name, &deps, &status, &worktreePath, &branch,
		&buildStatus, &buildAttempts); err != nil {
		return nil, wrapErr("BoardRepo.scanGroup", err)
	}
	groupID, err := ids.ParseGroupID(gid)
	if err != nil {
		slog.Default().Error("pg.BoardRepo: corrupt group_id; skipping row",
			"repo", "board_repo", "column", "id", "raw_id", gid, "error", err)
		return nil, err
	}
	bidParsed, err := ids.ParseBoardID(bid)
	if err != nil {
		slog.Default().Error("pg.BoardRepo: corrupt board_id in group row; skipping row",
			"repo", "board_repo", "column", "board_id", "raw_id", bid, "error", err)
		return nil, err
	}
	depIDs := make([]ids.GroupID, 0, len(deps))
	for _, d := range deps {
		depID, depErr := ids.ParseGroupID(d)
		if depErr != nil {
			slog.Default().Error("pg.BoardRepo: corrupt depends_on entry in group row; skipping row",
				"repo", "board_repo", "column", "depends_on", "raw_id", d, "error", depErr)
			return nil, depErr
		}
		depIDs = append(depIDs, depID)
	}
	g := apply.HydrateGroup(
		groupID, bidParsed, name, depIDs,
		apply.GroupStatus(status),
		worktreePath, branch,
		apply.GroupBuildStatus(buildStatus), buildAttempts,
	)
	return g, nil
}

// pgRows is the minimal interface the scan-loops need from pgx.Rows.
// Extracting it allows the loop bodies to be tested with a fake without a live DB.
type pgRows interface {
	scannable
	Next() bool
	Close()
	Err() error
}

// iterateGroupRows is the testable core of findGroupsByBoard. It consumes rows
// from any pgRows source and applies the scan-loop skip policy: corrupt rows
// (ULID parse errors) are skipped; valid rows have their tasks loaded via
// taskFinder and are appended to the result.
func iterateGroupRows(rows pgRows, taskFinder func(ids.GroupID) ([]*apply.Task, error)) ([]*apply.Group, error) {
	defer rows.Close()
	var out []*apply.Group
	for rows.Next() {
		g, scanErr := scanGroupRow(rows)
		if scanErr != nil {
			// Scan-loop policy: skip corrupt rows; log was already emitted by scanGroupRow.
			continue
		}

		tasks, err := taskFinder(g.ID())
		if err != nil {
			return nil, err
		}
		for _, t := range tasks {
			apply.AttachTaskToGroup(g, t)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// iterateTaskRows is the testable core of findTasksByGroup. Corrupt rows are
// skipped (scan-loop policy); valid rows are appended.
func iterateTaskRows(rows pgRows) ([]*apply.Task, error) {
	defer rows.Close()
	var out []*apply.Task
	for rows.Next() {
		t, scanErr := scanTaskRow(rows)
		if scanErr != nil {
			// Scan-loop policy: skip corrupt rows; log was already emitted by scanTaskRow.
			continue
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *BoardRepo) findGroupsByBoard(ctx context.Context, boardID ids.BoardID) ([]*apply.Group, error) {
	const q = `
SELECT id, board_id, name, depends_on, status, worktree_path, branch_name,
       build_status, build_attempts
FROM groups WHERE board_id = $1`
	rows, err := r.pool.Query(ctx, q, boardID.String())
	if err != nil {
		return nil, wrapErr("BoardRepo.findGroupsByBoard", err)
	}
	// Use HydrateGroup so all persisted fields (status, build state) are
	// restored without replaying transitions. AddTask is not available once
	// a group is beyond Pending, so we pass tasks to the board builder
	// below via a small two-step that avoids the transition guard.
	//
	// Workaround: re-hydrate via a helper that passes tasks through the
	// group struct directly (groups are passed into HydrateBoard as-is).
	// The simplest approach that avoids reflection is to build a fresh group
	// with all hydrated tasks and then apply HydrateGroup again — but that
	// duplicates construction. Instead we expose the task list attachment via
	// a package-level helper that is only callable by the persistence adapter.
	return iterateGroupRows(rows, func(groupID ids.GroupID) ([]*apply.Task, error) {
		return r.findTasksByGroup(ctx, groupID)
	})
}

// scanTaskRow hydrates one apply.Task from a scannable row.
// Returns a non-nil error when any ULID column fails to parse; the caller
// (findTasksByGroup) MUST skip that row and continue (scan-loop policy).
func scanTaskRow(s scannable) (*apply.Task, error) {
	var (
		tid, gid, description, status string
		files                         []string
		claimedBy                     *string
		attempts                      int
		envBytes                      []byte
	)
	if err := s.Scan(&tid, &gid, &description, &files, &status, &claimedBy, &attempts, &envBytes); err != nil {
		return nil, wrapErr("BoardRepo.scanTask", err)
	}
	taskID, err := ids.ParseTaskID(tid)
	if err != nil {
		slog.Default().Error("pg.BoardRepo: corrupt task_id; skipping row",
			"repo", "board_repo", "column", "id", "raw_id", tid, "error", err)
		return nil, err
	}
	groupIDParsed, err := ids.ParseGroupID(gid)
	if err != nil {
		slog.Default().Error("pg.BoardRepo: corrupt group_id in task row; skipping row",
			"repo", "board_repo", "column", "group_id", "raw_id", gid, "error", err)
		return nil, err
	}
	var sid *ids.SessionID
	if claimedBy != nil {
		parsed, parseErr := ids.ParseSessionID(*claimedBy)
		if parseErr != nil {
			slog.Default().Error("pg.BoardRepo: corrupt claimed_by in task row; skipping row",
				"repo", "board_repo", "column", "claimed_by", "raw_id", *claimedBy, "error", parseErr)
			return nil, parseErr
		}
		sid = &parsed
	}
	var env *envelope.Envelope
	if len(envBytes) > 0 {
		var e envelope.Envelope
		if jsonErr := json.Unmarshal(envBytes, &e); jsonErr == nil {
			env = &e
		}
	}
	t, err := apply.HydrateTask(
		taskID, groupIDParsed, description, files,
		apply.TaskStatus(status), sid, attempts, env,
	)
	if err != nil {
		return nil, wrapErr("BoardRepo.hydrateTask", err)
	}
	return t, nil
}

func (r *BoardRepo) findTasksByGroup(ctx context.Context, groupID ids.GroupID) ([]*apply.Task, error) {
	const q = `
SELECT id, group_id, description, files_pattern, status, claimed_by, attempts, envelope
FROM tasks WHERE group_id = $1`
	rows, err := r.pool.Query(ctx, q, groupID.String())
	if err != nil {
		return nil, wrapErr("BoardRepo.findTasksByGroup", err)
	}
	return iterateTaskRows(rows)
}

// FindTaskByID returns a Task by id, fully hydrated from the persisted row.
func (r *BoardRepo) FindTaskByID(ctx context.Context, id ids.TaskID) (*apply.Task, error) {
	const q = `
SELECT id, group_id, description, files_pattern, status, claimed_by, attempts, envelope
FROM tasks WHERE id = $1`
	var tid, gid, description, status string
	var files []string
	var claimedBy *string
	var attempts int
	var envBytes []byte
	err := r.pool.QueryRow(ctx, q, id.String()).Scan(&tid, &gid, &description, &files, &status, &claimedBy, &attempts, &envBytes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, outbound.ErrNotFound
		}
		return nil, wrapErr("BoardRepo.FindTaskByID", err)
	}
	taskID, err := ids.ParseTaskID(tid)
	if err != nil {
		slog.Default().ErrorContext(ctx, "pg.BoardRepo: corrupt task_id in FindTaskByID",
			"repo", "board_repo", "column", "id", "raw_id", tid, "error", err)
		return nil, wrapErr("BoardRepo.FindTaskByID.parseTask", err)
	}
	groupID, err := ids.ParseGroupID(gid)
	if err != nil {
		slog.Default().ErrorContext(ctx, "pg.BoardRepo: corrupt group_id in FindTaskByID",
			"repo", "board_repo", "column", "group_id", "raw_id", gid, "error", err)
		return nil, wrapErr("BoardRepo.FindTaskByID.parseGroup", err)
	}

	var sid *ids.SessionID
	if claimedBy != nil {
		parsed, parseErr := ids.ParseSessionID(*claimedBy)
		if parseErr != nil {
			slog.Default().ErrorContext(ctx, "pg.BoardRepo: corrupt claimed_by in FindTaskByID",
				"repo", "board_repo", "column", "claimed_by", "raw_id", *claimedBy, "error", parseErr)
			return nil, wrapErr("BoardRepo.FindTaskByID.parseClaimedBy", parseErr)
		}
		sid = &parsed
	}

	var env *envelope.Envelope
	if len(envBytes) > 0 {
		var e envelope.Envelope
		if jsonErr := json.Unmarshal(envBytes, &e); jsonErr == nil {
			env = &e
		}
	}

	return apply.HydrateTask(taskID, groupID, description, files, apply.TaskStatus(status), sid, attempts, env)
}

// ClaimTask atomically claims a task for a session. Returns claimed=true
// only if the row was in pending status before the update.
func (r *BoardRepo) ClaimTask(ctx context.Context, taskID ids.TaskID, sessionID ids.SessionID) (bool, error) {
	const q = `
UPDATE tasks SET status = 'claimed', claimed_by = $1
WHERE id = $2 AND status = 'pending'
RETURNING id`
	var returned string
	err := r.pool.QueryRow(ctx, q, sessionID.String(), taskID.String()).Scan(&returned)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil // already claimed or not pending
		}
		return false, wrapErr("BoardRepo.ClaimTask", err)
	}
	return true, nil
}

// Compile-time interface check.
var _ outbound.BoardRepository = (*BoardRepo)(nil)
