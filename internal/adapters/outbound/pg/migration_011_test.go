//go:build integration

package pg_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	dbpkg "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/db"
)

// TestMigration011_PreState asserts the pre-migration-011 state (A.1 RED gate):
// skill_usage table must NOT exist before migration 011 is applied.
func TestMigration011_PreState(t *testing.T) {
	_, dsn := setupMigration009OnlyPG(t)
	ctx := context.Background()

	// Roll back migration 011 by applying all migrations EXCEPT the last one
	// via MigrateDown once. Since setupMigration009OnlyPG applies ALL
	// migrations, and migration 011 is the new one, we roll it back once to
	// get back to the 010 baseline.
	require.NoError(t, dbpkg.MigrateDown(migrationsDir(t), dsn, 1))

	pool, err := dbpkg.Open(ctx, dbpkg.DefaultConfig(dsn))
	require.NoError(t, err)
	defer pool.Close()

	var tableExists bool
	err = pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM information_schema.tables
			WHERE table_schema = 'public'
			  AND table_name   = 'skill_usage'
		)
	`).Scan(&tableExists)
	require.NoError(t, err)
	require.False(t, tableExists,
		"pre-migration-011: skill_usage table must NOT exist at schema version 010")
}

// TestMigration011_PostUp asserts that after migration 011 is applied (A.4 GREEN gate):
// - skill_usage table exists with all required columns and constraints.
// - Both indexes are present and functional.
func TestMigration011_PostUp(t *testing.T) {
	pool, _ := setupMigration009OnlyPG(t)
	ctx := context.Background()

	// setupMigration009OnlyPG applies ALL migrations including 011.
	// Verify skill_usage table exists.
	var tableExists bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM information_schema.tables
			WHERE table_schema = 'public'
			  AND table_name   = 'skill_usage'
		)
	`).Scan(&tableExists)
	require.NoError(t, err)
	require.True(t, tableExists, "post-migration-011: skill_usage table must exist")

	// Verify all required columns are present.
	requiredCols := []string{
		"id", "change_id", "phase_type", "skill_id",
		"skill_version", "injected_at", "outcome",
	}
	for _, col := range requiredCols {
		var colExists bool
		err = pool.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1
				FROM information_schema.columns
				WHERE table_schema = 'public'
				  AND table_name   = 'skill_usage'
				  AND column_name  = $1
			)
		`, col).Scan(&colExists)
		require.NoError(t, err)
		require.True(t, colExists,
			"post-migration-011: column %q must exist in skill_usage", col)
	}

	// Verify UNIQUE constraint on (change_id, phase_type, skill_id, skill_version).
	var uniqueExists bool
	err = pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM pg_constraint c
			JOIN pg_class t ON c.conrelid = t.oid
			WHERE t.relname = 'skill_usage'
			  AND c.contype = 'u'
		)
	`).Scan(&uniqueExists)
	require.NoError(t, err)
	require.True(t, uniqueExists,
		"post-migration-011: UNIQUE constraint must exist on skill_usage")

	// Verify both indexes exist.
	for _, idx := range []string{"idx_skill_usage_change", "idx_skill_usage_skill_injected"} {
		var idxExists bool
		err = pool.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1
				FROM pg_indexes
				WHERE tablename = 'skill_usage'
				  AND indexname = $1
			)
		`, idx).Scan(&idxExists)
		require.NoError(t, err)
		require.True(t, idxExists,
			"post-migration-011: index %q must exist", idx)
	}

	// Verify outcome CHECK constraint accepts 'pending' and rejects 'invalid'.
	_, insertErr := pool.Exec(ctx, `
		INSERT INTO skill_usage (id, change_id, phase_type, skill_id, skill_version, injected_at, outcome)
		VALUES ('01ARZ3NDEKTSV4RRFFQ69G5SU1', '01ARZ3NDEKTSV4RRFFQ69G5CA1',
		        'apply', '01ARZ3NDEKTSV4RRFFQ69G5SK1', 'v1', now(), 'pending')
	`)
	require.NoError(t, insertErr, "inserting row with outcome='pending' must succeed")

	_, badInsertErr := pool.Exec(ctx, `
		INSERT INTO skill_usage (id, change_id, phase_type, skill_id, skill_version, injected_at, outcome)
		VALUES ('01ARZ3NDEKTSV4RRFFQ69G5SU2', '01ARZ3NDEKTSV4RRFFQ69G5CA1',
		        'apply', '01ARZ3NDEKTSV4RRFFQ69G5SK1', 'v2', now(), 'invalid_value')
	`)
	require.Error(t, badInsertErr, "inserting row with invalid outcome must fail CHECK constraint")
}

// TestMigration011_RoundTrip asserts that up+down leaves the schema at 010:
// skill_usage table absent after down. (A.5 GREEN gate)
func TestMigration011_RoundTrip(t *testing.T) {
	_, dsn := setupMigration009OnlyPG(t)
	ctx := context.Background()

	// Down one step: removes migration 011.
	require.NoError(t, dbpkg.MigrateDown(migrationsDir(t), dsn, 1))

	pool, err := dbpkg.Open(ctx, dbpkg.DefaultConfig(dsn))
	require.NoError(t, err)
	defer pool.Close()

	var tableExists bool
	err = pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = 'skill_usage'
		)
	`).Scan(&tableExists)
	require.NoError(t, err)
	require.False(t, tableExists,
		"after down migration 011: skill_usage must not exist")

	// Verify indexes are gone too.
	for _, idx := range []string{"idx_skill_usage_change", "idx_skill_usage_skill_injected"} {
		var idxExists bool
		err = pool.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE tablename = 'skill_usage' AND indexname = $1)
		`, idx).Scan(&idxExists)
		require.NoError(t, err)
		require.False(t, idxExists,
			"after down migration 011: index %q must be absent", idx)
	}
}
