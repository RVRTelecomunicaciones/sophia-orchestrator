//go:build integration

package pg_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/pg"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skillusage"
)

// B.2 RED — SkillUsageRepo integration tests.

// usageIDs — stable ULID-format IDs for skill_usage integration tests.
var (
	usageID1 = "01ARZ3NDEKTSV4RRFFQ69G5SU1"
	usageID2 = "01ARZ3NDEKTSV4RRFFQ69G5SU2"
	usageID3 = "01ARZ3NDEKTSV4RRFFQ69G5SU3"

	usageChangeID1 = "01ARZ3NDEKTSV4RRFFQ69G5UC1"
	usageChangeID2 = "01ARZ3NDEKTSV4RRFFQ69G5UC2"

	usageSkillID1 = "01ARZ3NDEKTSV4RRFFQ69G5US1"
	usageSkillID2 = "01ARZ3NDEKTSV4RRFFQ69G5US2"

	usageNow = time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
)

func mustUsageID(t *testing.T, raw string) ids.SkillUsageID {
	t.Helper()
	id, err := ids.ParseSkillUsageID(raw)
	require.NoError(t, err)
	return id
}

func mustUsageChangeID(t *testing.T, raw string) ids.ChangeID {
	t.Helper()
	id, err := ids.ParseChangeID(raw)
	require.NoError(t, err)
	return id
}

func mustUsageSkillID(t *testing.T, raw string) ids.SkillID {
	t.Helper()
	id, err := ids.ParseSkillID(raw)
	require.NoError(t, err)
	return id
}

func newTestUsage(t *testing.T, rawID, rawChangeID, phaseType, rawSkillID, version string) *skillusage.SkillUsage {
	t.Helper()
	return skillusage.New(
		mustUsageID(t, rawID),
		mustUsageChangeID(t, rawChangeID),
		phaseType,
		mustUsageSkillID(t, rawSkillID),
		version,
		usageNow,
	)
}

// TestSkillUsageRepo_Insert verifies that Insert writes a row with outcome=pending
// and that the row can be read back via FindByChange.
func TestSkillUsageRepo_Insert(t *testing.T) {
	pool, _ := setupMigration009OnlyPG(t) // applies all migrations including 011
	ctx := context.Background()
	repo := pg.NewSkillUsageRepo(pool)

	su := newTestUsage(t, usageID1, usageChangeID1, "apply", usageSkillID1, "v1")
	require.NoError(t, repo.Insert(ctx, su))

	rows, err := repo.FindByChange(ctx, mustUsageChangeID(t, usageChangeID1))
	require.NoError(t, err)
	require.Len(t, rows, 1)

	assert.Equal(t, usageID1, rows[0].ID().String())
	assert.Equal(t, usageChangeID1, rows[0].ChangeID().String())
	assert.Equal(t, "apply", rows[0].PhaseType())
	assert.Equal(t, usageSkillID1, rows[0].SkillID().String())
	assert.Equal(t, "v1", rows[0].SkillVersion())
	assert.Equal(t, skillusage.OutcomePending, rows[0].Outcome())
}

// TestSkillUsageRepo_Insert_Idempotent verifies that inserting the same
// (change_id, phase_type, skill_id, skill_version) tuple twice is a no-op.
func TestSkillUsageRepo_Insert_Idempotent(t *testing.T) {
	pool, _ := setupMigration009OnlyPG(t)
	ctx := context.Background()
	repo := pg.NewSkillUsageRepo(pool)

	su := newTestUsage(t, usageID1, usageChangeID1, "apply", usageSkillID1, "v1")
	require.NoError(t, repo.Insert(ctx, su), "first insert must succeed")

	// Second insert with different id but same unique tuple must be a no-op.
	su2 := newTestUsage(t, usageID2, usageChangeID1, "apply", usageSkillID1, "v1")
	require.NoError(t, repo.Insert(ctx, su2), "second insert (conflict) must not return error")

	// Only one row must exist.
	rows, err := repo.FindByChange(ctx, mustUsageChangeID(t, usageChangeID1))
	require.NoError(t, err)
	assert.Len(t, rows, 1, "idempotent insert must not create duplicate rows")
}

// TestSkillUsageRepo_UpdateOutcome verifies that UpdateOutcome changes the
// outcome of an existing row.
func TestSkillUsageRepo_UpdateOutcome(t *testing.T) {
	pool, _ := setupMigration009OnlyPG(t)
	ctx := context.Background()
	repo := pg.NewSkillUsageRepo(pool)

	su := newTestUsage(t, usageID1, usageChangeID1, "apply", usageSkillID1, "v1")
	require.NoError(t, repo.Insert(ctx, su))

	require.NoError(t, repo.UpdateOutcome(ctx, mustUsageID(t, usageID1), skillusage.OutcomeSuccess))

	rows, err := repo.FindByChange(ctx, mustUsageChangeID(t, usageChangeID1))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, skillusage.OutcomeSuccess, rows[0].Outcome())
}

// TestSkillUsageRepo_FindByChange_MultiRow verifies filter by change_id.
func TestSkillUsageRepo_FindByChange_MultiRow(t *testing.T) {
	pool, _ := setupMigration009OnlyPG(t)
	ctx := context.Background()
	repo := pg.NewSkillUsageRepo(pool)

	// Two rows for change1, one for change2.
	su1 := newTestUsage(t, usageID1, usageChangeID1, "apply", usageSkillID1, "v1")
	su2 := newTestUsage(t, usageID2, usageChangeID1, "verify", usageSkillID2, "v1")
	su3 := newTestUsage(t, usageID3, usageChangeID2, "apply", usageSkillID1, "v1")

	require.NoError(t, repo.Insert(ctx, su1))
	require.NoError(t, repo.Insert(ctx, su2))
	require.NoError(t, repo.Insert(ctx, su3))

	rows, err := repo.FindByChange(ctx, mustUsageChangeID(t, usageChangeID1))
	require.NoError(t, err)
	assert.Len(t, rows, 2, "only rows for change1 must be returned")

	rows2, err := repo.FindByChange(ctx, mustUsageChangeID(t, usageChangeID2))
	require.NoError(t, err)
	assert.Len(t, rows2, 1, "only row for change2 must be returned")
}

// TestSkillUsageRepo_FindBySkill verifies filter by skill_id.
func TestSkillUsageRepo_FindBySkill(t *testing.T) {
	pool, _ := setupMigration009OnlyPG(t)
	ctx := context.Background()
	repo := pg.NewSkillUsageRepo(pool)

	su1 := newTestUsage(t, usageID1, usageChangeID1, "apply", usageSkillID1, "v1")
	su2 := newTestUsage(t, usageID2, usageChangeID1, "verify", usageSkillID2, "v1")
	su3 := newTestUsage(t, usageID3, usageChangeID2, "apply", usageSkillID1, "v1")

	require.NoError(t, repo.Insert(ctx, su1))
	require.NoError(t, repo.Insert(ctx, su2))
	require.NoError(t, repo.Insert(ctx, su3))

	rows, err := repo.FindBySkill(ctx, mustUsageSkillID(t, usageSkillID1))
	require.NoError(t, err)
	assert.Len(t, rows, 2, "two rows for skill1 (across two changes)")

	rows2, err := repo.FindBySkill(ctx, mustUsageSkillID(t, usageSkillID2))
	require.NoError(t, err)
	assert.Len(t, rows2, 1, "one row for skill2")
}
