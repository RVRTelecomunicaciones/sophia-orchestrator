package pg

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
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

// HasEventForPhase reports whether at least one event of eventType has
// been recorded for phaseID. Implements the gate-state lookup added to
// outbound.AuditLog for sophia-wire-v1 §9.2 phase_not_gated /
// gate_already_decided.
func (a *AuditLog) HasEventForPhase(ctx context.Context, phaseID ids.PhaseID, eventType string) (bool, error) {
	const q = `SELECT EXISTS(SELECT 1 FROM audit_log WHERE phase_id = $1 AND event_type = $2)`
	var ok bool
	if err := a.pool.QueryRow(ctx, q, phaseID.String(), eventType).Scan(&ok); err != nil {
		return false, wrapErr("AuditLog.HasEventForPhase", err)
	}
	return ok, nil
}

// Compile-time interface check.
var _ outbound.AuditLog = (*AuditLog)(nil)
