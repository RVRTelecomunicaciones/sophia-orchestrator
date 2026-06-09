package pg

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// selectColumns lists all 16 columns for SELECT queries after migration 010.
// Order MUST match the scan positions in scanSkill.
const selectColumns = `id, name, phases, content, techniques,
    status, version, scope, applies_when, risk_level,
    activation_source, metrics, last_used_at, last_validated_at,
    created_at, updated_at`

// SkillRepo persists Skill aggregates against the skills table (migration 010).
// The GIN index on phases enables efficient ANY(phases) look-ups for FindByPhase.
// The status='active' filter on FindByPhase is a hard-coded invariant per D-M1-6.
type SkillRepo struct {
	pool *pgxpool.Pool
}

// NewSkillRepo constructs a SkillRepo.
func NewSkillRepo(pool *pgxpool.Pool) *SkillRepo {
	if pool == nil {
		panic("pg.SkillRepo: nil pool")
	}
	return &SkillRepo{pool: pool}
}

// FindByPhase returns every Skill whose phases array contains pt AND status='active'.
// Returns an empty (non-nil) slice when no rows match; never returns ErrNotFound.
// The status='active' filter is a hard-coded invariant (D-M1-6): FindByPhase
// callers MUST NEVER receive non-active skills. New code uses SkillMatcher.SkillsForContext.
func (r *SkillRepo) FindByPhase(ctx context.Context, pt phase.PhaseType) ([]*skill.Skill, error) {
	q := `
SELECT ` + selectColumns + `
FROM   skills
WHERE  $1 = ANY(phases)
  AND  status = 'active'`

	rows, err := r.pool.Query(ctx, q, string(pt))
	if err != nil {
		return nil, wrapErr("SkillRepo.FindByPhase", err)
	}
	defer rows.Close()

	out := make([]*skill.Skill, 0)
	for rows.Next() {
		s, err := scanSkill(rows)
		if err != nil {
			return nil, wrapErr("SkillRepo.FindByPhase.scan", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Upsert inserts or fully replaces the Skill row identified by (name, version).
// Conflicts on (name, version) per migration 010 D-M1-1. Idempotent.
func (r *SkillRepo) Upsert(ctx context.Context, s *skill.Skill) error {
	scopeJSON, err := json.Marshal(s.Scope())
	if err != nil {
		return wrapErr("SkillRepo.Upsert.marshalScope", err)
	}
	appliesJSON, err := json.Marshal(s.AppliesWhen())
	if err != nil {
		return wrapErr("SkillRepo.Upsert.marshalAppliesWhen", err)
	}
	metricsJSON, err := json.Marshal(s.Metrics())
	if err != nil {
		return wrapErr("SkillRepo.Upsert.marshalMetrics", err)
	}

	const q = `
INSERT INTO skills (
    id, name, phases, content, techniques,
    status, version, scope, applies_when, risk_level,
    activation_source, metrics, last_used_at, last_validated_at,
    created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5,
        $6, $7, $8, $9, $10,
        $11, $12, $13, $14,
        $15, $16)
ON CONFLICT (name, version) DO UPDATE SET
    id                = EXCLUDED.id,
    phases            = EXCLUDED.phases,
    content           = EXCLUDED.content,
    techniques        = EXCLUDED.techniques,
    status            = EXCLUDED.status,
    scope             = EXCLUDED.scope,
    applies_when      = EXCLUDED.applies_when,
    risk_level        = EXCLUDED.risk_level,
    activation_source = EXCLUDED.activation_source,
    metrics           = EXCLUDED.metrics,
    last_used_at      = EXCLUDED.last_used_at,
    last_validated_at = EXCLUDED.last_validated_at,
    updated_at        = EXCLUDED.updated_at`

	_, err = r.pool.Exec(ctx, q,
		s.ID().String(),
		s.Name(),
		s.PhaseStrings(),
		s.Content(),
		s.TechniqueStrings(),
		s.Status().String(),
		s.Version(),
		scopeJSON,
		appliesJSON,
		s.RiskLevel().String(),
		s.ActivationSource().String(),
		metricsJSON,
		s.LastUsedAt(),
		s.LastValidatedAt(),
		s.CreatedAt(),
		s.UpdatedAt(),
	)
	return wrapErr("SkillRepo.Upsert", err)
}

// InsertIfAbsent inserts the Skill only when no row with the same (name, version)
// already exists per migration 010 constraint swap. Returns nil in both the
// insert and no-op cases; returns a non-nil error only on infrastructure failures.
func (r *SkillRepo) InsertIfAbsent(ctx context.Context, s *skill.Skill) error {
	scopeJSON, err := json.Marshal(s.Scope())
	if err != nil {
		return wrapErr("SkillRepo.InsertIfAbsent.marshalScope", err)
	}
	appliesJSON, err := json.Marshal(s.AppliesWhen())
	if err != nil {
		return wrapErr("SkillRepo.InsertIfAbsent.marshalAppliesWhen", err)
	}
	metricsJSON, err := json.Marshal(s.Metrics())
	if err != nil {
		return wrapErr("SkillRepo.InsertIfAbsent.marshalMetrics", err)
	}

	const q = `
INSERT INTO skills (
    id, name, phases, content, techniques,
    status, version, scope, applies_when, risk_level,
    activation_source, metrics, last_used_at, last_validated_at,
    created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5,
        $6, $7, $8, $9, $10,
        $11, $12, $13, $14,
        $15, $16)
ON CONFLICT (name, version) DO NOTHING`

	_, err = r.pool.Exec(ctx, q,
		s.ID().String(),
		s.Name(),
		s.PhaseStrings(),
		s.Content(),
		s.TechniqueStrings(),
		s.Status().String(),
		s.Version(),
		scopeJSON,
		appliesJSON,
		s.RiskLevel().String(),
		s.ActivationSource().String(),
		metricsJSON,
		s.LastUsedAt(),
		s.LastValidatedAt(),
		s.CreatedAt(),
		s.UpdatedAt(),
	)
	return wrapErr("SkillRepo.InsertIfAbsent", err)
}

// List returns all persisted Skills. Returns an empty (non-nil) slice when the
// table is empty.
func (r *SkillRepo) List(ctx context.Context) ([]*skill.Skill, error) {
	q := `SELECT ` + selectColumns + `
FROM   skills
ORDER  BY name`

	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, wrapErr("SkillRepo.List", err)
	}
	defer rows.Close()

	out := make([]*skill.Skill, 0)
	for rows.Next() {
		s, err := scanSkill(rows)
		if err != nil {
			return nil, wrapErr("SkillRepo.List.scan", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// scanSkill reads one row from a pgx.Rows cursor and hydrates a Skill.
// Expects 16 columns in the order defined by selectColumns.
// JSONB columns (scope, applies_when, metrics) are scanned as []byte and
// decoded via json.Unmarshal into their respective value types.
func scanSkill(rows pgx.Rows) (*skill.Skill, error) {
	var (
		rawID                                  string
		name, content                          string
		phases, techniques                     []string
		statusStr, versionStr, riskStr, srcStr string
		scopeBytes, appliesBytes, metricsBytes []byte
		lastUsedAt, lastValidatedAt            *time.Time
		createdAt, updatedAt                   time.Time
	)
	if err := rows.Scan(
		&rawID, &name, &phases, &content, &techniques,
		&statusStr, &versionStr, &scopeBytes, &appliesBytes, &riskStr,
		&srcStr, &metricsBytes, &lastUsedAt, &lastValidatedAt,
		&createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}

	skillID, err := ids.ParseSkillID(rawID)
	if err != nil {
		return nil, err
	}

	phaseTypes := make([]phase.PhaseType, len(phases))
	for i, p := range phases {
		phaseTypes[i] = phase.PhaseType(p)
	}

	techTags := make([]skill.Technique, len(techniques))
	for i, t := range techniques {
		techTags[i] = skill.Technique(t)
	}

	var sc skill.Scope
	if err := json.Unmarshal(scopeBytes, &sc); err != nil {
		return nil, fmt.Errorf("scanSkill: scope: %w", err)
	}
	var aw skill.AppliesWhen
	if err := json.Unmarshal(appliesBytes, &aw); err != nil {
		return nil, fmt.Errorf("scanSkill: applies_when: %w", err)
	}
	var met skill.Metrics
	if err := json.Unmarshal(metricsBytes, &met); err != nil {
		return nil, fmt.Errorf("scanSkill: metrics: %w", err)
	}

	return skill.Hydrate(
		skillID, name, phaseTypes, content, techTags,
		skill.Status(statusStr), versionStr,
		sc, aw,
		skill.RiskLevel(riskStr), skill.ActivationSource(srcStr), met,
		lastUsedAt, lastValidatedAt,
		createdAt, updatedAt,
	), nil
}

// Verify SkillRepo satisfies the SkillRepository port at compile time.
var _ outbound.SkillRepository = (*SkillRepo)(nil)
