package eventstream_test

import (
	"context"
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
	s := eventstream.New(0, nil)
	pid := mkPhaseID(t)
	ch, cancel, err := s.Subscribe(context.Background(), pid)
	require.NoError(t, err)
	defer cancel()

	require.NoError(t, s.Publish(context.Background(), pid, ev("phase.started")))
	got := <-ch
	require.Equal(t, "phase.started", got.Type)
}

func TestSubscribe_MultipleSubscribersReceiveSameEvent(t *testing.T) {
	s := eventstream.New(0, nil)
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
	s := eventstream.New(1, dropFn)
	pid := mkPhaseID(t)
	_, cancel, _ := s.Subscribe(context.Background(), pid)
	defer cancel()

	// Buffer = 1; first publish fills it, second drops.
	require.NoError(t, s.Publish(context.Background(), pid, ev("e1")))
	require.NoError(t, s.Publish(context.Background(), pid, ev("e2")))
	require.Equal(t, int64(1), dropped.Load())
}

func TestCancel_RemovesSubscriber(t *testing.T) {
	s := eventstream.New(0, nil)
	pid := mkPhaseID(t)
	_, cancel, _ := s.Subscribe(context.Background(), pid)
	require.Equal(t, 1, s.SubscriberCount(pid))
	cancel()
	require.Equal(t, 0, s.SubscriberCount(pid))
}

func TestCancel_Idempotent(t *testing.T) {
	s := eventstream.New(0, nil)
	pid := mkPhaseID(t)
	_, cancel, _ := s.Subscribe(context.Background(), pid)
	cancel()
	cancel() // second call is a no-op (sync.Once)
	require.Equal(t, 0, s.SubscriberCount(pid))
}

func TestPublish_NoSubscribersIsFine(t *testing.T) {
	s := eventstream.New(0, nil)
	pid := mkPhaseID(t)
	require.NoError(t, s.Publish(context.Background(), pid, ev("phase.started")))
}

func TestPublish_DropFnNilDoesNotPanic(t *testing.T) {
	s := eventstream.New(1, nil)
	pid := mkPhaseID(t)
	_, cancel, _ := s.Subscribe(context.Background(), pid)
	defer cancel()
	require.NoError(t, s.Publish(context.Background(), pid, ev("e1")))
	require.NoError(t, s.Publish(context.Background(), pid, ev("e2"))) // dropped silently
}

func TestSubscribe_DefaultBufferSize(t *testing.T) {
	s := eventstream.New(0, nil) // requests default
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
	s := eventstream.New(0, nil)
	pid := mkPhaseID(t)
	ch, cancel, _ := s.Subscribe(context.Background(), pid)
	cancel()
	_, ok := <-ch
	require.False(t, ok, "channel must be closed after cancel")
}

func TestSubscriberCount_UnknownPhase(t *testing.T) {
	s := eventstream.New(0, nil)
	require.Equal(t, 0, s.SubscriberCount(mkPhaseID(t)))
}

func TestPublishToDifferentPhase_DoesNotReachSubscriber(t *testing.T) {
	s := eventstream.New(0, nil)
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
