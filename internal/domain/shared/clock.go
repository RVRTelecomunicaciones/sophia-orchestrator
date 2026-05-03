// Package shared provides cross-cutting domain primitives that must be
// dependency-injected into use cases: a Clock for deterministic time access
// and an IDGenerator for ULID minting. Lint forbids time.Now() and ulid.Make()
// in domain/application; use these abstractions instead.
package shared

import "time"

// Clock returns the current time. Inject FixedClock in tests, SystemClock in
// production wiring.
type Clock interface {
	Now() time.Time
}

type fixedClock struct{ t time.Time }

func (f fixedClock) Now() time.Time { return f.t }

// FixedClock returns a Clock that always returns t. For tests.
func FixedClock(t time.Time) Clock { return fixedClock{t} }

// SystemClock is the production clock; reads from time.Now().
// This is the only place time.Now is allowed (lint exemption for shared).
type SystemClock struct{}

// Now returns the current wall time.
func (SystemClock) Now() time.Time { return time.Now() } //nolint:forbidigo
