// Package eventstream implements an in-memory pub/sub used by the SSE HTTP
// endpoint. One Stream serves all changes; subscribers are keyed by PhaseID.
// Slow subscribers are dropped (non-blocking publish) to protect the
// orchestrator's goroutines.
package eventstream

import (
	"context"
	"sync"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
)

// DefaultBufferSize is the default per-subscriber channel size. Tuned for
// the typical SDD phase event rate (~10-20 events/second peak during apply).
const DefaultBufferSize = 32

// Stream is an in-memory pub/sub implementation of inbound.EventStream.
type Stream struct {
	mu      sync.RWMutex
	topics  map[ids.PhaseID][]*subscriber
	bufSize int
	dropFn  func(phaseID ids.PhaseID, eventType string) // metric hook
}

type subscriber struct {
	ch chan inbound.Event
}

// New constructs a Stream with the given per-subscriber buffer size. If
// bufSize ≤ 0, DefaultBufferSize is used. dropFn is invoked when an event
// is dropped because a subscriber's channel is full (use to bump metrics);
// nil drops silently.
func New(bufSize int, dropFn func(ids.PhaseID, string)) *Stream {
	if bufSize <= 0 {
		bufSize = DefaultBufferSize
	}
	return &Stream{
		topics:  map[ids.PhaseID][]*subscriber{},
		bufSize: bufSize,
		dropFn:  dropFn,
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

// Publish broadcasts ev to every active subscriber of phaseID. Non-blocking:
// if a subscriber's channel is full the event is dropped and dropFn is called.
func (s *Stream) Publish(_ context.Context, phaseID ids.PhaseID, ev inbound.Event) error {
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
	return nil
}

// SubscriberCount returns the number of active subscribers for phaseID.
// Useful for tests and metrics; not part of the inbound.EventStream contract.
func (s *Stream) SubscriberCount(phaseID ids.PhaseID) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.topics[phaseID])
}
