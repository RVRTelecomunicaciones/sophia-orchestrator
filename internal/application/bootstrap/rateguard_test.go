package bootstrap_test

// T5.1 — RED tests for MemoryRateGuard.
//
// Scenario IDs:
//   RG1 — 5 calls allowed, 6th denied (default limit)
//   RG2 — counter is per-project (project B unaffected by A's exhaustion)
//   RG3 — advancing clock past 24h window re-allows calls
//   RG4 — concurrent Allow calls are race-safe (-race)

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/bootstrap"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
)

// advanceClock is a test-only shared.Clock that starts at a base time and
// can be advanced via Advance(d). Safe for concurrent use.
type advanceClock struct {
	mu  sync.Mutex
	now time.Time
}

func newAdvanceClock(base time.Time) *advanceClock { return &advanceClock{now: base} }

func (c *advanceClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *advanceClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

var _ shared.Clock = (*advanceClock)(nil)

// base time used across tests.
var baseTime = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

// RG1 — default max=5; calls 1..5 allowed, 6th denied.
func TestMemoryRateGuard_DefaultLimit(t *testing.T) {
	t.Parallel()
	clk := newAdvanceClock(baseTime)
	g := bootstrap.NewMemoryRateGuard(5, 24*time.Hour, clk)

	ctx := context.Background()
	const projectA = "proj-a"

	for i := 1; i <= 5; i++ {
		ok, err := g.Allow(ctx, projectA)
		require.NoError(t, err, "call %d should not error", i)
		assert.True(t, ok, "call %d should be allowed", i)
	}

	// 6th call must be denied.
	ok, err := g.Allow(ctx, projectA)
	require.NoError(t, err)
	assert.False(t, ok, "6th call must be denied")
}

// RG2 — per-project isolation: exhausting project A must not affect project B.
func TestMemoryRateGuard_PerProject(t *testing.T) {
	t.Parallel()
	clk := newAdvanceClock(baseTime)
	g := bootstrap.NewMemoryRateGuard(2, 24*time.Hour, clk)

	ctx := context.Background()

	// Exhaust project A.
	for i := 0; i < 2; i++ {
		ok, err := g.Allow(ctx, "proj-a")
		require.NoError(t, err)
		require.True(t, ok)
	}
	denied, err := g.Allow(ctx, "proj-a")
	require.NoError(t, err)
	assert.False(t, denied, "proj-a should be exhausted")

	// Project B must still be allowed.
	ok, err := g.Allow(ctx, "proj-b")
	require.NoError(t, err)
	assert.True(t, ok, "proj-b must be unaffected by proj-a exhaustion")
}

// RG3 — advancing the clock past the window re-allows calls.
func TestMemoryRateGuard_WindowExpiry(t *testing.T) {
	t.Parallel()
	window := 24 * time.Hour
	clk := newAdvanceClock(baseTime)
	g := bootstrap.NewMemoryRateGuard(1, window, clk)

	ctx := context.Background()
	const project = "proj-expiry"

	// Use the single allowed call.
	ok, err := g.Allow(ctx, project)
	require.NoError(t, err)
	require.True(t, ok)

	// Next call immediately is denied.
	ok, err = g.Allow(ctx, project)
	require.NoError(t, err)
	assert.False(t, ok, "should be denied before window expires")

	// Advance clock just past the window.
	clk.Advance(window + time.Second)

	// Now it must be allowed again.
	ok, err = g.Allow(ctx, project)
	require.NoError(t, err)
	assert.True(t, ok, "must be re-allowed after window expiry")
}

// RG4 — concurrent Allow calls must be race-safe (requires -race).
func TestMemoryRateGuard_ConcurrentRaceSafe(t *testing.T) {
	t.Parallel()
	clk := newAdvanceClock(baseTime)
	const limit = 5
	g := bootstrap.NewMemoryRateGuard(limit, 24*time.Hour, clk)

	ctx := context.Background()
	const project = "proj-race"
	const goroutines = 20

	var allowed atomic.Int64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			ok, err := g.Allow(ctx, project)
			if err == nil && ok {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	// Exactly limit calls must have been allowed.
	assert.Equal(t, int64(limit), allowed.Load(), "exactly limit=%d calls should be allowed under concurrency", limit)
}
