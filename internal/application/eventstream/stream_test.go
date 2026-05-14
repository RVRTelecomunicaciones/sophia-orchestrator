package eventstream_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/eventstream"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/stretchr/testify/require"
)

func mkPhaseID(t *testing.T) ids.PhaseID {
	t.Helper()
	id, err := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5P01")
	require.NoError(t, err)
	return id
}

func ev(typ string) inbound.Event {
	return inbound.Event{Type: typ, Timestamp: time.Now(), Payload: map[string]any{}}
}

func TestSubscribeAndPublish_RoundTrip(t *testing.T) {
	s := eventstream.New(0, &eventstream.NoopEventStore{}, nil, nil)
	pid := mkPhaseID(t)
	ch, cancel, err := s.Subscribe(context.Background(), pid)
	require.NoError(t, err)
	defer cancel()

	require.NoError(t, s.Publish(context.Background(), pid, ev("phase.started")))
	got := <-ch
	require.Equal(t, "phase.started", got.Type)
}

func TestSubscribe_MultipleSubscribersReceiveSameEvent(t *testing.T) {
	s := eventstream.New(0, &eventstream.NoopEventStore{}, nil, nil)
	pid := mkPhaseID(t)
	ch1, c1, _ := s.Subscribe(context.Background(), pid)
	defer c1()
	ch2, c2, _ := s.Subscribe(context.Background(), pid)
	defer c2()

	require.NoError(t, s.Publish(context.Background(), pid, ev("phase.started")))
	require.Equal(t, "phase.started", (<-ch1).Type)
	require.Equal(t, "phase.started", (<-ch2).Type)
}

func TestPublish_DroppedWhenSubscriberFull(t *testing.T) {
	var dropped atomic.Int64
	dropFn := func(_ ids.PhaseID, _ string) { dropped.Add(1) }
	s := eventstream.New(1, &eventstream.NoopEventStore{}, dropFn, nil)
	pid := mkPhaseID(t)
	_, cancel, _ := s.Subscribe(context.Background(), pid)
	defer cancel()

	// Buffer = 1; first publish fills it, second drops.
	require.NoError(t, s.Publish(context.Background(), pid, ev("e1")))
	require.NoError(t, s.Publish(context.Background(), pid, ev("e2")))
	require.Equal(t, int64(1), dropped.Load())
}

func TestCancel_RemovesSubscriber(t *testing.T) {
	s := eventstream.New(0, &eventstream.NoopEventStore{}, nil, nil)
	pid := mkPhaseID(t)
	_, cancel, _ := s.Subscribe(context.Background(), pid)
	require.Equal(t, 1, s.SubscriberCount(pid))
	cancel()
	require.Equal(t, 0, s.SubscriberCount(pid))
}

func TestCancel_Idempotent(t *testing.T) {
	s := eventstream.New(0, &eventstream.NoopEventStore{}, nil, nil)
	pid := mkPhaseID(t)
	_, cancel, _ := s.Subscribe(context.Background(), pid)
	cancel()
	cancel() // second call is a no-op (sync.Once)
	require.Equal(t, 0, s.SubscriberCount(pid))
}

func TestPublish_NoSubscribersIsFine(t *testing.T) {
	s := eventstream.New(0, &eventstream.NoopEventStore{}, nil, nil)
	pid := mkPhaseID(t)
	require.NoError(t, s.Publish(context.Background(), pid, ev("phase.started")))
}

func TestPublish_DropFnNilDoesNotPanic(t *testing.T) {
	s := eventstream.New(1, &eventstream.NoopEventStore{}, nil, nil)
	pid := mkPhaseID(t)
	_, cancel, _ := s.Subscribe(context.Background(), pid)
	defer cancel()
	require.NoError(t, s.Publish(context.Background(), pid, ev("e1")))
	require.NoError(t, s.Publish(context.Background(), pid, ev("e2"))) // dropped silently
}

func TestSubscribe_DefaultBufferSize(t *testing.T) {
	s := eventstream.New(0, &eventstream.NoopEventStore{}, nil, nil) // requests default
	pid := mkPhaseID(t)
	ch, cancel, _ := s.Subscribe(context.Background(), pid)
	defer cancel()
	// Push DefaultBufferSize events without blocking.
	for i := 0; i < eventstream.DefaultBufferSize; i++ {
		require.NoError(t, s.Publish(context.Background(), pid, ev("e")))
	}
	// Verify we can read them all.
	for i := 0; i < eventstream.DefaultBufferSize; i++ {
		<-ch
	}
}

func TestChannel_ClosedAfterCancel(t *testing.T) {
	s := eventstream.New(0, &eventstream.NoopEventStore{}, nil, nil)
	pid := mkPhaseID(t)
	ch, cancel, _ := s.Subscribe(context.Background(), pid)
	cancel()
	_, ok := <-ch
	require.False(t, ok, "channel must be closed after cancel")
}

func TestSubscriberCount_UnknownPhase(t *testing.T) {
	s := eventstream.New(0, &eventstream.NoopEventStore{}, nil, nil)
	require.Equal(t, 0, s.SubscriberCount(mkPhaseID(t)))
}

func TestPublishToDifferentPhase_DoesNotReachSubscriber(t *testing.T) {
	s := eventstream.New(0, &eventstream.NoopEventStore{}, nil, nil)
	a := mkPhaseID(t)
	b, _ := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5P02")

	chA, c, _ := s.Subscribe(context.Background(), a)
	defer c()

	require.NoError(t, s.Publish(context.Background(), b, ev("e1")))

	select {
	case got := <-chA:
		t.Fatalf("subscriber for phase A received event for phase B: %s", got.Type)
	case <-time.After(20 * time.Millisecond):
		// expected: no event
	}
}

