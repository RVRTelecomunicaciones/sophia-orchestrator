package pg

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skillusage"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// SkillUsageRepo persists SkillUsage records into the skill_usage table
// (migration 011). Insert is idempotent via ON CONFLICT DO NOTHING on the
// unique constraint (change_id, phase_type, skill_id, skill_version).
type SkillUsageRepo struct {
	pool *pgxpool.Pool
}

// NewSkillUsageRepo constructs a SkillUsageRepo.
func NewSkillUsageRepo(pool *pgxpool.Pool) *SkillUsageRepo {
	if pool == nil {
		panic("pg.SkillUsageRepo: nil pool")
	}
	return &SkillUsageRepo{pool: pool}
}

// Insert writes a new skill_usage row. If the UNIQUE(change_id, phase_type,
// skill_id, skill_version) constraint fires, the conflicting row is left
// unchanged and no error is returned (idempotent re-injection).
func (r *SkillUsageRepo) Insert(ctx context.Context, su *skillusage.SkillUsage) error {
	const q = `
INSERT INTO skill_usage (id, change_id, phase_type, skill_id, skill_version, injected_at, outcome)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (change_id, phase_type, skill_id, skill_version) DO NOTHING`

	_, err := r.pool.Exec(ctx, q,
		su.ID().String(),
		su.ChangeID().String(),
		su.PhaseType(),
		su.SkillID().String(),
		su.SkillVersion(),
		su.InjectedAt(),
		su.Outcome().String(),
	)
	return wrapErr("SkillUsageRepo.Insert", err)
}

// UpdateOutcome sets the outcome column for a specific skill_usage row
// identified by its primary key ID.
func (r *SkillUsageRepo) UpdateOutcome(ctx context.Context, id ids.SkillUsageID, outcome skillusage.Outcome) error {
	const q = `UPDATE skill_usage SET outcome = $2 WHERE id = $1`
	_, err := r.pool.Exec(ctx, q, id.String(), outcome.String())
	return wrapErr("SkillUsageRepo.UpdateOutcome", err)
}

// FindByChange returns all skill_usage rows for the given change_id, ordered
// by injected_at ascending.
func (r *SkillUsageRepo) FindByChange(ctx context.Context, changeID ids.ChangeID) ([]*skillusage.SkillUsage, error) {
	const q = `
SELECT id, change_id, phase_type, skill_id, skill_version, injected_at, outcome
FROM   skill_usage
WHERE  change_id = $1
ORDER  BY injected_at ASC`

	return r.query(ctx, q, changeID.String())
}

// FindBySkill returns all skill_usage rows for the given skill_id, ordered
// by injected_at descending (most-recent first, matching idx_skill_usage_skill_injected).
func (r *SkillUsageRepo) FindBySkill(ctx context.Context, skillID ids.SkillID) ([]*skillusage.SkillUsage, error) {
	const q = `
SELECT id, change_id, phase_type, skill_id, skill_version, injected_at, outcome
FROM   skill_usage
WHERE  skill_id = $1
ORDER  BY injected_at DESC`

	return r.query(ctx, q, skillID.String())
}

// SumApplyAttemptsByChange returns the total tasks.attempts recorded for the
// change's apply tasks, joined tasks→groups→apply_boards→phases filtered by
// change_id (D-LH-2). Per-change granularity: tasks carry no skill_id, so this
// is the finest honest aggregation without a schema change. Returns 0 when the
// change has no apply tasks (COALESCE).
func (r *SkillUsageRepo) SumApplyAttemptsByChange(ctx context.Context, changeID ids.ChangeID) (int, error) {
	const q = `
SELECT COALESCE(SUM(t.attempts), 0)
FROM   tasks t
JOIN   groups g       ON g.id = t.group_id
JOIN   apply_boards b ON b.id = g.board_id
JOIN   phases p       ON p.id = b.phase_id
WHERE  p.change_id = $1`

	var sum int
	if err := r.pool.QueryRow(ctx, q, changeID.String()).Scan(&sum); err != nil {
		return 0, wrapErr("SkillUsageRepo.SumApplyAttemptsByChange", err)
	}
	return sum, nil
}

// query is the shared scan helper for FindByChange and FindBySkill.
func (r *SkillUsageRepo) query(ctx context.Context, q string, arg string) ([]*skillusage.SkillUsage, error) {
	rows, err := r.pool.Query(ctx, q, arg)
	if err != nil {
		return nil, wrapErr("SkillUsageRepo.query", err)
	}
	defer rows.Close()

	out := make([]*skillusage.SkillUsage, 0)
	for rows.Next() {
		su, err := scanSkillUsage(rows)
		if err != nil {
			return nil, wrapErr("SkillUsageRepo.query.scan", err)
		}
		out = append(out, su)
	}
	return out, rows.Err()
}

// Verify SkillUsageRepo satisfies the port at compile time.
var _ outbound.SkillUsageRepository = (*SkillUsageRepo)(nil)

// scanSkillUsage reads one row and hydrates a SkillUsage.
func scanSkillUsage(rows interface {
	Scan(dest ...any) error
}) (*skillusage.SkillUsage, error) {
	var (
		rawID, rawChangeID, phaseType, rawSkillID, skillVersion string
		injectedAt                                               = mustTime()
		outcomeStr                                               string
	)
	if err := rows.Scan(
		&rawID, &rawChangeID, &phaseType, &rawSkillID, &skillVersion,
		&injectedAt, &outcomeStr,
	); err != nil {
		return nil, err
	}

	id, err := ids.ParseSkillUsageID(rawID)
	if err != nil {
		return nil, err
	}
	changeID, err := ids.ParseChangeID(rawChangeID)
	if err != nil {
		return nil, err
	}
	skillID, err := ids.ParseSkillID(rawSkillID)
	if err != nil {
		return nil, err
	}

	return skillusage.Hydrate(id, changeID, phaseType, skillID, skillVersion,
		injectedAt, skillusage.Outcome(outcomeStr)), nil
}
