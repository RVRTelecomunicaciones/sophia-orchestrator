// Package outbound declares the interfaces that application services depend
// on. Concrete implementations live under internal/adapters/outbound/.
//
// SpawnGovernorRepo is the persistence-side counter and lock backend for the
// Discipline.SpawnGovernor (rate limiter for AI dispatcher subprocesses).
// V1 implementation: Postgres advisory lock + counter row (see ADR-0003-style
// storage in spawn_governor_state). V2: optional Redis backend.
package outbound

import "context"

// SpawnGovernorRepo persists the active-spawn count and provides atomic
// acquire/release operations under the configured Max cap.
type SpawnGovernorRepo interface {
	// Acquire attempts to reserve a slot. Returns (acquired, currentCount, err).
	// Acquired is true iff the active count was < max BEFORE this call and
	// the row has now been incremented.
	Acquire(ctx context.Context, max int) (acquired bool, current int, err error)

	// Release decrements the active count atomically. Idempotent: releasing
	// while the count is 0 is a no-op (logged), not an error.
	Release(ctx context.Context) error

	// Active returns the current active count without modifying it.
	Active(ctx context.Context) (int, error)
}
