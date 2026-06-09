package pg

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// SkillRepo persists Skill aggregates against the skills table (migration 009).
// The GIN index on phases enables efficient ANY(phases) look-ups for FindByPhase.
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

// FindByPhase returns every Skill whose phases array contains pt.
// Returns an empty (non-nil) slice when no rows match; never returns ErrNotFound.
func (r *SkillRepo) FindByPhase(ctx context.Context, pt phase.PhaseType) ([]*skill.Skill, error) {
	const q = `
SELECT id, name, phases, content, techniques, created_at, updated_at
FROM   skills
WHERE  $1 = ANY(phases)`

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

// Upsert inserts or fully replaces the Skill row identified by id.
func (r *SkillRepo) Upsert(ctx context.Context, s *skill.Skill) error {
	const q = `
INSERT INTO skills (id, name, phases, content, techniques, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (id) DO UPDATE SET
    name       = EXCLUDED.name,
    phases     = EXCLUDED.phases,
    content    = EXCLUDED.content,
    techniques = EXCLUDED.techniques,
    updated_at = EXCLUDED.updated_at`

	_, err := r.pool.Exec(ctx, q,
		s.ID().String(),
		s.Name(),
		s.PhaseStrings(),
		s.Content(),
		s.TechniqueStrings(),
		s.CreatedAt(),
		s.UpdatedAt(),
	)
	return wrapErr("SkillRepo.Upsert", err)
}

// InsertIfAbsent inserts the Skill only when no row with the same name already
// exists. It is a deliberate no-op when a name collision is detected — this is
// the safe boot-time seeder operation. Returns nil in both the insert and
// no-op cases; returns a non-nil error only on infrastructure failures.
func (r *SkillRepo) InsertIfAbsent(ctx context.Context, s *skill.Skill) error {
	const q = `
INSERT INTO skills (id, name, phases, content, techniques, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (name) DO NOTHING`

	_, err := r.pool.Exec(ctx, q,
		s.ID().String(),
		s.Name(),
		s.PhaseStrings(),
		s.Content(),
		s.TechniqueStrings(),
		s.CreatedAt(),
		s.UpdatedAt(),
	)
	return wrapErr("SkillRepo.InsertIfAbsent", err)
}

// List returns all persisted Skills. Returns an empty (non-nil) slice when the
// table is empty.
func (r *SkillRepo) List(ctx context.Context) ([]*skill.Skill, error) {
	const q = `
SELECT id, name, phases, content, techniques, created_at, updated_at
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
// M1 NOTE: This function is extended in Group E to read the 9 lifecycle
// columns added by migration 010. The temporary implementation here reads
// the 7 pre-M1 columns and passes zero-value lifecycle fields to Hydrate.
// selectColumns constant is updated in Group E to list all 16 columns.
func scanSkill(rows pgx.Rows) (*skill.Skill, error) {
	var (
		rawID      string
		name       string
		phases     []string
		content    string
		techniques []string
		createdAt  time.Time
		updatedAt  time.Time
	)
	if err := rows.Scan(&rawID, &name, &phases, &content, &techniques, &createdAt, &updatedAt); err != nil {
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

	return skill.Hydrate(skillID, name, phaseTypes, content, techTags,
		skill.StatusCandidate, "v1",
		skill.Scope{}, skill.AppliesWhen{},
		skill.RiskMedium, skill.SourceManual,
		skill.Metrics{},
		nil, nil, // lastUsedAt, lastValidatedAt
		createdAt, updatedAt,
	), nil
}

// Verify SkillRepo satisfies the SkillRepository port at compile time.
var _ outbound.SkillRepository = (*SkillRepo)(nil)
