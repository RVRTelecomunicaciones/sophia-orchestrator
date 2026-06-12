// Package outbox provides the Event domain entity for the transactional
// outbox (migration 012). An Event is INSERTed in the same transaction that
// completes a change and is later delivered at-least-once by the relay poller.
// All time and ID inputs are injected (Clock / IDGenerator) per repo
// convention: no time.Now() or ulid.Make() in the domain layer.
package outbox

import (
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
)

// EventType is the logical name of an outbox event. The outbox table is generic;
// the only producer in V1 is the phase.archived webhook.
type EventType string

// EventPhaseArchived is the single V1 producer: orch→ME phase.archived delivery.
const EventPhaseArchived EventType = "phase.archived"

// String returns the underlying string value.
func (e EventType) String() string { return string(e) }

// Status is the closed delivery-state enum, matching the SQL CHECK constraint
// in 012_outbox.up.sql: 'pending' | 'delivered'. There is no dead-letter or
// expiry state — a row stays pending until delivered.
type Status string

// Status values — must match the SQL CHECK constraint in 012_outbox.up.sql.
const (
	StatusPending   Status = "pending"
	StatusDelivered Status = "delivered"
)

// IsValid reports whether s is one of the two closed enum values.
func (s Status) IsValid() bool {
	switch s {
	case StatusPending, StatusDelivered:
		return true
	}
	return false
}

// String returns the underlying string value.
func (s Status) String() string { return string(s) }

// Event is a single durable outbound delivery record. Fields are unexported
// with public getters per project convention.
type Event struct {
	id            ids.OutboxID
	eventType     EventType
	payload       []byte
	status        Status
	attempts      int
	nextAttemptAt time.Time
	createdAt     time.Time
	deliveredAt   time.Time // zero value while pending
}

// New constructs a pending Event with attempts=0 and next_attempt_at=createdAt
// (immediately due). The payload is delivered verbatim to memory-engine.
func New(id ids.OutboxID, eventType EventType, payload []byte, createdAt time.Time) *Event {
	return &Event{
		id:            id,
		eventType:     eventType,
		payload:       payload,
		status:        StatusPending,
		attempts:      0,
		nextAttemptAt: createdAt,
		createdAt:     createdAt,
	}
}

// Hydrate reconstructs an Event from persisted storage without re-running
// validation. The persistence layer is trusted to have stored only valid data.
func Hydrate(
	id ids.OutboxID,
	eventType EventType,
	payload []byte,
	status Status,
	attempts int,
	nextAttemptAt time.Time,
	createdAt time.Time,
	deliveredAt time.Time,
) *Event {
	return &Event{
		id:            id,
		eventType:     eventType,
		payload:       payload,
		status:        status,
		attempts:      attempts,
		nextAttemptAt: nextAttemptAt,
		createdAt:     createdAt,
		deliveredAt:   deliveredAt,
	}
}

// ── Getters ──────────────────────────────────────────────────────────────────

// ID returns the outbox event identifier.
func (e *Event) ID() ids.OutboxID { return e.id }

// EventType returns the logical event name.
func (e *Event) EventType() EventType { return e.eventType }

// Payload returns the JSON body to deliver verbatim.
func (e *Event) Payload() []byte { return e.payload }

// Status returns the current delivery status.
func (e *Event) Status() Status { return e.status }

// Attempts returns the number of delivery attempts so far.
func (e *Event) Attempts() int { return e.attempts }

// NextAttemptAt returns the earliest time the relay may (re)claim this row.
func (e *Event) NextAttemptAt() time.Time { return e.nextAttemptAt }

// CreatedAt returns the immutable enqueue timestamp.
func (e *Event) CreatedAt() time.Time { return e.createdAt }

// DeliveredAt returns the delivery timestamp, or the zero time while pending.
func (e *Event) DeliveredAt() time.Time { return e.deliveredAt }
