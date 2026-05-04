package pg

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// SessionRepo persists AgentSession aggregates.
type SessionRepo struct {
	pool *pgxpool.Pool
}

// NewSessionRepo constructs a SessionRepo.
func NewSessionRepo(pool *pgxpool.Pool) *SessionRepo {
	if pool == nil {
		panic("pg.SessionRepo: nil pool")
	}
	return &SessionRepo{pool: pool}
}

// Save upserts a session row.
func (r *SessionRepo) Save(ctx context.Context, s *session.Session) error {
	var envBytes []byte
	if s.Envelope() != nil {
		var err error
		envBytes, err = json.Marshal(s.Envelope())
		if err != nil {
			return wrapErr("SessionRepo.Save.marshal", err)
		}
	}
	var worktreeID *string
	if s.WorktreeID() != nil {
		v := s.WorktreeID().String()
		worktreeID = &v
	}
	const q = `
INSERT INTO agent_sessions (id, change_id, phase_id, agent_role, provider, worktree_id, prompt_sha256, command, status, exit_code, envelope, started_at, ended_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
ON CONFLICT (id) DO UPDATE SET
  status      = EXCLUDED.status,
  exit_code   = EXCLUDED.exit_code,
  envelope    = EXCLUDED.envelope,
  ended_at    = EXCLUDED.ended_at,
  worktree_id = EXCLUDED.worktree_id`
	_, err := r.pool.Exec(ctx, q,
		s.ID().String(), s.ChangeID().String(), s.PhaseID().String(),
		string(s.Role()), string(s.Provider()), worktreeID,
		s.PromptSHA256(), s.Command(), string(s.Status()),
		nullableInt(s.ExitCode()), envBytes,
		s.StartedAt(), s.EndedAt(),
	)
	return wrapErr("SessionRepo.Save", err)
}

// FindByID returns the AgentSession identified by id.
func (r *SessionRepo) FindByID(ctx context.Context, id ids.SessionID) (*session.Session, error) {
	const q = `
SELECT id, change_id, phase_id, agent_role, provider, worktree_id, prompt_sha256, command, status, exit_code, envelope, started_at, ended_at
FROM agent_sessions WHERE id = $1`
	row := r.pool.QueryRow(ctx, q, id.String())
	return scanSession(row)
}

// FindByPhaseID returns all AgentSessions associated with a Phase.
func (r *SessionRepo) FindByPhaseID(ctx context.Context, phaseID ids.PhaseID) ([]*session.Session, error) {
	const q = `
SELECT id, change_id, phase_id, agent_role, provider, worktree_id, prompt_sha256, command, status, exit_code, envelope, started_at, ended_at
FROM agent_sessions WHERE phase_id = $1
ORDER BY started_at ASC`
	rows, err := r.pool.Query(ctx, q, phaseID.String())
	if err != nil {
		return nil, wrapErr("SessionRepo.FindByPhaseID", err)
	}
	defer rows.Close()
	var out []*session.Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func scanSession(s scannable) (*session.Session, error) {
	var (
		idStr, changeIDStr, phaseIDStr, role, provider string
		worktreeIDStr                                  *string
		promptSHA, command, status                     string
		exitCode                                       *int
		envBytes                                       []byte
		startedAt                                      time.Time
		endedAt                                        *time.Time
	)
	err := s.Scan(&idStr, &changeIDStr, &phaseIDStr, &role, &provider, &worktreeIDStr, &promptSHA, &command, &status, &exitCode, &envBytes, &startedAt, &endedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, outbound.ErrNotFound
		}
		return nil, wrapErr("scanSession", err)
	}
	sid, _ := ids.ParseSessionID(idStr)
	cid, _ := ids.ParseChangeID(changeIDStr)
	pid, _ := ids.ParsePhaseID(phaseIDStr)
	var wid *ids.WorktreeID
	if worktreeIDStr != nil {
		v, _ := ids.ParseWorktreeID(*worktreeIDStr)
		wid = &v
	}
	var env *envelope.Envelope
	if len(envBytes) > 0 {
		env = &envelope.Envelope{}
		if err := json.Unmarshal(envBytes, env); err != nil {
			return nil, wrapErr("scanSession.unmarshal-envelope", err)
		}
	}
	return session.Hydrate(
		sid, cid, pid,
		session.AgentRole(role), session.Provider(provider),
		wid, promptSHA, command,
		session.Status(status), exitCode, env,
		startedAt, endedAt,
	), nil
}

// Compile-time interface check.
var _ outbound.SessionRepository = (*SessionRepo)(nil)
