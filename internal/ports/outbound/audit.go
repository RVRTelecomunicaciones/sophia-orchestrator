package outbound

import (
	"context"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
)

// AuditLog is the outbound port for the orchestrator's append-only audit
// trail (R11). Two backends in V1: Postgres (system of record) + memory-
// engine ledger (queryable index). The composite adapter writes both.
type AuditLog interface {
	Append(ctx context.Context, e AuditEvent) error
}

// AuditEvent is one append-only audit record.
type AuditEvent struct {
	ChangeID   *ids.ChangeID
	PhaseID    *ids.PhaseID
	SessionID  *ids.SessionID
	EventType  string // "phase.started" | "phase.completed" | "iron_law.violated" | "apply.task.escalated" | ...
	Payload    []byte // JSON
	OccurredAt time.Time
}
