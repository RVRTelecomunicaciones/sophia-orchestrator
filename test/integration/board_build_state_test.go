//go:build integration

package integration_test

// board_build_state_test.go — Task 3.2: prove persisted build attempts/status
// survive board reload and resume (the Slice 1 hydration fix in migration 008).
//
// These tests exercise the full PG round-trip path via BoardRepo:
//   SaveBoard → SaveGroup (build_status/build_attempts) → FindBoardByPhaseID
//
// NOTE: Requires Docker for testcontainers. Without Docker the tests are skipped
// by the infrastructure (`setupPG` requires a live container). The build tag
// `integration` ensures this file is NOT included in `go test ./...` (no -tags).

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/pg"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func mkBoardID(t *testing.T, raw string) ids.BoardID {
	t.Helper()
	id, err := ids.ParseBoardID(raw)
	require.NoError(t, err)
	return id
}

func mkGroupID(t *testing.T, raw string) ids.GroupID {
	t.Helper()
	id, err := ids.ParseGroupID(raw)
	require.NoError(t, err)
	return id
}

func mkTaskIDInt(t *testing.T, raw string) ids.TaskID {
	t.Helper()
	id, err := ids.ParseTaskID(raw)
	require.NoError(t, err)
	return id
}

// seedChangAndPhase creates minimal change + phase rows needed by FK constraints.
func seedChangeAndPhase(
	t *testing.T,
	ctx context.Context,
	cRepo *pg.ChangeRepo,
	pRepo *pg.PhaseRepo,
	cid ids.ChangeID,
	pid ids.PhaseID,
	changeName string,
) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	c, err := change.New(cid, changeName, "proj-build", change.ArtifactStoreMemoryEngine, "", now)
	require.NoError(t, err)
	require.NoError(t, cRepo.Save(ctx, c))

	p, err := phase.New(pid, cid, phase.PhaseApply, 3)
	require.NoError(t, err)
	require.NoError(t, p.Start(now))
	require.NoError(t, pRepo.Save(ctx, p))
}

// ─── BoardRepo build-state round-trip ────────────────────────────────────────

