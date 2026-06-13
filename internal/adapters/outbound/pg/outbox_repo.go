package pg

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/outbox"
)

// OutboxRepo persists webhook_outbox rows (migration 012) and drives the relay
// poller's at-least-once delivery loop. EnqueueTx joins a caller-provided
// transaction so the outbox INSERT commits atomically with change completion;
// the relay-facing methods (ClaimDue/MarkDelivered/Reschedule/PendingCount)
// run on the pool.
type OutboxRepo struct {
	pool *pgxpool.Pool
}

// NewOutboxRepo constructs an OutboxRepo. Pool must be non-nil.
func NewOutboxRepo(pool *pgxpool.Pool) *OutboxRepo {
	if pool == nil {
		panic("pg.OutboxRepo: nil pool")
	}
	return &OutboxRepo{pool: pool}
}

// SaveCompletedWithOutbox upserts the completed change row and INSERTs the
// pending outbox event in ONE transaction (loop-hardening D-LH-1). The outbox
// row commits if and only if change completion commits, closing the data-loss
// window the legacy fire-and-forget POST left open. Implements the
// phase.OutboxEnqueuer port.
func (r *OutboxRepo) SaveCompletedWithOutbox(ctx context.Context, c *change.Change, ev *outbox.Event) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return wrapErr("OutboxRepo.SaveCompletedWithOutbox.begin", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const upsert = `
INSERT INTO changes (id, name, project, status, current_phase, artifact_store, base_ref, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (id) DO UPDATE SET
  status        = EXCLUDED.status,
  current_phase = EXCLUDED.current_phase,
  updated_at    = EXCLUDED.updated_at`
	if _, err := tx.Exec(ctx, upsert,
		c.ID().String(), c.Name(), c.Project(),
		string(c.Status()), string(c.CurrentPhase()),
		string(c.ArtifactStore()), c.BaseRef(),
		c.CreatedAt(), c.UpdatedAt(),
	); err != nil {
		return wrapErr("OutboxRepo.SaveCompletedWithOutbox.change", err)
	}

	if err := r.EnqueueTx(ctx, tx, ev); err != nil {
		return err
	}

	return wrapErr("OutboxRepo.SaveCompletedWithOutbox.commit", tx.Commit(ctx))
}

// EnqueueTx INSERTs a pending outbox row inside the caller's transaction. If the
// transaction rolls back, the row never becomes visible — this is how the
// outbox shares the change-completion transaction.
func (r *OutboxRepo) EnqueueTx(ctx context.Context, tx pgx.Tx, ev *outbox.Event) error {
	const q = `
INSERT INTO webhook_outbox (id, event_type, payload, status, attempts, next_attempt_at, created_at, delivered_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err := tx.Exec(ctx, q,
		ev.ID().String(),
		ev.EventType().String(),
		ev.Payload(),
		ev.Status().String(),
		ev.Attempts(),
		ev.NextAttemptAt(),
		ev.CreatedAt(),
		nullableTime(ev.DeliveredAt()),
	)
	return wrapErr("OutboxRepo.EnqueueTx", err)
}

// ClaimDue selects up to limit pending rows due for delivery, locking each with
// FOR UPDATE SKIP LOCKED so concurrent relays never double-claim. The lock is
// held for the lifetime of the internal transaction; rows are returned as
// hydrated domain events. The caller delivers and then calls MarkDelivered or
// Reschedule, which run in their own statements (at-least-once: a crash between
// claim and mark simply re-delivers later).
func (r *OutboxRepo) ClaimDue(ctx context.Context, limit int, now time.Time) ([]outbox.Event, error) {
	const q = `
SELECT id, event_type, payload, status, attempts, next_attempt_at, created_at, delivered_at
FROM webhook_outbox
WHERE status = 'pending' AND next_attempt_at <= $1
ORDER BY next_attempt_at
LIMIT $2
FOR UPDATE SKIP LOCKED`

	rows, err := r.pool.Query(ctx, q, now, limit)
	if err != nil {
		return nil, wrapErr("OutboxRepo.ClaimDue", err)
	}
	defer rows.Close()

	var out []outbox.Event
	for rows.Next() {
		ev, scanErr := scanOutbox(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, *ev)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapErr("OutboxRepo.ClaimDue.rows", err)
	}
	return out, nil
}

// MarkDelivered flips a row to delivered and stamps delivered_at.
func (r *OutboxRepo) MarkDelivered(ctx context.Context, id ids.OutboxID, at time.Time) error {
	const q = `
UPDATE webhook_outbox
SET status = 'delivered', delivered_at = $2
WHERE id = $1`
	_, err := r.pool.Exec(ctx, q, id.String(), at)
	return wrapErr("OutboxRepo.MarkDelivered", err)
}

// Reschedule bumps attempts and pushes next_attempt_at forward by the backoff.
// The row stays pending (no dead-letter, no expiry).
func (r *OutboxRepo) Reschedule(ctx context.Context, id ids.OutboxID, attempts int, next time.Time) error {
	const q = `
UPDATE webhook_outbox
SET attempts = $2, next_attempt_at = $3
WHERE id = $1`
	_, err := r.pool.Exec(ctx, q, id.String(), attempts, next)
	return wrapErr("OutboxRepo.Reschedule", err)
}

// PendingCount returns the number of rows still awaiting delivery.
func (r *OutboxRepo) PendingCount(ctx context.Context) (int, error) {
	const q = `SELECT count(*) FROM webhook_outbox WHERE status = 'pending'`
	var n int
	if err := r.pool.QueryRow(ctx, q).Scan(&n); err != nil {
		return 0, wrapErr("OutboxRepo.PendingCount", err)
	}
	return n, nil
}

// scanOutbox hydrates an outbox.Event from a row.
func scanOutbox(row pgx.Row) (*outbox.Event, error) {
	var (
		idStr       string
		eventType   string
		payload     []byte
		status      string
		attempts    int
		nextAttempt time.Time
		createdAt   time.Time
		deliveredAt *time.Time
	)
	if err := row.Scan(&idStr, &eventType, &payload, &status, &attempts, &nextAttempt, &createdAt, &deliveredAt); err != nil {
		return nil, wrapErr("OutboxRepo.scan", err)
	}
	id, err := ids.ParseOutboxID(idStr)
	if err != nil {
		return nil, wrapErr("OutboxRepo.scan.id", err)
	}
	var delivered time.Time
	if deliveredAt != nil {
		delivered = *deliveredAt
	}
	return outbox.Hydrate(
		id,
		outbox.EventType(eventType),
		payload,
		outbox.Status(status),
		attempts,
		nextAttempt,
		createdAt,
		delivered,
	), nil
}

// nullableTime returns nil for a zero time so NULL is stored, else the time.
func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