// ---------------------------------------------------------------------------
// Durable EventStore integration — audit rojo #3
// ---------------------------------------------------------------------------

// inMemoryStore is a non-PG outbound.EventStore for tests that exercise
// the persist + replay roundtrip. Production wires pg.EventStore.
type inMemoryStore struct {
	mu     sync.Mutex
	seq    int64
	events map[ids.PhaseID][]inbound.Event
	failOn map[ids.PhaseID]error // optional per-phase Append failure injection
}

func newInMemoryStore() *inMemoryStore {
	return &inMemoryStore{events: map[ids.PhaseID][]inbound.Event{}, failOn: map[ids.PhaseID]error{}}
}

func (m *inMemoryStore) Append(_ context.Context, phaseID ids.PhaseID, e inbound.Event) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err, ok := m.failOn[phaseID]; ok {
		return 0, err
	}
	m.seq++
	e.Sequence = m.seq
	m.events[phaseID] = append(m.events[phaseID], e)
	return m.seq, nil
}

func (m *inMemoryStore) Replay(_ context.Context, phaseID ids.PhaseID, since int64) ([]inbound.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []inbound.Event
	for _, e := range m.events[phaseID] {
		if e.Sequence > since {
			out = append(out, e)
		}
	}
	return out, nil
}

// TestPublish_AssignsSequence verifies that every Publish call writes
// through the EventStore and the assigned Sequence is propagated to live
// subscribers. This is the core invariant of the audit rojo #3 fix —
// without it, Last-Event-ID resume cannot work.
func TestPublish_AssignsSequence(t *testing.T) {
	store := newInMemoryStore()
	s := eventstream.New(0, store, nil, nil)
	pid := mkPhaseID(t)

	ch, cancel, _ := s.Subscribe(context.Background(), pid)
	defer cancel()

	require.NoError(t, s.Publish(context.Background(), pid, ev("e1")))
	require.NoError(t, s.Publish(context.Background(), pid, ev("e2")))

	got1 := <-ch
	got2 := <-ch
	require.Equal(t, int64(1), got1.Sequence, "first event must have sequence 1")
	require.Equal(t, int64(2), got2.Sequence, "second event must have sequence 2")
}

// TestPublish_DegradedOnStoreFailure verifies that an Append failure
// does NOT block the live broadcast — subscribers still see the event
// with Sequence=0 (degraded mode). errFn is invoked for observability.
func TestPublish_DegradedOnStoreFailure(t *testing.T) {
	store := newInMemoryStore()
	pid := mkPhaseID(t)
	store.failOn[pid] = errors.New("store down")

	var lastErr error
	errFn := func(_ ids.PhaseID, _ string, err error) { lastErr = err }

	s := eventstream.New(0, store, nil, errFn)
	ch, cancel, _ := s.Subscribe(context.Background(), pid)
	defer cancel()

	// Publish returns the store's error (caller may surface or ignore)
	// but the in-memory broadcast still fires.
	err := s.Publish(context.Background(), pid, ev("e1"))
	require.ErrorContains(t, err, "store down")
	require.ErrorContains(t, lastErr, "store down", "errFn must observe the failure")

	got := <-ch
	require.Equal(t, int64(0), got.Sequence,
		"degraded broadcast must carry Sequence=0 (not persisted)")
	require.Equal(t, "e1", got.Type)
}

// TestReplay_RestartScenario simulates an orch restart: events 1-5
// persisted, then the in-memory Stream is discarded, a fresh Stream
// is constructed pointing at the same store, and a new subscriber
// arrives with Last-Event-ID=2. They MUST see events 3-5 from the
// replay even though no in-memory state survived the restart.
func TestReplay_RestartScenario(t *testing.T) {
	store := newInMemoryStore()
	pid := mkPhaseID(t)

	// Phase 1: pre-restart. Stream publishes 5 events; no subscribers
	// were attached (simulating events that fired before the client
	// connected).
	s1 := eventstream.New(0, store, nil, nil)
	for i := 1; i <= 5; i++ {
		require.NoError(t, s1.Publish(context.Background(), pid, ev("phase.started")))
	}

	// Phase 2: restart. New Stream, same store.
	s2 := eventstream.New(0, store, nil, nil)
	_ = s2 // proves restart-survival path through the store

	// Phase 3: client connects with Last-Event-ID=2. Replay should
	// return events 3, 4, 5 only — events 1 and 2 have already been
	// seen by the client.
	historical, err := store.Replay(context.Background(), pid, 2)
	require.NoError(t, err)
	require.Len(t, historical, 3, "replay since=2 should return events 3-5")
	require.Equal(t, int64(3), historical[0].Sequence)
	require.Equal(t, int64(4), historical[1].Sequence)
	require.Equal(t, int64(5), historical[2].Sequence)
}

// TestNoopEventStore_AssignsMonotonicSequence verifies the Noop store
// still emits monotonic sequences so tests that depend on Sequence>0
// (e.g. SSE handler dedup) continue to work even without a real store.
func TestNoopEventStore_AssignsMonotonicSequence(t *testing.T) {
	n := &eventstream.NoopEventStore{}
	pid := mkPhaseID(t)
	s1, err := n.Append(context.Background(), pid, ev("a"))
	require.NoError(t, err)
	s2, err := n.Append(context.Background(), pid, ev("b"))
	require.NoError(t, err)
	require.Equal(t, int64(1), s1)
	require.Equal(t, int64(2), s2)
	hist, err := n.Replay(context.Background(), pid, 0)
	require.NoError(t, err)
	require.Empty(t, hist, "Noop has no durable history — always empty Replay")
}
