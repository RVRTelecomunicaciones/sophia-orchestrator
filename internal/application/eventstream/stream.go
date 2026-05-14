// Package eventstream implements a durable pub/sub used by the SSE HTTP
// endpoint. One Stream serves all changes; subscribers are keyed by PhaseID.
//
// Durability (audit rojo #3 fix): every Publish FIRST appends the event to
// the injected outbound.EventStore (Postgres-backed), then broadcasts
// in-memory. Slow subscribers may still miss the in-memory broadcast and
// are tracked via dropFn — but the event is durable, so a client that
// reconnects with Last-Event-ID can replay everything via EventStore.Replay
// invoked by the SSE handler.
package eventstream

import (
	"context"
	"sync"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// DefaultBufferSize is the default per-subscriber channel size. Tuned for
// the typical SDD phase event rate (~10-20 events/second peak during apply).
const DefaultBufferSize = 32

// Stream is the durable pub/sub implementation of inbound.EventStream.
// EventStore-backed; in-memory channels are an optimization for live
// subscribers — the system of record is the durable store.
type Stream struct {
	mu      sync.RWMutex
	topics  map[ids.PhaseID][]*subscriber
	bufSize int
	store   outbound.EventStore                                          // durable backing
	dropFn  func(phaseID ids.PhaseID, eventType string)                  // metric hook for in-memory drops
	errFn   func(phaseID ids.PhaseID, eventType string, err error)       // metric hook for Append failures
}

type subscriber struct {
	ch chan inbound.Event
}

// New constructs a Stream with the given per-subscriber buffer size. If
// bufSize ≤ 0, DefaultBufferSize is used.
//
// store MUST be non-nil — Stream is durable-by-default. Tests that don't
// care about persistence can pass a Noop implementation
// (see ports/outbound/testdoubles or the embedded NoopEventStore below).
//
// dropFn is invoked when an in-memory broadcast is dropped (subscriber
// channel full); nil drops silently. errFn is invoked when EventStore.Append
// returns an error; nil logs nothing. Both are intended to be wired to
// Prometheus counters in production.
func New(bufSize int, store outbound.EventStore, dropFn func(ids.PhaseID, string), errFn func(ids.PhaseID, string, error)) *Stream {
	if store == nil {
		panic("eventstream.New: store is required (use NoopEventStore for non-durable tests)")
	}
	if bufSize <= 0 {
		bufSize = DefaultBufferSize
	}
	return &Stream{
		topics:  map[ids.PhaseID][]*subscriber{},
		bufSize: bufSize,
		store:   store,
		dropFn:  dropFn,
		errFn:   errFn,
	}
}

// Subscribe registers a subscriber for phaseID and returns a receive channel
// plus a cancel function. The HTTP handler MUST call cancel when its SSE
// connection ends; otherwise the subscriber leaks.
//
// Channel is closed when cancel is invoked. Reads on a closed channel
// return the zero Event with ok=false.
func (s *Stream) Subscribe(_ context.Context, phaseID ids.PhaseID) (<-chan inbound.Event, func(), error) {
	sub := &subscriber{ch: make(chan inbound.Event, s.bufSize)}

	s.mu.Lock()
	s.topics[phaseID] = append(s.topics[phaseID], sub)
	s.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			subs := s.topics[phaseID]
			for i, x := range subs {
				if x == sub {
					s.topics[phaseID] = append(subs[:i], subs[i+1:]...)
					close(sub.ch)
					break
				}
			}
			if len(s.topics[phaseID]) == 0 {
				delete(s.topics, phaseID)
			}
		})
	}
	return sub.ch, cancel, nil
}

// Publish first persists ev via EventStore.Append (assigning ev.Sequence),
// then broadcasts to every active in-memory subscriber of phaseID.
//
// On Append failure: errFn is invoked and the broadcast still proceeds
// with Sequence=0 — degraded mode keeps the live-stream UX usable for
// currently-connected clients even when the DB is briefly unavailable.
// The error is returned so callers can decide whether to surface it
// (the orch-internal publishEvent helpers swallow the return value,
// matching the prior non-blocking behavior).
//
// In-memory broadcast remains non-blocking: subscribers with full
// channels miss the live event and dropFn fires. Those clients still
// recover the event on next reconnect via Last-Event-ID + Replay.
func (s *Stream) Publish(ctx context.Context, phaseID ids.PhaseID, ev inbound.Event) error {
	seq, err := s.store.Append(ctx, phaseID, ev)
	if err != nil {
		if s.errFn != nil {
			s.errFn(phaseID, ev.Type, err)
		}
		// degraded: continue with Sequence=0 so live subscribers still
		// see the event (with the caveat that Last-Event-ID resume
		// cannot recover what's not in the DB).
	} else {
		ev.Sequence = seq
	}

	s.mu.RLock()
	subs := append([]*subscriber{}, s.topics[phaseID]...) // snapshot under read lock
	s.mu.RUnlock()

	for _, sub := range subs {
		select {
		case sub.ch <- ev:
			// delivered
		default:
			if s.dropFn != nil {
				s.dropFn(phaseID, ev.Type)
			}
		}
	}
	return err
}

// SubscriberCount returns the number of active subscribers for phaseID.
// Useful for tests and metrics; not part of the inbound.EventStream contract.
func (s *Stream) SubscriberCount(phaseID ids.PhaseID) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.topics[phaseID])
}

// NoopEventStore is a non-durable EventStore for tests that don't care
// about persistence. Append assigns a monotonic in-memory sequence;
// Replay always returns an empty slice (no history is recoverable).
//
// Production code must use the real outbound.EventStore (pg.EventStore).
type NoopEventStore struct {
	mu  sync.Mutex
	seq int64
}

// Append assigns an in-memory monotonic sequence and discards the event.
func (n *NoopEventStore) Append(_ context.Context, _ ids.PhaseID, _ inbound.Event) (int64, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.seq++
	return n.seq, nil
}

// Replay always returns nil — Noop has no durable history.
func (n *NoopEventStore) Replay(_ context.Context, _ ids.PhaseID, _ int64) ([]inbound.Event, error) {
	return nil, nil
}

// Compile-time interface check.
var _ outbound.EventStore = (*NoopEventStore)(nil)
