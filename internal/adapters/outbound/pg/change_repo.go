package pg

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// ChangeRepo persists Change aggregates in Postgres.
type ChangeRepo struct {
	pool *pgxpool.Pool
}

// NewChangeRepo constructs a ChangeRepo. Pool must be non-nil.
func NewChangeRepo(pool *pgxpool.Pool) *ChangeRepo {
	if pool == nil {
		panic("pg.ChangeRepo: nil pool")
	}
	return &ChangeRepo{pool: pool}
}

// Save upserts a Change row. The (project, name) UNIQUE constraint ensures
// a single Change per slot.
func (r *ChangeRepo) Save(ctx context.Context, c *change.Change) error {
	const q = `
INSERT INTO changes (id, name, project, status, current_phase, artifact_store, base_ref, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (id) DO UPDATE SET
  status         = EXCLUDED.status,
  current_phase  = EXCLUDED.current_phase,
  updated_at     = EXCLUDED.updated_at
`
	_, err := r.pool.Exec(ctx, q,
		c.ID().String(), c.Name(), c.Project(),
		string(c.Status()), string(c.CurrentPhase()),
		string(c.ArtifactStore()), c.BaseRef(),
		c.CreatedAt(), c.UpdatedAt(),
	)
	return wrapErr("ChangeRepo.Save", err)
}

// FindByID returns the Change identified by id or outbound.ErrNotFound.
func (r *ChangeRepo) FindByID(ctx context.Context, id ids.ChangeID) (*change.Change, error) {
	const q = `
SELECT id, name, project, status, current_phase, artifact_store, base_ref, created_at, updated_at
FROM changes WHERE id = $1`
	row := r.pool.QueryRow(ctx, q, id.String())
	return scanChange(row)
}

// FindByProjectName returns the Change with the given (project, name) pair.
func (r *ChangeRepo) FindByProjectName(ctx context.Context, project, name string) (*change.Change, error) {
	const q = `
SELECT id, name, project, status, current_phase, artifact_store, base_ref, created_at, updated_at
FROM changes WHERE project = $1 AND name = $2`
	row := r.pool.QueryRow(ctx, q, project, name)
	return scanChange(row)
}

// List returns Changes for a project optionally filtered by status, paginated.
func (r *ChangeRepo) List(ctx context.Context, project, status string, limit, offset int) ([]*change.Change, error) {
	const q = `
SELECT id, name, project, status, current_phase, artifact_store, base_ref, created_at, updated_at
FROM changes
WHERE ($1 = '' OR project = $1) AND ($2 = '' OR status = $2)
ORDER BY created_at DESC
LIMIT $3 OFFSET $4`
	rows, err := r.pool.Query(ctx, q, project, status, limit, offset)
	if err != nil {
		return nil, wrapErr("ChangeRepo.List", err)
	}
	defer rows.Close()
	out := make([]*change.Change, 0)
	for rows.Next() {
		c, err := scanChange(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// scanChange supports both pgx.Rows and pgx.Row via the Scan method.
type scannable interface {
	Scan(dest ...any) error
}

func scanChange(s scannable) (*change.Change, error) {
	var (
		idStr, name, project, status string
		currentPhase, artifactStore  string
		baseRef                      string
		createdAt, updatedAt         = mustTime(), mustTime()
	)
	err := s.Scan(&idStr, &name, &project, &status, &currentPhase, &artifactStore, &baseRef, &createdAt, &updatedAt)
	if err != nil {
		return nil, wrapErr("scanChange", err)
	}
	id, err := ids.ParseChangeID(idStr)
	if err != nil {
		return nil, wrapErr("scanChange.parse", err)
	}
	return change.Hydrate(
		id, name, project,
		change.Status(status),
		phase.PhaseType(currentPhase),
		change.ArtifactStoreMode(artifactStore),
		baseRef, createdAt, updatedAt,
	), nil
}

// Compile-time interface check.
var _ outbound.ChangeRepository = (*ChangeRepo)(nil)
