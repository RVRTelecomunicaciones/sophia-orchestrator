package pg

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// EventStore persists SSE events to phase_events. Closes audit rojo #3
// (in-memory hub → events lost on restart, no replay for late
// subscribers).
//
// Sequence is the BIGSERIAL `id` column. Append returns it via
// RETURNING. Replay queries `WHERE phase_id=$1 AND id>$2 ORDER BY id`
// — the composite index phase_events_phase_idx serves both predicates
// without a separate sort step.
type EventStore struct {
	pool *pgxpool.Pool
}

// NewEventStore constructs an EventStore. Panics on nil pool to mirror
// the existing pg-adapter pattern.
func NewEventStore(pool *pgxpool.Pool) *EventStore {
	if pool == nil {
		panic("pg.EventStore: nil pool")
	}
	return &EventStore{pool: pool}
}

// Append inserts one event for phaseID and returns the assigned
// monotonic Sequence. The payload is JSON-encoded inline; any type that
// json.Marshal supports works (typed structs from
// internal/ports/inbound/event_payloads.go, plain map[string]any, etc.).
func (s *EventStore) Append(ctx context.Context, phaseID ids.PhaseID, ev inbound.Event) (int64, error) {
	payload, err := json.Marshal(ev.Payload)
	if err != nil {
		return 0, fmt.Errorf("EventStore.Append: marshal payload: %w", err)
	}
	const q = `
INSERT INTO phase_events (phase_id, event_type, payload, trace_id, created_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING id`
	var seq int64
	row := s.pool.QueryRow(ctx, q,
		phaseID.String(), ev.Type, payload, ev.TraceID, ev.Timestamp,
	)
	if err := row.Scan(&seq); err != nil {
		return 0, wrapErr("EventStore.Append", err)
	}
	return seq, nil
}

// Replay returns every persisted event for phaseID with Sequence > sinceSeq,
// ordered by Sequence ascending. Empty slice when nothing matches. The
// returned events have Sequence populated; Payload is returned as
// json.RawMessage wrapped in `any` so the SSE handler can pass it
// straight through json.Marshal without a redundant decode/encode cycle.
func (s *EventStore) Replay(ctx context.Context, phaseID ids.PhaseID, sinceSeq int64) ([]inbound.Event, error) {
	const q = `
SELECT id, event_type, payload, trace_id, created_at
FROM phase_events
WHERE phase_id = $1 AND id > $2
ORDER BY id ASC`
	rows, err := s.pool.Query(ctx, q, phaseID.String(), sinceSeq)
	if err != nil {
		return nil, wrapErr("EventStore.Replay query", err)
	}
	defer rows.Close()

	var out []inbound.Event
	for rows.Next() {
		var (
			ev      inbound.Event
			payload []byte
		)
		if err := rows.Scan(&ev.Sequence, &ev.Type, &payload, &ev.TraceID, &ev.Timestamp); err != nil {
			return nil, wrapErr("EventStore.Replay scan", err)
		}
		// json.RawMessage avoids re-marshaling when the SSE handler
		// serializes the event downstream — the bytes pass through
		// the second json.Marshal call as a verbatim JSON value.
		ev.Payload = json.RawMessage(payload)
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapErr("EventStore.Replay rows", err)
	}
	return out, nil
}

// Compile-time interface check.
var _ outbound.EventStore = (*EventStore)(nil)
