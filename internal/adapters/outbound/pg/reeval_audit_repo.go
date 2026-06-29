package pg

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// ReevalAuditRepo persists the append-only reeval-run audit trail into the
// reeval_run + reeval_run_item tables (migration 013). Save writes a run and
// all its items in one transaction so a run is never partially recorded. This
// is the immutable snapshot that `reeval --revert` reads to compute the inverse
// transitions (D1 loop-hardening follow-up).
type ReevalAuditRepo struct {
	pool *pgxpool.Pool
}

// NewReevalAuditRepo constructs a ReevalAuditRepo.
func NewReevalAuditRepo(pool *pgxpool.Pool) *ReevalAuditRepo {
	if pool == nil {
		panic("pg.ReevalAuditRepo: nil pool")
	}
	return &ReevalAuditRepo{pool: pool}
}

// Save writes the run row plus every item row in a single transaction. The
// reverts_run_id column is NULL for apply runs (empty RevertsRunID).
func (r *ReevalAuditRepo) Save(ctx context.Context, run outbound.ReevalRun) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return wrapErr("ReevalAuditRepo.Save.begin", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const insertRun = `
INSERT INTO reeval_run (id, mode, reverts_run_id, created_at)
VALUES ($1, $2, $3, $4)`
	if _, err := tx.Exec(ctx, insertRun,
		run.ID, run.Mode, nullableStr(run.RevertsRunID), run.CreatedAt,
	); err != nil {
		return wrapErr("ReevalAuditRepo.Save.run", err)
	}

	const insertItem = `
INSERT INTO reeval_run_item (id, run_id, skill_id, prior_status, new_status)
VALUES ($1, $2, $3, $4, $5)`
	for _, it := range run.Items {
		if _, err := tx.Exec(ctx, insertItem,
			it.ID, run.ID, it.SkillID, it.PriorStatus, it.NewStatus,
		); err != nil {
			return wrapErr("ReevalAuditRepo.Save.item", err)
		}
	}

	return wrapErr("ReevalAuditRepo.Save.commit", tx.Commit(ctx))
}

// FindByID loads a run and its items. Returns outbound.ErrNotFound when no run
// matches the id.
func (r *ReevalAuditRepo) FindByID(ctx context.Context, runID string) (outbound.ReevalRun, error) {
	const q = `
SELECT id, mode, COALESCE(reverts_run_id, ''), created_at
FROM   reeval_run
WHERE  id = $1`
	run, err := r.scanRun(ctx, q, runID)
	if err != nil {
		return outbound.ReevalRun{}, wrapErr("ReevalAuditRepo.FindByID", err)
	}
	return run, nil
}

// FindLatest loads the most recently created run and its items. Returns
// outbound.ErrNotFound when the audit trail is empty.
func (r *ReevalAuditRepo) FindLatest(ctx context.Context) (outbound.ReevalRun, error) {
	const q = `
SELECT id, mode, COALESCE(reverts_run_id, ''), created_at
FROM   reeval_run
ORDER  BY created_at DESC, id DESC
LIMIT  1`
	run, err := r.scanRun(ctx, q)
	if err != nil {
		return outbound.ReevalRun{}, wrapErr("ReevalAuditRepo.FindLatest", err)
	}
	return run, nil
}

// scanRun reads a single run row from q (with optional args) and hydrates its
// items. pgx.ErrNoRows is mapped to ErrNotFound by the caller via wrapErr.
func (r *ReevalAuditRepo) scanRun(ctx context.Context, q string, args ...any) (outbound.ReevalRun, error) {
	var run outbound.ReevalRun
	if err := r.pool.QueryRow(ctx, q, args...).Scan(
		&run.ID, &run.Mode, &run.RevertsRunID, &run.CreatedAt,
	); err != nil {
		return outbound.ReevalRun{}, err
	}

	const itemsQ = `
SELECT id, skill_id, prior_status, new_status
FROM   reeval_run_item
WHERE  run_id = $1
ORDER  BY id ASC`
	rows, err := r.pool.Query(ctx, itemsQ, run.ID)
	if err != nil {
		return outbound.ReevalRun{}, wrapErr("ReevalAuditRepo.scanRun.items", err)
	}
	defer rows.Close()

	for rows.Next() {
		var it outbound.ReevalRunItem
		if err := rows.Scan(&it.ID, &it.SkillID, &it.PriorStatus, &it.NewStatus); err != nil {
			return outbound.ReevalRun{}, wrapErr("ReevalAuditRepo.scanRun.scan", err)
		}
		run.Items = append(run.Items, it)
	}
	return run, wrapErr("ReevalAuditRepo.scanRun.rows", rows.Err())
}

// ExistsByRevertsRunID returns true when any revert run already names
// originalRunID as its reverts_run_id. The query runs a single indexed lookup
// on the reverts_run_id column (present since migration 013, no new migration
// needed). Used by revertRun to enforce idempotency before emitting metrics.
func (r *ReevalAuditRepo) ExistsByRevertsRunID(ctx context.Context, originalRunID string) (bool, error) {
	const q = `SELECT EXISTS(SELECT 1 FROM reeval_run WHERE reverts_run_id = $1)`
	var exists bool
	if err := r.pool.QueryRow(ctx, q, originalRunID).Scan(&exists); err != nil {
		return false, wrapErr("ReevalAuditRepo.ExistsByRevertsRunID", err)
	}
	return exists, nil
}

// nullableStr returns nil for an empty string so a NULL is stored, else the value.
func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Verify ReevalAuditRepo satisfies the port at compile time.
var _ outbound.ReevalAuditRepository = (*ReevalAuditRepo)(nil)
