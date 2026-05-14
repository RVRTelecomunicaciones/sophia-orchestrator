package pg

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// SpawnGovernorRepo persists the SpawnGovernor counter in the singleton
// spawn_governor_state row, using a Postgres advisory transaction lock to
// serialize Acquire+Release calls across orchestrator instances.
type SpawnGovernorRepo struct {
	pool *pgxpool.Pool
}

// NewSpawnGovernorRepo constructs a SpawnGovernorRepo.
func NewSpawnGovernorRepo(pool *pgxpool.Pool) *SpawnGovernorRepo {
	if pool == nil {
		panic("pg.SpawnGovernorRepo: nil pool")
	}
	return &SpawnGovernorRepo{pool: pool}
}

// Acquire atomically tests-and-increments the active counter under a
// transaction-scoped advisory lock.
func (r *SpawnGovernorRepo) Acquire(ctx context.Context, maxConcurrent int) (bool, int, error) {
	var ok bool
	var current int
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext('sophia-spawn-governor'))"); err != nil {
			return err
		}
		var active int
		if err := tx.QueryRow(ctx, "SELECT active_count FROM spawn_governor_state WHERE id = 1").Scan(&active); err != nil {
			return err
		}
		if active >= maxConcurrent {
			current = active
			return nil
		}
		if _, err := tx.Exec(ctx,
			"UPDATE spawn_governor_state SET active_count = active_count + 1, max_count = $1, updated_at = $2 WHERE id = 1",
			maxConcurrent, time.Now(),
		); err != nil {
			return err
		}
		ok = true
		current = active + 1
		return nil
	})
	if err != nil {
		return false, 0, wrapErr("SpawnGovernorRepo.Acquire", err)
	}
	return ok, current, nil
}

// Release decrements the active counter under an advisory lock. Bounded at 0.
func (r *SpawnGovernorRepo) Release(ctx context.Context) error {
	const q = `
UPDATE spawn_governor_state
SET active_count = GREATEST(active_count - 1, 0),
    updated_at = $1
WHERE id = 1`
	_, err := r.pool.Exec(ctx, q, time.Now())
	return wrapErr("SpawnGovernorRepo.Release", err)
}

// Active returns the current active count.
func (r *SpawnGovernorRepo) Active(ctx context.Context) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, "SELECT active_count FROM spawn_governor_state WHERE id = 1").Scan(&n)
	if err != nil {
		return 0, wrapErr("SpawnGovernorRepo.Active", err)
	}
	return n, nil
}

// Compile-time interface check.
var _ outbound.SpawnGovernorRepo = (*SpawnGovernorRepo)(nil)
