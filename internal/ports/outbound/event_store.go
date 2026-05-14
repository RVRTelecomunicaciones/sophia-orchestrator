package outbound

import (
	"context"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
)

// EventStore persists SSE events durably so the stream survives orch
// restarts and late subscribers can resume via Last-Event-ID. Audit
// rojo #3 fix.
//
// Concurrency: implementations MUST tolerate concurrent Append calls
// from many goroutines and Replay calls from many subscribers. Append
// MUST return a monotonically-increasing Sequence per Append (the
// Postgres BIGSERIAL satisfies this naturally).
//
// Failure semantics:
//   - Append returning an error means the event is NOT durable. The
//     eventstream.Stream policy is to log the error and still
//     broadcast to in-memory subscribers (degraded mode) so the
//     current-stream experience is not regressed; callers that need
//     strict durability should layer their own guards.
//   - Replay returning an error means the historical events could
//     not be loaded. The SSE handler treats this as a fatal stream
//     error (returns 500) because resume cannot be honoured safely.
type EventStore interface {
	// Append durably persists ev for phaseID. Returns the assigned
	// monotonic Sequence (> 0 on success). ev's existing Sequence
	// field is ignored — the store is the sole assigner.
	Append(ctx context.Context, phaseID ids.PhaseID, ev inbound.Event) (int64, error)

	// Replay returns every event persisted for phaseID with Sequence
	// strictly greater than sinceSeq, ordered by Sequence ascending.
	// sinceSeq=0 returns the complete history. The returned slice may
	// be empty.
	//
	// The slice is materialized (not streamed) — appropriate for the
	// expected per-phase event volume (~hundreds to low thousands at
	// most). If volumes grow we can switch to a row-stream variant
	// without changing callers' for-range loops.
	Replay(ctx context.Context, phaseID ids.PhaseID, sinceSeq int64) ([]inbound.Event, error)
}
