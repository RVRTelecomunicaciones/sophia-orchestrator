//go:build integration

package pg_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/pg"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
	dbpkg "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/db"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// setupSkillPG spins a Postgres 16 testcontainer, applies all migrations
// (including 009_skills), and returns a connected pool.
//
// This helper is local to this test file; when Docker is unavailable the
// test is skipped via skipIfNoDocker.
func setupSkillPG(t *testing.T) *pgxpool.Pool {
	t.Helper()
	skipIfNoDocker(t)

	ctx := context.Background()

	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("sophia_test"),
		tcpostgres.WithUsername("sophia"),
		tcpostgres.WithPassword("sophia"),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = container.Terminate(ctx)
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	require.NoError(t, dbpkg.MigrateUp(migrationsDir(t), dsn))

	pool, err := dbpkg.Open(ctx, dbpkg.DefaultConfig(dsn))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// migrationsDir returns the absolute path to migrations/postgres.
func migrationsDir(t *testing.T) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	abs, err := filepath.Abs(filepath.Join(filepath.Dir(here), "..", "..", "..", "..", "migrations", "postgres"))
	require.NoError(t, err)
	require.DirExists(t, abs)
	return abs
}

// skipIfNoDocker skips the test when the Docker socket is unavailable so that
// CI without Docker support does not fail the integration suite.
func skipIfNoDocker(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/var/run/docker.sock"); os.IsNotExist(err) {
		// Also check DOCKER_HOST for non-standard sockets.
		if os.Getenv("DOCKER_HOST") == "" {
			t.Skip("Docker socket not available — skipping integration test (run in CI with Docker)")
		}
	}
}

func mustSkillIDInteg(t *testing.T, raw string) ids.SkillID {
	t.Helper()
	id, err := ids.ParseSkillID(raw)
	require.NoError(t, err)
	return id
}

var (
	integNow = time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	skillID1 = "01ARZ3NDEKTSV4RRFFQ69G5SK1"
	skillID2 = "01ARZ3NDEKTSV4RRFFQ69G5SK2"
	skillID3 = "01ARZ3NDEKTSV4RRFFQ69G5SK3"
)

func newTestSkill(t *testing.T, rawID, name string, phases []phase.PhaseType) *skill.Skill {
	t.Helper()
	s, err := skill.New(
		mustSkillIDInteg(t, rawID),
		name,
		phases,
		"Apply constitutional self-critique to review each change before committing.",
		[]skill.Technique{skill.TechniqueConstitutionalSelfCritique, skill.TechniqueInlineWhy},
		skill.LifecycleInput{},
		integNow,
	)
	require.NoError(t, err)
	return s
}

// ── Migration 009 sanity check ────────────────────────────────────────────────

func TestSkillRepo_Migration009_TableExists(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)

	// List on an empty table must return an empty slice, not an error.
	skills, err := repo.List(context.Background())
	require.NoError(t, err)
	require.Empty(t, skills)
}

// ── Upsert ────────────────────────────────────────────────────────────────────

func TestSkillRepo_Upsert_Insert(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	s := newTestSkill(t, skillID1, "apply-implement-safely", []phase.PhaseType{phase.PhaseApply})
	require.NoError(t, repo.Upsert(ctx, s))

	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "apply-implement-safely", list[0].Name())
}

func TestSkillRepo_Upsert_Replaces(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	s := newTestSkill(t, skillID1, "apply-implement-safely", []phase.PhaseType{phase.PhaseApply})
	require.NoError(t, repo.Upsert(ctx, s))

	// Update and upsert again.
	later := integNow.Add(time.Hour)
	require.NoError(t, s.Update("apply-implement-safely",
		[]phase.PhaseType{phase.PhaseApply, phase.PhaseVerify},
		"Revised guidance: step-back before diving in.",
		[]skill.Technique{skill.TechniqueStepBack},
		skill.LifecycleInput{},
		later,
	))
	require.NoError(t, repo.Upsert(ctx, s))

	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "Revised guidance: step-back before diving in.", list[0].Content())
	require.Equal(t, []phase.PhaseType{phase.PhaseApply, phase.PhaseVerify}, list[0].Phases())
}

// ── InsertIfAbsent ────────────────────────────────────────────────────────────

func TestSkillRepo_InsertIfAbsent_InsertsOnEmpty(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	s := newTestSkill(t, skillID1, "apply-implement-safely", []phase.PhaseType{phase.PhaseApply})
	require.NoError(t, repo.InsertIfAbsent(ctx, s))

	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
}

