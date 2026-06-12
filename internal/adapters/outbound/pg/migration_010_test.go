//go:build integration

package pg_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	dbpkg "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/db"
)

// setupMigration009OnlyPG spins a Postgres 16 container and applies migrations
// up to and including version 009 only, giving the pre-010 baseline regardless
// of how many migrations exist on the branch. Tests that verify 010 up/down
// behaviour call MigrateUp / MigrateDown on the returned DSN after this setup.
func setupMigration009OnlyPG(t *testing.T) (pool *pgxpool.Pool, dsn string) {
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

	dsn, err = container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// Apply exactly up to migration 009 so the pre-010 baseline is stable
	// regardless of how many later migrations exist in the directory.
	require.NoError(t, dbpkg.MigrateToVersion(migrationsDir(t), dsn, 9))

	pool, err = dbpkg.Open(ctx, dbpkg.DefaultConfig(dsn))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool, dsn
}

// TestMigration010_PreState asserts the pre-migration-010 state (C.1 RED gate):
//   - The skills table has exactly 7 columns (the 009 schema).
//   - The skills_name_unique constraint is present.
//   - None of the 9 M1 lifecycle columns are present.
func TestMigration010_PreState(t *testing.T) {
	// C.1 RED: This test will fail until migration 010 exists because the
	// pre-state assertions run against whatever is currently applied.
	// We assert the state AFTER all current migrations — which is the 009
	// baseline on this branch before 010 is created.
	pool, _ := setupMigration009OnlyPG(t)
	ctx := context.Background()

	// Assert 7 columns in the skills table.
	var count int
	err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND table_name   = 'skills'
	`).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 7, count,
		"pre-migration-010: skills must have exactly 7 columns (009 baseline)")

	// Assert skills_name_unique constraint exists.
	var constraintExists bool
	err = pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM   pg_constraint c
			JOIN   pg_class t ON c.conrelid = t.oid
			WHERE  t.relname     = 'skills'
			  AND  c.conname     = 'skills_name_unique'
			  AND  c.contype     = 'u'
		)
	`).Scan(&constraintExists)
	require.NoError(t, err)
	require.True(t, constraintExists,
		"pre-migration-010: skills_name_unique constraint must exist")

	// Assert none of the 9 lifecycle columns are present yet.
	lifecycleCols := []string{
		"status", "version", "scope", "applies_when",
		"risk_level", "activation_source", "metrics",
		"last_used_at", "last_validated_at",
	}
	for _, col := range lifecycleCols {
		var colExists bool
		err = pool.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1
				FROM information_schema.columns
				WHERE table_schema = 'public'
				  AND table_name   = 'skills'
				  AND column_name  = $1
			)
		`, col).Scan(&colExists)
		require.NoError(t, err)
		require.False(t, colExists,
			"pre-migration-010: column %q must not exist before migration 010", col)
	}
}

// TestMigration010_PostUp asserts that after migration 010 is applied
// (C.4 GREEN gate — runs after 010_skills_lifecycle.up.sql is written):
//   - The skills table gains exactly 9 new lifecycle columns.
//   - The skills_name_version_unique constraint exists.
//   - The skills_name_unique constraint is gone.
//   - The 3 new indexes are present.
func TestMigration010_PostUp(t *testing.T) {
	pool, dsn := setupMigration009OnlyPG(t)
	ctx := context.Background()

	// Apply exactly migration 010 on top of the 009 baseline.
	require.NoError(t, dbpkg.MigrateToVersion(migrationsDir(t), dsn, 10))

	// Count total columns: should be 16 after 010 (7 original + 9 new).
	var totalCols int
	err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND table_name   = 'skills'
	`).Scan(&totalCols)
	require.NoError(t, err)
	require.Equal(t, 16, totalCols,
		"post-migration-010: skills must have 16 columns total (7 original + 9 lifecycle)")

	// Assert skills_name_version_unique constraint exists.
	var nvUnique bool
	err = pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM   pg_constraint c
			JOIN   pg_class t ON c.conrelid = t.oid
			WHERE  t.relname = 'skills'
			  AND  c.conname = 'skills_name_version_unique'
			  AND  c.contype = 'u'
		)
	`).Scan(&nvUnique)
	require.NoError(t, err)
	require.True(t, nvUnique,
		"post-migration-010: skills_name_version_unique must exist")

	// Assert skills_name_unique is gone.
	var nameUnique bool
	err = pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM   pg_constraint c
			JOIN   pg_class t ON c.conrelid = t.oid
			WHERE  t.relname = 'skills'
			  AND  c.conname = 'skills_name_unique'
			  AND  c.contype = 'u'
		)
	`).Scan(&nameUnique)
	require.NoError(t, err)
	require.False(t, nameUnique,
		"post-migration-010: skills_name_unique must be gone after 010")

	// Assert 3 lifecycle indexes exist.
	for _, idx := range []string{
		"idx_skills_status",
		"idx_skills_scope_gin",
		"idx_skills_applies_gin",
	} {
		var idxExists bool
		err = pool.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1
				FROM pg_indexes
				WHERE tablename = 'skills'
				  AND indexname = $1
			)
		`, idx).Scan(&idxExists)
		require.NoError(t, err)
		require.True(t, idxExists,
			"post-migration-010: index %q must exist", idx)
	}
}

// TestMigration010_RoundTrip asserts that up+down+up leaves the schema
// identical: after down, 009 baseline is restored; after another up, 010
// schema is back.  (C.5 GREEN gate)
func TestMigration010_RoundTrip(t *testing.T) {
	_, dsn := setupMigration009OnlyPG(t)
	ctx := context.Background()

	// Apply exactly up to migration 010 on the 009 baseline (not 011+).
	require.NoError(t, dbpkg.MigrateToVersion(migrationsDir(t), dsn, 10))

	// Down migration: roll back one step (010 → 009).
	require.NoError(t, dbpkg.MigrateDown(migrationsDir(t), dsn, 1))

	pool, err := dbpkg.Open(ctx, dbpkg.DefaultConfig(dsn))
	require.NoError(t, err)
	defer pool.Close()

	// After down, skills must be back to 7 columns + skills_name_unique.
	var colCount int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema='public' AND table_name='skills'
	`).Scan(&colCount)
	require.NoError(t, err)
	require.Equal(t, 7, colCount,
		"after down: skills must revert to 7 columns")

	var nameUniqueRestored bool
	err = pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM pg_constraint c
			JOIN pg_class t ON c.conrelid=t.oid
			WHERE t.relname='skills' AND c.conname='skills_name_unique' AND c.contype='u'
		)
	`).Scan(&nameUniqueRestored)
	require.NoError(t, err)
	require.True(t, nameUniqueRestored,
		"after down: skills_name_unique must be restored")

	// Indexes must be gone after down.
	for _, idx := range []string{
		"idx_skills_status", "idx_skills_scope_gin", "idx_skills_applies_gin",
	} {
		var idxExists bool
		err = pool.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE tablename='skills' AND indexname=$1)
		`, idx).Scan(&idxExists)
		require.NoError(t, err)
		require.False(t, idxExists,
			"after down: index %q must be absent", idx)
	}

	pool.Close()

	// Re-apply up.
	require.NoError(t, dbpkg.MigrateUp(migrationsDir(t), dsn))
	pool2, err := dbpkg.Open(ctx, dbpkg.DefaultConfig(dsn))
	require.NoError(t, err)
	defer pool2.Close()

	var colCount2 int
	err = pool2.QueryRow(ctx, `
		SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema='public' AND table_name='skills'
	`).Scan(&colCount2)
	require.NoError(t, err)
	require.Equal(t, 16, colCount2, "after re-up: back to 16 columns")
}

