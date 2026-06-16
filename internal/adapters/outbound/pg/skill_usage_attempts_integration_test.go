//go:build integration

package pg_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/pg"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
)

// F.1 RED — SumApplyAttemptsByChange aggregates tasks.attempts joined to a change
// via tasks→groups→apply_boards→phases (migration 002/003). Per-change granularity
// (tasks has no skill_id), per design D-LH-2.

// seedApplyAttempts inserts a change → phase → apply_board → group → tasks chain
// with the provided per-task attempts values. Returns the change_id used.
func seedApplyAttempts(t *testing.T, pool *pgxpool.Pool, changeID, phaseID, boardID, groupID string, attempts []int) {
	t.Helper()
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
INSERT INTO changes (id, name, project, status, artifact_store, created_at, updated_at)
VALUES ($1, 'reeval-test', 'test-proj', 'active', 'engram', NOW(), NOW())`, changeID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
INSERT INTO phases (id, change_id, phase_type, status, attempts)
VALUES ($1, $2, 'apply', 'completed', 0)`, phaseID, changeID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
INSERT INTO apply_boards (id, phase_id, status)
VALUES ($1, $2, 'completed')`, boardID, phaseID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
INSERT INTO groups (id, board_id, name, status)
VALUES ($1, $2, 'g1', 'completed')`, groupID, boardID)
	require.NoError(t, err)

	for i, a := range attempts {
		taskID := newSeedID(t, "TASK", i)
		_, err = pool.Exec(ctx, `
INSERT INTO tasks (id, group_id, description, files_pattern, status, attempts)
VALUES ($1, $2, 'task', '{}', 'completed', $3)`, taskID, groupID, a)
		require.NoError(t, err)
	}
}

// newSeedID derives a deterministic 26-char ULID-format id for seeding.
func newSeedID(t *testing.T, prefix string, n int) string {
	t.Helper()
	base := "01ARZ3NDEKTSV4RRFFQ69G50"
	suffix := string(rune('A'+(n%26))) + string(rune('A'+(n/26)%26))
	id := base + suffix
	require.Len(t, id, 26)
	return id
}

func mustChangeID(t *testing.T, raw string) ids.ChangeID {
	t.Helper()
	id, err := ids.ParseChangeID(raw)
	require.NoError(t, err)
	return id
}

// TestSkillUsageRepo_SumApplyAttemptsByChange verifies SUM(tasks.attempts) for a change.
func TestSkillUsageRepo_SumApplyAttemptsByChange(t *testing.T) {
	pool := setupSkillPG(t)
	ctx := context.Background()
	repo := pg.NewSkillUsageRepo(pool)

	changeID := "01ARZ3NDEKTSV4RRFFQ69G5AC1"
	seedApplyAttempts(t, pool,
		changeID,
		"01ARZ3NDEKTSV4RRFFQ69G5AP1",
		"01ARZ3NDEKTSV4RRFFQ69G5AB1",
		"01ARZ3NDEKTSV4RRFFQ69G5AG1",
		[]int{2, 3, 0}, // SUM = 5
	)

	sum, err := repo.SumApplyAttemptsByChange(ctx, mustChangeID(t, changeID))
	require.NoError(t, err)
	assert.Equal(t, 5, sum, "SUM(tasks.attempts) for the change")
}

// TestSkillUsageRepo_SumApplyAttemptsByChange_NoRows returns 0 (COALESCE) when no
// apply tasks exist for the change.
func TestSkillUsageRepo_SumApplyAttemptsByChange_NoRows(t *testing.T) {
	pool := setupSkillPG(t)
	ctx := context.Background()
	repo := pg.NewSkillUsageRepo(pool)

	emptyChange := "01ARZ3NDEKTSV4RRFFQ69G5EC1"
	_, err := pool.Exec(ctx, `
INSERT INTO changes (id, name, project, status, artifact_store, created_at, updated_at)
VALUES ($1, 'empty', 'test-proj', 'active', 'engram', NOW(), NOW())`, emptyChange)
	require.NoError(t, err)

	sum, err := repo.SumApplyAttemptsByChange(ctx, mustChangeID(t, emptyChange))
	require.NoError(t, err)
	assert.Equal(t, 0, sum, "no apply tasks → COALESCE(SUM, 0) = 0")
}
