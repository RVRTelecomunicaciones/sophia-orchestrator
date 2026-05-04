package pg

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// AuditLog appends events to the audit_log table. R11: append-only — the
// table grants no UPDATE or DELETE to the application user.
type AuditLog struct {
	pool *pgxpool.Pool
}

// NewAuditLog constructs an AuditLog.
func NewAuditLog(pool *pgxpool.Pool) *AuditLog {
	if pool == nil {
		panic("pg.AuditLog: nil pool")
	}
	return &AuditLog{pool: pool}
}

// Append inserts one audit event.
func (a *AuditLog) Append(ctx context.Context, e outbound.AuditEvent) error {
	const q = `
INSERT INTO audit_log (change_id, phase_id, session_id, event_type, payload, created_at)
VALUES ($1, $2, $3, $4, $5, $6)`
	var changeID, phaseID, sessionID *string
	if e.ChangeID != nil {
		s := e.ChangeID.String()
		changeID = &s
	}
	if e.PhaseID != nil {
		s := e.PhaseID.String()
		phaseID = &s
	}
	if e.SessionID != nil {
		s := e.SessionID.String()
		sessionID = &s
	}
	_, err := a.pool.Exec(ctx, q, changeID, phaseID, sessionID, e.EventType, e.Payload, e.OccurredAt)
	return wrapErr("AuditLog.Append", err)
}

// Compile-time interface check.
var _ outbound.AuditLog = (*AuditLog)(nil)
