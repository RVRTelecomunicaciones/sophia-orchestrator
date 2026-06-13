package outbox_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	domainoutbox "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/outbox"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	apprelay "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/outbox"
)

// ── Backoff schedule ─────────────────────────────────────────────────────────

func TestBackoff_Schedule(t *testing.T) {
	const base = 10 * time.Second
	const ceiling = 5 * time.Minute
	tests := []struct {
		name     string
		attempts int
		want     time.Duration
	}{
		{"first retry (attempts=0)", 0, 10 * time.Second},
		{"attempts=1", 1, 20 * time.Second},
		{"attempts=2", 2, 40 * time.Second},
		{"attempts=3", 3, 80 * time.Second},
		{"attempts=4", 4, 160 * time.Second},
		{"attempts=5 clamps below ceiling", 5, 300 * time.Second},
		{"attempts=6 clamped at ceiling", 6, ceiling},
		{"attempts=20 clamped at ceiling", 20, ceiling},
		{"attempts=100 no overflow, clamped", 100, ceiling},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := apprelay.Backoff(tt.attempts)
			assert.Equal(t, tt.want, got, "backoff(%d)", tt.attempts)
			assert.LessOrEqual(t, got, ceiling, "must never exceed 5m ceiling")
			assert.GreaterOrEqual(t, got, base, "must never be below base 10s")
		})
	}
}

// ── Fakes ────────────────────────────────────────────────────────────────────

type fakeRepo struct {
	due []domainoutbox.Event

	claimLimit    int
	claimNow      time.Time
	claimErr      error
	markedID      ids.OutboxID
	markedAt      time.Time
	markErr       error
	rescheduledID ids.OutboxID
	rescheduledTo time.Time
	rescheduledAt int
	rescheduleErr error
	pending       int
}

func (f *fakeRepo) ClaimDue(_ context.Context, limit int, now time.Time) ([]domainoutbox.Event, error) {
	f.claimLimit = limit
	f.claimNow = now
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	return f.due, nil
}

func (f *fakeRepo) MarkDelivered(_ context.Context, id ids.OutboxID, at time.Time) error {
	f.markedID = id
	f.markedAt = at
	return f.markErr
}

func (f *fakeRepo) Reschedule(_ context.Context, id ids.OutboxID, attempts int, next time.Time) error {
	f.rescheduledID = id
	f.rescheduledAt = attempts
	f.rescheduledTo = next
	return f.rescheduleErr
}

func (f *fakeRepo) PendingCount(_ context.Context) (int, error) {
	return f.pending, nil
}

func sampleEvent(t *testing.T, attempts int) domainoutbox.Event {
	t.Helper()
	id, err := ids.ParseOutboxID("01ARZ3NDEKTSV4RRFFQ69G5OB1")
	require.NoError(t, err)
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return *domainoutbox.Hydrate(id, domainoutbox.EventPhaseArchived, []byte(`{"change_id":"c1"}`),
		domainoutbox.StatusPending, attempts, created, created, time.Time{})
}

// ── One-tick behavior ────────────────────────────────────────────────────────

func TestTick_Success_MarksDelivered(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	repo := &fakeRepo{due: []domainoutbox.Event{sampleEvent(t, 0)}}
	var delivered []byte
	r := apprelay.New(apprelay.Deps{
		Repo:       repo,
		Clock:      shared.FixedClock(now),
		Deliver:    func(_ context.Context, payload []byte) error { delivered = payload; return nil },
		Interval:   5 * time.Second,
		BatchLimit: 50,
	})

	require.NoError(t, r.Tick(context.Background()))

	wantID, _ := ids.ParseOutboxID("01ARZ3NDEKTSV4RRFFQ69G5OB1")
	assert.Equal(t, []byte(`{"change_id":"c1"}`), delivered)
	assert.Equal(t, wantID, repo.markedID, "delivered row must be marked")
	assert.Equal(t, now, repo.markedAt)
	assert.True(t, repo.rescheduledID.IsZero(), "success must not reschedule")
	assert.Equal(t, 50, repo.claimLimit)
}

func TestTick_Failure_Reschedules(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	repo := &fakeRepo{due: []domainoutbox.Event{sampleEvent(t, 2)}}
	r := apprelay.New(apprelay.Deps{
		Repo:       repo,
		Clock:      shared.FixedClock(now),
		Deliver:    func(_ context.Context, _ []byte) error { return errors.New("ME down") },
		Interval:   5 * time.Second,
		BatchLimit: 50,
	})

	require.NoError(t, r.Tick(context.Background()))

	wantID, _ := ids.ParseOutboxID("01ARZ3NDEKTSV4RRFFQ69G5OB1")
	assert.True(t, repo.markedID.IsZero(), "failure must not mark delivered")
	assert.Equal(t, wantID, repo.rescheduledID)
	assert.Equal(t, 3, repo.rescheduledAt, "attempts must increment to 3")
	assert.Equal(t, now.Add(apprelay.Backoff(3)), repo.rescheduledTo, "next = now + backoff(attempts+1)")
}

func TestTick_EmptyClaim_NoOp(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	repo := &fakeRepo{due: nil}
	r := apprelay.New(apprelay.Deps{
		Repo:       repo,
		Clock:      shared.FixedClock(now),
		Deliver:    func(_ context.Context, _ []byte) error { t.Fatal("deliver must not be called on empty claim"); return nil },
		Interval:   5 * time.Second,
		BatchLimit: 50,
	})

	require.NoError(t, r.Tick(context.Background()))
	assert.True(t, repo.markedID.IsZero())
	assert.True(t, repo.rescheduledID.IsZero())
}

func TestStart_StopsOnContextCancel(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	repo := &fakeRepo{due: nil}
	r := apprelay.New(apprelay.Deps{
		Repo:       repo,
		Clock:      shared.FixedClock(now),
		Deliver:    func(_ context.Context, _ []byte) error { return nil },
		Interval:   1 * time.Millisecond,
		BatchLimit: 50,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Start(ctx); close(done) }()
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after context cancel")
	}
}
