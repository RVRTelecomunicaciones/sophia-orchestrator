// Package bootstrap — rateguard.go provides the RateGuard port and the
// in-memory sliding-window implementation MemoryRateGuard (DG-C7-6).
//
// V1 limitation: the guard is per-process and in-memory. A fleet of
// orchestrator processes each gets its own independent budget. A
// cross-process DB-backed guard is a V2 concern.
package bootstrap

import (
	"context"
	"sync"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
)

// RateGuard decides whether a bootstrap call for the given projectID is
// permitted under the configured quota policy.
//
// All implementations MUST be safe for concurrent use from multiple goroutines.
type RateGuard interface {
	// Allow returns (true, nil) when the call is within quota, or (false, nil)
	// when the quota has been exhausted for the current window. A non-nil error
	// indicates an unexpected internal failure; callers should treat it as
	// denied and log a WARN.
	Allow(ctx context.Context, projectID string) (bool, error)
}

// windowEntry tracks the call timestamps within the sliding window for one
// project. Only timestamps within [now-window, now] are counted.
type windowEntry struct {
	calls []time.Time // ordered by insertion time (oldest first)
}

// MemoryRateGuard is a per-process, in-memory sliding-window rate guard.
// It is configurable via NewMemoryRateGuard and safe for concurrent use.
type MemoryRateGuard struct {
	limit  int
	window time.Duration
	clock  shared.Clock

	mu      sync.Mutex
	entries map[string]*windowEntry
}

// NewMemoryRateGuard constructs a MemoryRateGuard with the given parameters.
//   - limit: maximum allowed calls per project within the window period.
//   - window: the sliding window duration (e.g. 24*time.Hour).
//   - clock: injected clock; use shared.FixedClock in tests, shared.SystemClock{} in production.
func NewMemoryRateGuard(limit int, window time.Duration, clock shared.Clock) *MemoryRateGuard {
	return &MemoryRateGuard{
		limit:   limit,
		window:  window,
		clock:   clock,
		entries: make(map[string]*windowEntry),
	}
}

// Allow checks and potentially records a bootstrap call for projectID.
// It trims timestamps older than the sliding window before counting.
func (g *MemoryRateGuard) Allow(_ context.Context, projectID string) (bool, error) {
	now := g.clock.Now()
	cutoff := now.Add(-g.window)

	g.mu.Lock()
	defer g.mu.Unlock()

	e, ok := g.entries[projectID]
	if !ok {
		e = &windowEntry{}
		g.entries[projectID] = e
	}

	// Trim expired timestamps (calls older than the window boundary).
	valid := e.calls[:0]
	for _, t := range e.calls {
		if !t.Before(cutoff) {
			valid = append(valid, t)
		}
	}
	e.calls = valid

	if len(e.calls) >= g.limit {
		return false, nil
	}

	e.calls = append(e.calls, now)
	return true, nil
}