// TestMigration010_IdempotentDown asserts that running the down migration
// a second time raises no error. (C.6 GREEN gate)
func TestMigration010_IdempotentDown(t *testing.T) {
	_, dsn := setupMigration009OnlyPG(t)

	// Apply exactly up to migration 010 on the 009 baseline.
	require.NoError(t, dbpkg.MigrateToVersion(migrationsDir(t), dsn, 10))

	// First down: normal rollback (010 → 009).
	require.NoError(t, dbpkg.MigrateDown(migrationsDir(t), dsn, 1),
		"first down migration must succeed")

	// Second down: already at 009 — should be a no-op or succeed gracefully.
	// golang-migrate returns nil when there are no more migrations to revert.
	err := dbpkg.MigrateDown(migrationsDir(t), dsn, 1)
	// Accept nil (no-op) as success — already at the floor.
	// Some migrate versions return migrate.ErrNoChange; we treat that as success.
	if err != nil {
		require.Contains(t, err.Error(), "no change",
			"second down must be idempotent: got unexpected error: %v", err)
	}
}

// TestMigration010_CheckConstraints asserts that V4.1 §5.2 CHECK constraints
// accept all valid enum values and reject invalid ones. (C.7 GREEN gate)
func TestMigration010_CheckConstraints(t *testing.T) {
	pool, dsn := setupMigration009OnlyPG(t)
	ctx := context.Background()

	// Apply exactly up to migration 010 to get the CHECK constraints.
	require.NoError(t, dbpkg.MigrateToVersion(migrationsDir(t), dsn, 10))

	// Helper to attempt an INSERT with specific enum values.
	// Each call uses a unique id/name derived from the subtest index.
	tryInsert := func(t *testing.T, id, name, status, risk, source string) error {
		t.Helper()
		_, err := pool.Exec(ctx, `
			INSERT INTO skills (id, name, phases, content, techniques, status, version,
			                    scope, applies_when, risk_level, activation_source,
			                    metrics, created_at, updated_at)
			VALUES ($1, $2, '{apply}', 'test content', '{inline-why}',
			        $3, 'v1', '{}', '{}', $4, $5, '{}', now(), now())`,
			id, name, status, risk, source)
		return err
	}

	// All 6 valid status values — must be accepted.
	// IDs are exactly 26 chars (ULID-alphabet, collision-free within this test).
	statusIDs := []string{
		"01JZSTATUS00000000CANDIDAT",
		"01JZSTATUS00000000VALIDATD",
		"01JZSTATUS00000000ACTIVE00",
		"01JZSTATUS00000000DEPRECTD",
		"01JZSTATUS00000000BLOCKED0",
		"01JZSTATUS00000000ARCHIVED",
	}
	for i, s := range []string{"candidate", "validated", "active", "deprecated", "blocked", "archived"} {
		require.NoError(t,
			tryInsert(t, statusIDs[i], "s-"+s, s, "medium", "manual"),
			"valid status=%q must be accepted", s)
	}
	// Invalid status — must be rejected.
	require.Error(t,
		tryInsert(t, "01JZSTATUSBAD000000UNKNOWN", "s-bad", "unknown", "medium", "manual"),
		"invalid status='unknown' must be rejected by CHECK constraint")

	// All 5 valid activation_source values — must be accepted.
	sourceIDs := []string{
		"01JZSOURCE00000000MANUAL00",
		"01JZSOURCE00000000LEGACYSD",
		"01JZSOURCE00000000ARCHIVWR",
		"01JZSOURCE00000000LLMPROPL",
		"01JZSOURCE00000000IMPORTED",
	}
	for i, src := range []string{"manual", "legacy_seed", "archive_worker", "llm_proposal", "imported"} {
		require.NoError(t,
			tryInsert(t, sourceIDs[i], "src-"+src, "active", "medium", src),
			"valid activation_source=%q must be accepted", src)
	}
	// Invalid source — must be rejected.
	require.Error(t,
		tryInsert(t, "01JZSOURCEBAD000000BADSRC0", "src-bad", "active", "medium", "bad_source"),
		"invalid activation_source='bad_source' must be rejected")

	// All 4 valid risk_level values — must be accepted.
	riskIDs := []string{
		"01JZRISK0000000000000LOW00",
		"01JZRISK0000000000000MEDM0",
		"01JZRISK0000000000000HIGH0",
		"01JZRISK0000000000CRITICAL",
	}
	for i, risk := range []string{"low", "medium", "high", "critical"} {
		require.NoError(t,
			tryInsert(t, riskIDs[i], "risk-"+risk, "active", risk, "manual"),
			"valid risk_level=%q must be accepted", risk)
	}
	// Invalid risk level — must be rejected.
	require.Error(t,
		tryInsert(t, "01JZRISKBAD0000000UNKNOWNR", "risk-bad", "active", "unknown_risk", "manual"),
		"invalid risk_level='unknown_risk' must be rejected")
}
