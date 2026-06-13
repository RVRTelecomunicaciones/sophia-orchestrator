//go:build integration

package pg_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/pg"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/webhook"
	outboxapp "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/outbox"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/outbox"
)

// mutableClock is a test Clock whose Now() can be advanced deterministically
// between relay ticks. It is the synchronization primitive for this end-to-end
// test: instead of sleeping for the backoff to elapse, the test moves the clock
// forward so the rescheduled row becomes due. Safe for concurrent reads.
type mutableClock struct {
	mu  sync.Mutex
	now time.Time
}

func newMutableClock(t time.Time) *mutableClock { return &mutableClock{now: t} }

func (c *mutableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mutableClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeME is an httptest-backed memory-engine receiver with a toggle. While
// "down" it returns 503 so the relay reschedules the outbox row; once flipped
// "up" it returns 200 and records the delivered body. A channel signals the
// first successful delivery so the test never sleeps to observe it.
type fakeME struct {
	mu        sync.Mutex
	up        bool
	delivered [][]byte
	gotBody   chan []byte
}

func newFakeME() *fakeME {
	return &fakeME{gotBody: make(chan []byte, 1)}
}

func (f *fakeME) setUp(up bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.up = up
}

func (f *fakeME) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	f.mu.Lock()
	up := f.up
	f.mu.Unlock()

	if !up {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	f.mu.Lock()
	f.delivered = append(f.delivered, body)
	f.mu.Unlock()
	w.WriteHeader(http.StatusOK)

	select {
	case f.gotBody <- body:
	default:
	}
}

// TestOutboxRelay_EndToEnd_MEDownThenUp drives the full relay path against a
// real Postgres outbox and a real webhook adapter pointed at a togglable fake
// memory-engine:
//
//	1. enqueue a pending phase.archived row (the change-completion seam);
//	2. ME is DOWN — relay tick delivers, gets 503, leaves the row pending with
//	   attempts incremented and next_attempt_at pushed out by the capped backoff;
//	3. advance the clock past the backoff and bring ME UP — the next tick claims
//	   the now-due row, delivers it (byte-identical payload), and marks it
//	   delivered so PendingCount drops to zero.
//
// Synchronization is clock- and channel-based: no time.Sleep for the retry
// window, no polling for delivery.
func TestOutboxRelay_EndToEnd_MEDownThenUp(t *testing.T) {
	pool := setupSkillPG(t)
	ctx := context.Background()
	repo := pg.NewOutboxRepo(pool)

	me := newFakeME()
	srv := httptest.NewServer(me)
	t.Cleanup(srv.Close)

	adapter := webhook.New(webhook.Config{
		URL:     srv.URL,
		APIKey:  "test-key",
		Timeout: 2 * time.Second,
	})

	start := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	clock := newMutableClock(start)

	relay := outboxapp.New(outboxapp.Deps{
		Repo:       repo,
		Clock:      clock,
		Deliver:    adapter.Deliver,
		Interval:   time.Hour, // unused: we drive Tick directly
		BatchLimit: 50,
	})

	// 1. Enqueue a due pending event (mirrors SaveCompletedWithOutbox's INSERT).
	payload := []byte(`{"change_id":"c-e2e","change_name":"loop-hardening","phase_type":"archived"}`)
	ev := outbox.New(
		mustOutboxIDIntegration(t, "01ARZ3NDEKTSV4RRFFQ69G5OEE"),
		outbox.EventPhaseArchived,
		payload,
		start,
	)
	enqueue(t, ctx, pool, repo, ev)

	// 2. ME DOWN: tick claims and delivers, gets 503, row stays pending.
	me.setUp(false)
	require.NoError(t, relay.Tick(ctx))

	var status string
	var attempts int
	var nextAttempt time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status, attempts, next_attempt_at FROM webhook_outbox WHERE id=$1`,
		ev.ID().String()).Scan(&status, &attempts, &nextAttempt))
	require.Equal(t, "pending", status, "failed delivery must leave the row pending")
	require.Equal(t, 1, attempts, "failed delivery must increment attempts")
	require.True(t, nextAttempt.After(start),
		"failed delivery must push next_attempt_at out by the backoff")

	cnt, err := repo.PendingCount(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, cnt, "event is still pending after ME-down tick")

	// A tick BEFORE the backoff elapses must not re-claim the row (not yet due).
	require.NoError(t, relay.Tick(ctx))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT attempts FROM webhook_outbox WHERE id=$1`, ev.ID().String()).Scan(&attempts))
	require.Equal(t, 1, attempts, "row is not due yet — attempts must not advance")

	// 3. ME UP + clock advanced past the backoff: row becomes due, delivers.
	me.setUp(true)
	clock.advance(outboxapp.Backoff(1) + time.Second)
	require.NoError(t, relay.Tick(ctx))

	// The fake ME signals the delivered body on its channel — assert byte-identity
	// without sleeping.
	select {
	case got := <-me.gotBody:
		require.Equal(t, payload, got, "delivered payload must be byte-identical to the enqueued event")
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not deliver to memory-engine after it came back up")
	}

	// Row is now delivered and no longer pending.
	var deliveredAt *time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status, delivered_at FROM webhook_outbox WHERE id=$1`,
		ev.ID().String()).Scan(&status, &deliveredAt))
	require.Equal(t, "delivered", status)
	require.NotNil(t, deliveredAt, "delivered_at must be stamped")

	cnt, err = repo.PendingCount(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, cnt, "delivered event must no longer be pending")
}
