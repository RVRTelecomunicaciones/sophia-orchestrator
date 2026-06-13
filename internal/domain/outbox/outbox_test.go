package outbox_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/outbox"
)

func mustOutboxID(t *testing.T, raw string) ids.OutboxID {
	t.Helper()
	id, err := ids.ParseOutboxID(raw)
	require.NoError(t, err)
	return id
}

// New constructs a pending event with attempts=0 and next_attempt_at=createdAt.
func TestNew_PendingDefaults(t *testing.T) {
	id := mustOutboxID(t, "01ARZ3NDEKTSV4RRFFQ69G5OB1")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	payload := []byte(`{"change_id":"x"}`)

	ev := outbox.New(id, outbox.EventPhaseArchived, payload, now)

	assert.Equal(t, id, ev.ID())
	assert.Equal(t, outbox.EventPhaseArchived, ev.EventType())
	assert.Equal(t, payload, ev.Payload())
	assert.Equal(t, outbox.StatusPending, ev.Status())
	assert.Equal(t, 0, ev.Attempts())
	assert.Equal(t, now, ev.NextAttemptAt())
	assert.Equal(t, now, ev.CreatedAt())
	assert.True(t, ev.DeliveredAt().IsZero())
}

// Status closed enum validation.
func TestStatus_IsValid(t *testing.T) {
	tests := []struct {
		name  string
		s     outbox.Status
		valid bool
	}{
		{"pending", outbox.StatusPending, true},
		{"delivered", outbox.StatusDelivered, true},
		{"unknown rejected", outbox.Status("bogus"), false},
		{"empty rejected", outbox.Status(""), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.valid, tt.s.IsValid())
		})
	}
}

// Hydrate reconstructs a persisted event without re-running validation.
func TestHydrate_RoundTrip(t *testing.T) {
	id := mustOutboxID(t, "01ARZ3NDEKTSV4RRFFQ69G5OB2")
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	next := created.Add(30 * time.Second)
	delivered := created.Add(40 * time.Second)

	ev := outbox.Hydrate(id, outbox.EventPhaseArchived, []byte(`{}`),
		outbox.StatusDelivered, 3, next, created, delivered)

	assert.Equal(t, outbox.StatusDelivered, ev.Status())
	assert.Equal(t, 3, ev.Attempts())
	assert.Equal(t, next, ev.NextAttemptAt())
	assert.Equal(t, delivered, ev.DeliveredAt())
}