// TestBoardRepo_BuildState_AllStatusVariants proves that each GroupBuildStatus
// value (Pending, Passed, Failed, Skipped) and its build_attempts count survive
// the SaveGroup → FindBoardByPhaseID round-trip introduced in migration 008.
//
// This is the Slice 1 hydration contract: board reload restores the full build
// gate state so the application layer can resume an interrupted build loop
// without re-running attempts that already happened.
func TestBoardRepo_BuildState_AllStatusVariants(t *testing.T) {
	pool := setupPG(t)
	ctx := context.Background()

	cRepo := pg.NewChangeRepo(pool)
	pRepo := pg.NewPhaseRepo(pool)
	bRepo := pg.NewBoardRepo(pool)

	cid := mkChangeID(t, "01ARZ3NDEKTSV4RRFFQ69G5C20")
	pid := mkPhaseID(t, "01ARZ3NDEKTSV4RRFFQ69G5P20")
	seedChangeAndPhase(t, ctx, cRepo, pRepo, cid, pid, "build-state-variants")

	boardID := mkBoardID(t, "01ARZ3NDEKTSV4RRFFQ69G5BD4")
	board := apply.HydrateBoard(boardID, pid, apply.BoardStatusBuilding, nil)
	require.NoError(t, bRepo.SaveBoard(ctx, board))

	// Four groups — one per BuildStatus variant.
	type groupFixture struct {
		id       ids.GroupID
		name     string
		status   apply.GroupBuildStatus
		attempts int
	}
	fixtures := []groupFixture{
		{mkGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5GA2"), "g-pending", apply.GroupBuildStatusPending, 1},
		{mkGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5GB2"), "g-passed", apply.GroupBuildStatusPassed, 2},
		{mkGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5GC2"), "g-failed", apply.GroupBuildStatusFailed, 3},
		{mkGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5GD2"), "g-skipped", apply.GroupBuildStatusSkipped, 0},
	}

	for _, f := range fixtures {
		g := apply.HydrateGroup(
			f.id, boardID, f.name, nil,
			apply.GroupStatusRunning,
			"/wt/"+f.name, "sophia/build-state-variants/"+f.name,
			f.status, f.attempts,
		)
		require.NoError(t, bRepo.SaveGroup(ctx, g))
	}

	// Reload via the hydration path.
	reloaded, err := bRepo.FindBoardByPhaseID(ctx, pid)
	require.NoError(t, err)
	require.NotNil(t, reloaded)
	require.Equal(t, boardID, reloaded.ID())

	// Index reloaded groups by name for assertions.
	byName := map[string]*apply.Group{}
	for _, g := range reloaded.Groups() {
		byName[g.Name()] = g
	}
	require.Len(t, byName, 4, "all four groups must be hydrated on reload")

	// Pending: 1 attempt, still retryable.
	gPending := byName["g-pending"]
	require.NotNil(t, gPending)
	require.Equal(t, apply.GroupBuildStatusPending, gPending.BuildStatus(),
		"1 failed attempt below MaxAttempts → build status stays Pending after reload")
	require.Equal(t, 1, gPending.BuildAttempts())

	// Passed: 2 attempts, passed.
	gPassed := byName["g-passed"]
	require.NotNil(t, gPassed)
	require.Equal(t, apply.GroupBuildStatusPassed, gPassed.BuildStatus())
	require.Equal(t, 2, gPassed.BuildAttempts())

	// Failed: 3 attempts, budget exhausted.
	gFailed := byName["g-failed"]
	require.NotNil(t, gFailed)
	require.Equal(t, apply.GroupBuildStatusFailed, gFailed.BuildStatus(),
		"3 failed attempts == MaxAttempts → build status Failed after reload")
	require.Equal(t, 3, gFailed.BuildAttempts())

	// Skipped: 0 attempts, no manifest.
	gSkipped := byName["g-skipped"]
	require.NotNil(t, gSkipped)
	require.Equal(t, apply.GroupBuildStatusSkipped, gSkipped.BuildStatus())
	require.Equal(t, 0, gSkipped.BuildAttempts())
}

// TestBoardRepo_BuildState_UpdatedInPlace verifies that a second SaveGroup
// call with updated build_status/build_attempts correctly overwrites the prior
// row via ON CONFLICT DO UPDATE — no duplicate rows, correct values.
func TestBoardRepo_BuildState_UpdatedInPlace(t *testing.T) {
	pool := setupPG(t)
	ctx := context.Background()

	cRepo := pg.NewChangeRepo(pool)
	pRepo := pg.NewPhaseRepo(pool)
	bRepo := pg.NewBoardRepo(pool)

	cid := mkChangeID(t, "01ARZ3NDEKTSV4RRFFQ69G5C21")
	pid := mkPhaseID(t, "01ARZ3NDEKTSV4RRFFQ69G5P21")
	seedChangeAndPhase(t, ctx, cRepo, pRepo, cid, pid, "build-state-upsert")

	boardID := mkBoardID(t, "01ARZ3NDEKTSV4RRFFQ69G5BD5")
	board := apply.HydrateBoard(boardID, pid, apply.BoardStatusBuilding, nil)
	require.NoError(t, bRepo.SaveBoard(ctx, board))

	gid := mkGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5GU2")

	// First save: 0 attempts, Pending.
	g0 := apply.HydrateGroup(gid, boardID, "upsert-group", nil,
		apply.GroupStatusRunning, "/wt/upsert", "sophia/upsert",
		apply.GroupBuildStatusPending, 0,
	)
	require.NoError(t, bRepo.SaveGroup(ctx, g0))

	// Second save after first build attempt (still Pending, 1 attempt).
	g1 := apply.HydrateGroup(gid, boardID, "upsert-group", nil,
		apply.GroupStatusRunning, "/wt/upsert", "sophia/upsert",
		apply.GroupBuildStatusPending, 1,
	)
	require.NoError(t, bRepo.SaveGroup(ctx, g1))

	// Third save after second successful build.
	g2 := apply.HydrateGroup(gid, boardID, "upsert-group", nil,
		apply.GroupStatusCompleted, "/wt/upsert", "sophia/upsert",
		apply.GroupBuildStatusPassed, 2,
	)
	require.NoError(t, bRepo.SaveGroup(ctx, g2))

	// Reload: exactly ONE group row with the final state.
	reloaded, err := bRepo.FindBoardByPhaseID(ctx, pid)
	require.NoError(t, err)
	groups := reloaded.Groups()
	require.Len(t, groups, 1,
		"ON CONFLICT DO UPDATE must update in place — no duplicate rows")
	require.Equal(t, apply.GroupBuildStatusPassed, groups[0].BuildStatus())
	require.Equal(t, 2, groups[0].BuildAttempts())
	require.Equal(t, apply.GroupStatusCompleted, groups[0].Status())
}

// TestBoardRepo_BuildState_TasksHydratedAlongsideBuildState verifies that
// AttachTaskToGroup (the Slice 1 fix) correctly populates tasks alongside the
// build state so resume can access both without a second query.
func TestBoardRepo_BuildState_TasksHydratedAlongsideBuildState(t *testing.T) {
	pool := setupPG(t)
	ctx := context.Background()

	cRepo := pg.NewChangeRepo(pool)
	pRepo := pg.NewPhaseRepo(pool)
	bRepo := pg.NewBoardRepo(pool)

	cid := mkChangeID(t, "01ARZ3NDEKTSV4RRFFQ69G5C22")
	pid := mkPhaseID(t, "01ARZ3NDEKTSV4RRFFQ69G5P22")
	seedChangeAndPhase(t, ctx, cRepo, pRepo, cid, pid, "build-state-with-tasks")

	boardID := mkBoardID(t, "01ARZ3NDEKTSV4RRFFQ69G5BD6")
	board := apply.HydrateBoard(boardID, pid, apply.BoardStatusBuilding, nil)
	require.NoError(t, bRepo.SaveBoard(ctx, board))

	// Group with build state Passed and 2 tasks.
	gid := mkGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5GT2")
	group := apply.HydrateGroup(gid, boardID, "tasks-and-build", nil,
		apply.GroupStatusCompleted, "/wt/tasks-and-build", "sophia/tasks-and-build",
		apply.GroupBuildStatusPassed, 1,
	)
	require.NoError(t, bRepo.SaveGroup(ctx, group))

	tid1 := mkTaskIDInt(t, "01ARZ3NDEKTSV4RRFFQ69G5TK3")
	task1, err := apply.NewTask(tid1, gid, "implement auth handler", []string{"internal/auth/*.go"})
	require.NoError(t, err)
	require.NoError(t, bRepo.SaveTask(ctx, task1))

	tid2 := mkTaskIDInt(t, "01ARZ3NDEKTSV4RRFFQ69G5TK4")
	task2, err := apply.NewTask(tid2, gid, "add integration test", []string{"test/integration/*.go"})
	require.NoError(t, err)
	require.NoError(t, bRepo.SaveTask(ctx, task2))

	// Reload: verify build state AND task list survive together.
	reloaded, err := bRepo.FindBoardByPhaseID(ctx, pid)
	require.NoError(t, err)
	groups := reloaded.Groups()
	require.Len(t, groups, 1)

	g := groups[0]
	require.Equal(t, apply.GroupBuildStatusPassed, g.BuildStatus(),
		"build status must survive reload even when tasks are attached")
	require.Equal(t, 1, g.BuildAttempts(),
		"build attempt count must survive reload alongside task hydration")
	require.Len(t, g.Tasks(), 2,
		"both tasks must be hydrated via AttachTaskToGroup")
}

// TestBoardRepo_BuildState_NotFound verifies that FindBoardByPhaseID returns
// ErrNotFound when no board exists for the given phase ID.
func TestBoardRepo_BuildState_NotFound(t *testing.T) {
	pool := setupPG(t)
	ctx := context.Background()
	bRepo := pg.NewBoardRepo(pool)

	pid := mkPhaseID(t, "01ARZ3NDEKTSV4RRFFQ69G5PXX")
	_, err := bRepo.FindBoardByPhaseID(ctx, pid)
	require.Error(t, err)
}
