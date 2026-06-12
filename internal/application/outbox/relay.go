// Package outbox contains the relay poller that drains the transactional
// outbox (migration 012) and delivers pending events to memory-engine at
// least once with capped exponential backoff. The relay is pgx-free: it
// depends on a narrow Repository port and an injected delivery function, so
// every behavior is unit-testable with fakes and an injected Clock.
package outbox

import (
	"context"
	"log/slog"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/outbox"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
)

const (
	// backoffBase is the first-retry delay; doubles each attempt.
	backoffBase = 10 * time.Second
	// backoffCeiling caps the computed delay (~5 minutes).
	backoffCeiling = 5 * time.Minute
	// defaultInterval is the relay poll cadence when none is configured.
	defaultInterval = 5 * time.Second
	// defaultBatchLimit bounds how many due rows a single tick claims.
	defaultBatchLimit = 50
)

// Backoff returns the retry delay for the given prior attempt count:
// min(base * 2^attempts, 5m), base 10s. The shift is clamped before it can
// overflow so high attempt counts saturate cleanly at the ceiling.
func Backoff(attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	// 2^attempts can overflow time.Duration (int64 ns) for large attempts;
	// once base<<attempts would exceed the ceiling we can short-circuit. The
	// ceiling is reached by attempts where base*2^attempts >= 5m, i.e.
	// attempts >= 5 (10s*2^5 = 320s > 300s). Guard the shift defensively.
	const maxShift = 6 // 10s << 6 = 640s already past the 5m ceiling
	if attempts >= maxShift {
		return backoffCeiling
	}
	d := backoffBase << uint(attempts)
	if d > backoffCeiling {
		return backoffCeiling
	}
	return d
}

// Repository is the narrow outbox port the relay needs. It is intentionally
// pgx-free; the transactional EnqueueTx lives on the concrete pg repo used by
// the change-completion path, not here.
type Repository interface {
	// ClaimDue returns up to limit pending rows whose next_attempt_at <= now,
	// locking them with FOR UPDATE SKIP LOCKED so concurrent relays do not
	// double-claim.
	ClaimDue(ctx context.Context, limit int, now time.Time) ([]outbox.Event, error)
	// MarkDelivered flips a row to delivered and stamps delivered_at.
	MarkDelivered(ctx context.Context, id ids.OutboxID, at time.Time) error
	// Reschedule bumps attempts and sets the next_attempt_at backoff target.
	Reschedule(ctx context.Context, id ids.OutboxID, attempts int, next time.Time) error
	// PendingCount returns the number of rows still pending delivery.
	PendingCount(ctx context.Context) (int, error)
}

// DeliverFunc performs the synchronous transport (HTTP POST to memory-engine).
// It returns a non-nil error on any non-2xx, timeout, or transport failure.
type DeliverFunc func(ctx context.Context, payload []byte) error

// Deps are the relay's injected collaborators.
type Deps struct {
	Repo       Repository
	Clock      shared.Clock
	Deliver    DeliverFunc
	Interval   time.Duration
	BatchLimit int
}

// Relay periodically drains due outbox rows and delivers them.
type Relay struct {
	repo       Repository
	clock      shared.Clock
	deliver    DeliverFunc
	interval   time.Duration
	batchLimit int
}

// New constructs a Relay, applying defaults for a zero Interval/BatchLimit.
func New(d Deps) *Relay {
	interval := d.Interval
	if interval <= 0 {
		interval = defaultInterval
	}
	limit := d.BatchLimit
	if limit <= 0 {
		limit = defaultBatchLimit
	}
	return &Relay{
		repo:       d.Repo,
		clock:      d.Clock,
		deliver:    d.Deliver,
		interval:   interval,
		batchLimit: limit,
	}
}

// Start runs the relay ticker loop until ctx is cancelled. It mirrors the
// HTTP-server goroutine lifecycle in bootstrap: owned by App.Run, stopped on
// ctx.Done(). Tick errors are logged and swallowed so a transient repo error
// never kills the poller.
func (r *Relay) Start(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("outbox relay stopping", slog.String("reason", ctx.Err().Error()))
			return
		case <-ticker.C:
			if err := r.Tick(ctx); err != nil {
				slog.Warn("outbox relay tick failed",
					slog.String("error", err.Error()),
				)
			}
		}
	}
}

// Tick claims one batch of due rows and delivers each. On 2xx the row is
// marked delivered; on any failure the row stays pending, attempts increments,
// and next_attempt_at advances by the capped backoff. At-least-once: a row is
// never dropped. Returns an error only when the claim itself fails.
func (r *Relay) Tick(ctx context.Context) error {
	now := r.clock.Now()
	events, err := r.repo.ClaimDue(ctx, r.batchLimit, now)
	if err != nil {
		return err //nolint:wrapcheck // surfaced to the loop logger as-is
	}
	if len(events) == 0 {
		return nil
	}

	if pending, perr := r.repo.PendingCount(ctx); perr == nil {
		slog.Info("outbox relay tick",
			slog.Int("outbox.pending_count", pending),
			slog.Int("outbox.claimed", len(events)),
		)
	}

	for i := range events {
		ev := events[i]
		if derr := r.deliver(ctx, ev.Payload()); derr != nil {
			attempts := ev.Attempts() + 1
			next := now.Add(Backoff(attempts))
			slog.Warn("outbox delivery failed; retrying",
				slog.String("outbox.event_id", ev.ID().String()),
				slog.String("outbox.event_type", ev.EventType().String()),
				slog.Int("outbox.attempts", attempts),
				slog.String("outbox.delivery_status", "failed"),
				slog.String("error", derr.Error()),
			)
			if rerr := r.repo.Reschedule(ctx, ev.ID(), attempts, next); rerr != nil {
				slog.Error("outbox reschedule failed",
					slog.String("outbox.event_id", ev.ID().String()),
					slog.String("error", rerr.Error()),
				)
			}
			continue
		}
		if merr := r.repo.MarkDelivered(ctx, ev.ID(), now); merr != nil {
			slog.Error("outbox mark-delivered failed",
				slog.String("outbox.event_id", ev.ID().String()),
				slog.String("error", merr.Error()),
			)
			continue
		}
		slog.Debug("outbox delivered",
			slog.String("outbox.event_id", ev.ID().String()),
			slog.String("outbox.delivery_status", "delivered"),
		)
	}
	return nil
}
