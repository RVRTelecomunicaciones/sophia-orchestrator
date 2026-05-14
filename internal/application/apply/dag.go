package apply

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
)

// DAGCoordinator implements in-process group-dependency signaling for apply
// phase parallel coordination (spec § 5 step 9 — "Group N complete" mailbox
// broadcast). V1 uses Go channels keyed by GroupID. V1.5 (when runtime
// Phase 2 ships mailbox.broadcast/read_inbox capabilities) swaps the
// channel implementation for runtime calls — the public Wait/Signal API
// stays stable so call sites don't change.
type DAGCoordinator struct {
	mu       sync.RWMutex
	channels map[ids.GroupID]chan groupResult
}

// groupResult is the signal payload broadcast on a group completion. The
// failed flag lets dependent groups distinguish "upstream succeeded, my
// work can begin" from "upstream failed, abort cascading".
type groupResult struct {
	failed bool
	err    error
}

// NewDAGCoordinator builds a coordinator pre-populated with one channel per
// group in the board. Each channel is buffered so a Signal call never
// blocks the producer (the signal is fire-and-forget).
func NewDAGCoordinator(groups []*apply.Group) *DAGCoordinator {
	d := &DAGCoordinator{channels: map[ids.GroupID]chan groupResult{}}
	for _, g := range groups {
		d.channels[g.ID()] = make(chan groupResult, 1)
	}
	return d
}

// Signal broadcasts that group has completed (failed=true if it errored).
// Idempotent: subsequent calls for the same group are dropped (the
// buffered channel only accepts one value; second send is non-blocking).
func (d *DAGCoordinator) Signal(g ids.GroupID, failed bool, err error) {
	d.mu.RLock()
	ch, ok := d.channels[g]
	d.mu.RUnlock()
	if !ok {
		return
	}
	select {
	case ch <- groupResult{failed: failed, err: err}:
	default:
	}
}

// Wait blocks until every group in deps has signaled, ctx is cancelled, or
// timeout elapses. Returns nil when all dependencies completed
// successfully; returns ErrDependencyTimeout / context.Err / wrapped
// upstream-failure error otherwise.
func (d *DAGCoordinator) Wait(ctx context.Context, deps []ids.GroupID, timeout time.Duration) error {
	if len(deps) == 0 {
		return nil
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for _, dep := range deps {
		d.mu.RLock()
		ch, ok := d.channels[dep]
		d.mu.RUnlock()
		if !ok {
			return fmt.Errorf("apply.DAGCoordinator: unknown dep %s", dep.String())
		}
		select {
		case res := <-ch:
			// Re-publish the signal so other dependents (or our own
			// re-reads) can still see it.
			d.Signal(dep, res.failed, res.err)
			if res.failed {
				return fmt.Errorf("%w: dependency %s failed: %w", ErrGroupFailed, dep.String(), res.err)
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("%w: waited %v for %s", ErrDependencyTimeout, timeout, dep.String())
		}
	}
	return nil
}
