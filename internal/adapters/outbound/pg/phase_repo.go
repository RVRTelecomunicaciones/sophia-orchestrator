package pg

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// PhaseRepo persists Phase aggregates in Postgres.
type PhaseRepo struct {
	pool *pgxpool.Pool
}

// NewPhaseRepo constructs a PhaseRepo.
func NewPhaseRepo(pool *pgxpool.Pool) *PhaseRepo {
	if pool == nil {
		panic("pg.PhaseRepo: nil pool")
	}
	return &PhaseRepo{pool: pool}
}

// Save upserts a Phase row. Two conflict targets are handled:
//
//   - ON CONFLICT (id): in-place update for status/envelope/confidence
//     changes on the same attempt (e.g. running → terminal mid-phase).
//   - ON CONFLICT (change_id, phase_type, attempts): idempotent retry
//     upsert — when the orchestrator retries a blocked/failed phase it
//     bumps attempts before calling Save; this constraint prevents a
//     duplicate-key crash and updates the row in place.
//
// Spec #49: the (change_id, phase_type, attempts) upsert path is what
// makes retries idempotent without dropping prior history.
func (r *PhaseRepo) Save(ctx context.Context, p *phase.Phase) error {
	var envBytes []byte
	if p.Envelope() != nil {
		var err error
		envBytes, err = json.Marshal(p.Envelope())
		if err != nil {
			return wrapErr("PhaseRepo.Save.marshal", err)
		}
	}
	const q = `
INSERT INTO phases (id, change_id, phase_type, status, envelope, confidence, retry_budget, attempts, started_at, completed_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (change_id, phase_type, attempts) DO UPDATE SET
  status       = EXCLUDED.status,
  envelope     = EXCLUDED.envelope,
  confidence   = EXCLUDED.confidence,
  retry_budget = EXCLUDED.retry_budget,
  started_at   = EXCLUDED.started_at,
  completed_at = EXCLUDED.completed_at
`
	_, err := r.pool.Exec(ctx, q,
		p.ID().String(), p.ChangeID().String(), string(p.Type()), string(p.Status()),
		envBytes, p.Confidence(), p.RetryBudget(), p.Attempts(),
		p.StartedAt(), p.CompletedAt(),
	)
	return wrapErr("PhaseRepo.Save", err)
}

// FindByID returns the Phase identified by id.
func (r *PhaseRepo) FindByID(ctx context.Context, id ids.PhaseID) (*phase.Phase, error) {
	const q = `
SELECT id, change_id, phase_type, status, envelope, confidence, retry_budget, attempts, started_at, completed_at
FROM phases WHERE id = $1`
	row := r.pool.QueryRow(ctx, q, id.String())
	return scanPhase(row)
}

// FindByChangeAndType returns the most-recent Phase of pt for a Change.
func (r *PhaseRepo) FindByChangeAndType(ctx context.Context, changeID ids.ChangeID, pt phase.PhaseType) (*phase.Phase, error) {
	const q = `
SELECT id, change_id, phase_type, status, envelope, confidence, retry_budget, attempts, started_at, completed_at
FROM phases WHERE change_id = $1 AND phase_type = $2
ORDER BY attempts DESC LIMIT 1`
	row := r.pool.QueryRow(ctx, q, changeID.String(), string(pt))
	return scanPhase(row)
}

// FindAllRunning returns every Phase whose status is "running" across
// ALL changes. Used by the boot-time recovery scan to detect phases
// stranded by an orchestrator crash (Spec #68 / BUG-23). Returns an
// empty slice when nothing is running — never returns ErrNotFound.
func (r *PhaseRepo) FindAllRunning(ctx context.Context) ([]*phase.Phase, error) {
	const q = `
SELECT id, change_id, phase_type, status, envelope, confidence, retry_budget, attempts, started_at, completed_at
FROM phases WHERE status = 'running'
ORDER BY started_at`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, wrapErr("PhaseRepo.FindAllRunning", err)
	}
	defer rows.Close()
	out := make([]*phase.Phase, 0)
	for rows.Next() {
		p, scanErr := scanPhase(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapErr("PhaseRepo.FindAllRunning.iter", err)
	}
	return out, nil
}

// FindRunningByChange returns the Phase currently running for a Change, or
// outbound.ErrNotFound if none.
func (r *PhaseRepo) FindRunningByChange(ctx context.Context, changeID ids.ChangeID) (*phase.Phase, error) {
	const q = `
SELECT id, change_id, phase_type, status, envelope, confidence, retry_budget, attempts, started_at, completed_at
FROM phases WHERE change_id = $1 AND status IN ('running', 'interrupted')
ORDER BY started_at DESC LIMIT 1`
	row := r.pool.QueryRow(ctx, q, changeID.String())
	return scanPhase(row)
}

// LockByChange acquires a Postgres advisory lock keyed by the change_id so
// only one phase can be running per Change at a time across orchestrator
// instances. The lock is session-scoped — when the connection returns to
// the pool, the lock releases automatically (caveat: callers must NOT hold
// the lock across HTTP requests).
func (r *PhaseRepo) LockByChange(ctx context.Context, changeID ids.ChangeID) error {
	_, err := r.pool.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", changeID.String())
	return wrapErr("PhaseRepo.LockByChange", err)
}

func scanPhase(s scannable) (*phase.Phase, error) {
	var (
		idStr, changeIDStr, phaseTypeStr, statusStr string
		envBytes                                    []byte
		confidence                                  float64
		retryBudget, attempts                       int
		startedAt, completedAt                      *time.Time
	)
	err := s.Scan(&idStr, &changeIDStr, &phaseTypeStr, &statusStr, &envBytes, &confidence, &retryBudget, &attempts, &startedAt, &completedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, outbound.ErrNotFound
		}
		return nil, wrapErr("scanPhase", err)
	}
	pid, err := ids.ParsePhaseID(idStr)
	if err != nil {
		return nil, wrapErr("scanPhase.parse-phase-id", err)
	}
	cid, err := ids.ParseChangeID(changeIDStr)
	if err != nil {
		return nil, wrapErr("scanPhase.parse-change-id", err)
	}
	var env *envelope.Envelope
	if len(envBytes) > 0 {
		env = &envelope.Envelope{}
		if err := json.Unmarshal(envBytes, env); err != nil {
			return nil, wrapErr("scanPhase.unmarshal-envelope", err)
		}
	}
	return phase.Hydrate(
		pid, cid,
		phase.PhaseType(phaseTypeStr),
		phase.PhaseStatus(statusStr),
		env, confidence, retryBudget, attempts,
		startedAt, completedAt,
	), nil
}

// Compile-time interface check.
var _ outbound.PhaseRepository = (*PhaseRepo)(nil)
