package inbound

import (
	"context"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
)

// Event is one SSE event emitted by the orchestrator during a phase run.
type Event struct {
	Type      string // "phase.started" | "agent.spawned" | "phase.completed" | "heartbeat" | ...
	Timestamp time.Time
	Payload   map[string]any
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
