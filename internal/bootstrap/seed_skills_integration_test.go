//go:build integration

package bootstrap_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/pg"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/bootstrap"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
	dbpkg "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/db"
)

// setupSkillIntegPG spins a Postgres 16 testcontainer, applies all migrations,
// and returns a connected pool. Skips when Docker is unavailable.
func setupSkillIntegPG(t *testing.T) *pgxpool.Pool {
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
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	migsDir := seedIntegMigrationsDir(t)
	require.NoError(t, dbpkg.MigrateUp(migsDir, dsn))

	pool, err := dbpkg.Open(ctx, dbpkg.DefaultConfig(dsn))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func seedIntegMigrationsDir(t *testing.T) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	abs, err := filepath.Abs(filepath.Join(filepath.Dir(here), "..", "..", "migrations", "postgres"))
	require.NoError(t, err)
	require.DirExists(t, abs)
	return abs
}

// skipIfNoDocker skips when the Docker socket is unavailable.
// Defined here because integration tests in bootstrap_test are in a
// separate build-tagged file; the helper from pg_test package is not
// accessible here.
func skipIfNoDocker(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/var/run/docker.sock"); os.IsNotExist(err) {
		if os.Getenv("DOCKER_HOST") == "" {
			t.Skip("Docker socket not available — skipping integration test")
		}
	}
}

// TestSeedSkills_Integration_EmptyTable seeds against a real Postgres container.
// Verifies all 9 rows are inserted and a second run is idempotent (D-M1-4).
func TestSeedSkills_Integration_EmptyTable(t *testing.T) {
	pool := setupSkillIntegPG(t)
	repo := pg.NewSkillRepo(pool)
	clock := shared.SystemClock{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx := context.Background()

	require.NoError(t, bootstrap.SeedSkills(ctx, repo, clock, logger))

	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 9, "all 9 skills must be present after seeding an empty table")

	// Second run — Upsert is idempotent; row count must stay at 9.
	require.NoError(t, bootstrap.SeedSkills(ctx, repo, clock, logger))
	list2, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list2, 9, "second seeder run must not change row count")
}

// TestSeedSkills_Integration_V41Payload verifies that each seeded row carries the
// V4.1 §7 legacy payload: status=active, version=v1, activation_source=legacy_seed,
// risk_level=medium (D-M1-2).
func TestSeedSkills_Integration_V41Payload(t *testing.T) {
	pool := setupSkillIntegPG(t)
	repo := pg.NewSkillRepo(pool)
	clock := shared.SystemClock{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx := context.Background()

	require.NoError(t, bootstrap.SeedSkills(ctx, repo, clock, logger))

	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 9)

	for _, s := range list {
		require.Equal(t, skill.StatusActive, s.Status(),
			"seed skill %q must have status=active (V4.1 §7)", s.Name())
		require.Equal(t, "v1", s.Version(),
			"seed skill %q must have version=v1 (V4.1 §7)", s.Name())
		require.Equal(t, skill.SourceLegacySeed, s.ActivationSource(),
			"seed skill %q must have activation_source=legacy_seed (V4.1 §7)", s.Name())
		require.Equal(t, skill.RiskMedium, s.RiskLevel(),
			"seed skill %q must have risk_level=medium (V4.1 §7)", s.Name())
	}
}

// TestSeedSkills_Integration_UpsertReplacesStaleRow verifies that Upsert replaces
// a stale row on re-seed, delivering the canonical V4.1 §7 legacy payload (D-M1-4).
// This is the M1 migration contract: operator edits are scoped to name+version bump;
// the seeder owns the canonical v1 content.
func TestSeedSkills_Integration_UpsertReplacesStaleRow(t *testing.T) {
	pool := setupSkillIntegPG(t)
	repo := pg.NewSkillRepo(pool)
	clock := shared.SystemClock{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx := context.Background()

	// First seed — populate all 9 rows.
	require.NoError(t, bootstrap.SeedSkills(ctx, repo, clock, logger))

	// Simulate stale content: overwrite apply-implement-safely via Upsert.
	list, err := repo.List(ctx)
	require.NoError(t, err)

	var applyRow *skill.Skill
	for _, s := range list {
		if s.Name() == "apply-implement-safely" {
			applyRow = s
			break
		}
	}
	require.NotNil(t, applyRow, "apply-implement-safely must exist after first seed")

	const staleContent = "STALE CONTENT: should be replaced by re-seed."
	require.NoError(t, applyRow.Update(
		applyRow.Name(), applyRow.Phases(), staleContent, applyRow.Techniques(), skill.LifecycleInput{}, time.Now(),
	))
	require.NoError(t, repo.Upsert(ctx, applyRow))

	// Re-seed — Upsert must replace the stale row with canonical content.
	require.NoError(t, bootstrap.SeedSkills(ctx, repo, clock, logger))

	list2, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list2, 9)

	// Canonical seeds — build expected content for apply-implement-safely.
	seeds, err := bootstrap.ExportedBuildSeedSkills(time.Now())
	require.NoError(t, err)
	var canonicalContent string
	for _, s := range seeds {
		if s.Name() == "apply-implement-safely" {
			canonicalContent = s.Content()
			break
		}
	}
	require.NotEmpty(t, canonicalContent)

	for _, s := range list2 {
		if s.Name() == "apply-implement-safely" {
			require.Equal(t, canonicalContent, s.Content(),
				"Upsert must restore canonical seed content on re-seed")
		}
	}
}
