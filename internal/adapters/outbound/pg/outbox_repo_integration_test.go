//go:build integration

package pg_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/pg"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/outbox"
)

func mustOutboxIDIntegration(t *testing.T, raw string) ids.OutboxID {
	t.Helper()
	id, err := ids.ParseOutboxID(raw)
	require.NoError(t, err)
	return id
}

// EnqueueTx commits with the caller tx; a rollback leaves no row.
func TestOutboxRepo_EnqueueTx_RollbackLeavesNoRow(t *testing.T) {
	pool := setupSkillPG(t)
	ctx := context.Background()
	repo := pg.NewOutboxRepo(pool)

	id := mustOutboxIDIntegration(t, "01ARZ3NDEKTSV4RRFFQ69G5OB1")
	now := time.Now().UTC()
	ev := outbox.New(id, outbox.EventPhaseArchived, []byte(`{"change_id":"c1"}`), now)

	// Begin a tx, enqueue, then ROLLBACK.
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, repo.EnqueueTx(ctx, tx, ev))
	require.NoError(t, tx.Rollback(ctx))

	cnt, err := repo.PendingCount(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, cnt, "rolled-back enqueue must leave no row")

	// Now commit a second enqueue and assert it persists.
	tx2, err := pool.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, repo.EnqueueTx(ctx, tx2, ev))
	require.NoError(t, tx2.Commit(ctx))

	cnt, err = repo.PendingCount(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, cnt, "committed enqueue must persist exactly one pending row")
}

// ClaimDue returns only due pending rows and respects FOR UPDATE SKIP LOCKED.
func TestOutboxRepo_ClaimDue_OnlyDuePending(t *testing.T) {
	pool := setupSkillPG(t)
	ctx := context.Background()
	repo := pg.NewOutboxRepo(pool)

	now := time.Now().UTC()
	due := outbox.New(mustOutboxIDIntegration(t, "01ARZ3NDEKTSV4RRFFQ69G5OB1"),
		outbox.EventPhaseArchived, []byte(`{"k":"due"}`), now.Add(-time.Minute))
	future := outbox.New(mustOutboxIDIntegration(t, "01ARZ3NDEKTSV4RRFFQ69G5OB2"),
		outbox.EventPhaseArchived, []byte(`{"k":"future"}`), now.Add(time.Hour))

	enqueue(t, ctx, pool, repo, due)
	enqueue(t, ctx, pool, repo, future)

	got, err := repo.ClaimDue(ctx, 50, now)
	require.NoError(t, err)
	require.Len(t, got, 1, "only the due row is claimed")
	require.Equal(t, due.ID(), got[0].ID())
}

// Two concurrent claimers must never see the same due row.
func TestOutboxRepo_ClaimDue_SkipLockedNoDoubleClaim(t *testing.T) {
	pool := setupSkillPG(t)
	ctx := context.Background()
	repo := pg.NewOutboxRepo(pool)

	now := time.Now().UTC()
	a := outbox.New(mustOutboxIDIntegration(t, "01ARZ3NDEKTSV4RRFFQ69G5OB1"),
		outbox.EventPhaseArchived, []byte(`{"k":"a"}`), now.Add(-time.Minute))
	b := outbox.New(mustOutboxIDIntegration(t, "01ARZ3NDEKTSV4RRFFQ69G5OB2"),
		outbox.EventPhaseArchived, []byte(`{"k":"b"}`), now.Add(-time.Minute))
	enqueue(t, ctx, pool, repo, a)
	enqueue(t, ctx, pool, repo, b)

	// Claimer 1 opens a tx and locks one row, holding the lock.
	const claimQ = `
SELECT id FROM webhook_outbox
WHERE status='pending' AND next_attempt_at <= $1
ORDER BY next_attempt_at
LIMIT 1
FOR UPDATE SKIP LOCKED`

	tx1, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx1.Rollback(ctx) }()
	var locked1 string
	require.NoError(t, tx1.QueryRow(ctx, claimQ, now).Scan(&locked1))

	// Claimer 2 (separate tx) must skip the locked row and grab the other.
	tx2, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx2.Rollback(ctx) }()
	var locked2 string
	require.NoError(t, tx2.QueryRow(ctx, claimQ, now).Scan(&locked2))

	require.NotEqual(t, locked1, locked2, "SKIP LOCKED must hand each claimer a distinct row")
}

// MarkDelivered flips status and sets delivered_at; Reschedule bumps attempts.
func TestOutboxRepo_MarkDelivered_And_Reschedule(t *testing.T) {
	pool := setupSkillPG(t)
	ctx := context.Background()
	repo := pg.NewOutboxRepo(pool)

	now := time.Now().UTC()
	ev := outbox.New(mustOutboxIDIntegration(t, "01ARZ3NDEKTSV4RRFFQ69G5OB1"),
		outbox.EventPhaseArchived, []byte(`{"k":"v"}`), now.Add(-time.Minute))
	enqueue(t, ctx, pool, repo, ev)

	// Reschedule: attempts -> 1, next_attempt_at advanced, still pending.
	next := now.Add(20 * time.Second)
	require.NoError(t, repo.Reschedule(ctx, ev.ID(), 1, next))
	var status string
	var attempts int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status, attempts FROM webhook_outbox WHERE id=$1`, ev.ID().String()).
		Scan(&status, &attempts))
	require.Equal(t, "pending", status)
	require.Equal(t, 1, attempts)

	// MarkDelivered: status -> delivered, delivered_at set.
	require.NoError(t, repo.MarkDelivered(ctx, ev.ID(), now))
	var deliveredAt *time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status, delivered_at FROM webhook_outbox WHERE id=$1`, ev.ID().String()).
		Scan(&status, &deliveredAt))
	require.Equal(t, "delivered", status)
	require.NotNil(t, deliveredAt)

	// Delivered rows are no longer claimed.
	got, err := repo.ClaimDue(ctx, 50, now)
	require.NoError(t, err)
	require.Empty(t, got)

	cnt, err := repo.PendingCount(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, cnt)
}

// enqueue commits a single outbox row via a short-lived tx.
func enqueue(t *testing.T, ctx context.Context, pool interface {
	Begin(context.Context) (pgx.Tx, error)
}, repo *pg.OutboxRepo, ev *outbox.Event) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, repo.EnqueueTx(ctx, tx, ev))
	require.NoError(t, tx.Commit(ctx))
}
