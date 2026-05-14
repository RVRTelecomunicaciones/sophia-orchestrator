package inbound

import (
	"context"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
)

// Event is one SSE event emitted by the orchestrator during a phase run.
//
// Type MUST be one of the constants declared in event_types.go (Event*).
// Free-form strings compile but are an audit risk — IsKnownEventType
// can be used in tests or middleware to enforce the constraint.
//
// Payload accepts any JSON-marshalable value. Production code is
// expected to pass one of the typed structs declared in
// event_payloads.go (e.g. ApplyTaskClaimedPayload) so the producer
// gets compile-time validation of the field names. The legacy
// `map[string]any` shape is still accepted for backward compatibility
// with tests and gradual-migration callers — both produce identical
// JSON bytes downstream.
type Event struct {
	Type      string // see event_types.go for the catalogue
	Timestamp time.Time
	Payload   any // see event_payloads.go for the typed payload structs
	TraceID   string
}

// EventStream is the publish-subscribe abstraction backing the
// `/api/v1/changes/{id}/phases/{phase_id}/events` SSE endpoint.
type EventStream interface {
	// Subscribe returns a channel of Events for phaseID, plus a cancel
	// function the HTTP handler MUST call on disconnect.
	Subscribe(ctx context.Context, phaseID ids.PhaseID) (<-chan Event, func(), error)

	// Publish emits an event to all current subscribers of phaseID.
	// Non-blocking: subscribers with full channels miss events (caller
	// records this in metrics).
	Publish(ctx context.Context, phaseID ids.PhaseID, ev Event) error
}
