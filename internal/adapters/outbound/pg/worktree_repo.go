package pg

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/worktree"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// WorktreeRepo persists Worktree aggregates.
type WorktreeRepo struct {
	pool *pgxpool.Pool
}

// NewWorktreeRepo constructs a WorktreeRepo.
func NewWorktreeRepo(pool *pgxpool.Pool) *WorktreeRepo {
	if pool == nil {
		panic("pg.WorktreeRepo: nil pool")
	}
	return &WorktreeRepo{pool: pool}
}

// Save upserts a worktree row.
func (r *WorktreeRepo) Save(ctx context.Context, w *worktree.Worktree) error {
	var sessionID *string
	if w.SessionID() != nil {
		v := w.SessionID().String()
		sessionID = &v
	}
	const q = `
INSERT INTO worktrees (id, session_id, path, branch, status, created_at, cleaned_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (id) DO UPDATE SET
  status     = EXCLUDED.status,
  cleaned_at = EXCLUDED.cleaned_at,
  session_id = EXCLUDED.session_id`
	_, err := r.pool.Exec(ctx, q,
		w.ID().String(), sessionID, w.Path(), w.Branch(),
		string(w.Status()), w.CreatedAt(), w.CleanedAt(),
	)
	return wrapErr("WorktreeRepo.Save", err)
}

// FindByID returns the Worktree identified by id.
func (r *WorktreeRepo) FindByID(ctx context.Context, id ids.WorktreeID) (*worktree.Worktree, error) {
	const q = `
SELECT id, session_id, path, branch, status, created_at, cleaned_at
FROM worktrees WHERE id = $1`
	row := r.pool.QueryRow(ctx, q, id.String())
	return scanWorktree(row)
}

// FindBySessionID returns the Worktree associated with a session.
func (r *WorktreeRepo) FindBySessionID(ctx context.Context, sessionID ids.SessionID) (*worktree.Worktree, error) {
	const q = `
SELECT id, session_id, path, branch, status, created_at, cleaned_at
FROM worktrees WHERE session_id = $1
ORDER BY created_at DESC LIMIT 1`
	row := r.pool.QueryRow(ctx, q, sessionID.String())
	return scanWorktree(row)
}

func scanWorktree(s scannable) (*worktree.Worktree, error) {
	var (
		idStr        string
		sessionIDStr *string
		path, branch string
		status       string
		createdAt    time.Time
		cleanedAt    *time.Time
	)
	err := s.Scan(&idStr, &sessionIDStr, &path, &branch, &status, &createdAt, &cleanedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, outbound.ErrNotFound
		}
		return nil, wrapErr("scanWorktree", err)
	}
	wid, _ := ids.ParseWorktreeID(idStr)
	var sid *ids.SessionID
	if sessionIDStr != nil {
		v, _ := ids.ParseSessionID(*sessionIDStr)
		sid = &v
	}
	return worktree.Hydrate(
		wid, sid, path, branch,
		worktree.Status(status), createdAt, cleanedAt,
	), nil
}

// Compile-time interface check.
var _ outbound.WorktreeRepository = (*WorktreeRepo)(nil)