func TestSkillRepo_InsertIfAbsent_NoOpOnExisting(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	original := newTestSkill(t, skillID1, "apply-implement-safely", []phase.PhaseType{phase.PhaseApply})
	require.NoError(t, repo.Upsert(ctx, original))

	// Operator edits content directly (simulated via Upsert).
	later := integNow.Add(time.Hour)
	require.NoError(t, original.Update("apply-implement-safely",
		[]phase.PhaseType{phase.PhaseApply},
		"OPERATOR EDITED: do not overwrite me.",
		[]skill.Technique{skill.TechniqueStepBack},
		skill.LifecycleInput{},
		later,
	))
	require.NoError(t, repo.Upsert(ctx, original))

	// InsertIfAbsent with the same name but different content must not overwrite.
	different := newTestSkill(t, skillID2, "apply-implement-safely", []phase.PhaseType{phase.PhaseApply})
	require.NoError(t, repo.InsertIfAbsent(ctx, different))

	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "OPERATOR EDITED: do not overwrite me.", list[0].Content(),
		"InsertIfAbsent must not overwrite operator-edited content")
}

func TestSkillRepo_InsertIfAbsent_IdempotentMultipleCalls(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	s := newTestSkill(t, skillID1, "apply-implement-safely", []phase.PhaseType{phase.PhaseApply})

	// Three consecutive calls must all succeed and produce exactly one row.
	require.NoError(t, repo.InsertIfAbsent(ctx, s))
	require.NoError(t, repo.InsertIfAbsent(ctx, s))
	require.NoError(t, repo.InsertIfAbsent(ctx, s))

	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
}

// ── FindByPhase ───────────────────────────────────────────────────────────────

func TestSkillRepo_FindByPhase_MatchingRow(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	applySkill := newTestSkill(t, skillID1, "apply-implement-safely", []phase.PhaseType{phase.PhaseApply})
	verifySkill := newTestSkill(t, skillID2, "verify-chain-validation", []phase.PhaseType{phase.PhaseVerify})
	multiSkill := newTestSkill(t, skillID3, "multi-phase", []phase.PhaseType{phase.PhaseApply, phase.PhaseVerify})

	require.NoError(t, repo.Upsert(ctx, applySkill))
	require.NoError(t, repo.Upsert(ctx, verifySkill))
	require.NoError(t, repo.Upsert(ctx, multiSkill))

	got, err := repo.FindByPhase(ctx, phase.PhaseApply)
	require.NoError(t, err)
	require.Len(t, got, 2, "apply-implement-safely and multi-phase must both match")

	names := make(map[string]struct{}, len(got))
	for _, s := range got {
		names[s.Name()] = struct{}{}
	}
	require.Contains(t, names, "apply-implement-safely")
	require.Contains(t, names, "multi-phase")
}

func TestSkillRepo_FindByPhase_NoMatchReturnsEmpty(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	s := newTestSkill(t, skillID1, "apply-implement-safely", []phase.PhaseType{phase.PhaseApply})
	require.NoError(t, repo.Upsert(ctx, s))

	got, err := repo.FindByPhase(ctx, phase.PhaseDesign)
	require.NoError(t, err)
	require.Empty(t, got, "no skill matches design phase — must return empty slice, not error")
}

func TestSkillRepo_FindByPhase_EmptyTableReturnsEmpty(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	got, err := repo.FindByPhase(ctx, phase.PhaseApply)
	require.NoError(t, err)
	require.Empty(t, got)
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestSkillRepo_List_MultipleRows(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	s1 := newTestSkill(t, skillID1, "alpha", []phase.PhaseType{phase.PhaseApply})
	s2 := newTestSkill(t, skillID2, "beta", []phase.PhaseType{phase.PhaseVerify})
	require.NoError(t, repo.Upsert(ctx, s1))
	require.NoError(t, repo.Upsert(ctx, s2))

	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2)
}

// ── Hydration roundtrip ───────────────────────────────────────────────────────

func TestSkillRepo_Upsert_HydrationRoundtrip(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	s, err := skill.New(
		mustSkillIDInteg(t, skillID1),
		"design-architect-system",
		[]phase.PhaseType{phase.PhaseDesign},
		"Step back and consider the full system before proposing structure.",
		[]skill.Technique{skill.TechniqueExtendedThinking, skill.TechniqueStepBack, skill.TechniqueInlineWhy},
		skill.LifecycleInput{},
		integNow,
	)
	require.NoError(t, err)
	require.NoError(t, repo.Upsert(ctx, s))

	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)

	got := list[0]
	require.Equal(t, s.ID(), got.ID())
	require.Equal(t, s.Name(), got.Name())
	require.Equal(t, s.Content(), got.Content())
	require.Equal(t, s.Phases(), got.Phases())
	require.Equal(t, s.Techniques(), got.Techniques())
	require.True(t, s.CreatedAt().Equal(got.CreatedAt()), "createdAt must round-trip")
	require.True(t, s.UpdatedAt().Equal(got.UpdatedAt()), "updatedAt must round-trip")
}

// ── NotFound sentinel via outbound package ────────────────────────────────────

func TestSkillRepo_FindByPhase_NeverReturnsErrNotFound(t *testing.T) {
	// FindByPhase must return an empty slice, NOT ErrNotFound, per the port contract.
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	got, err := repo.FindByPhase(ctx, phase.PhaseInit)
	require.NoError(t, err)
	require.NotErrorIs(t, err, outbound.ErrNotFound)
	require.NotNil(t, got)
}
