//go:build integration

package pg_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	dbpkg "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/db"
)

// TestMigration013_PreState asserts the pre-migration-013 state: neither
// reeval_run nor reeval_run_item exist at schema version 012.
func TestMigration013_PreState(t *testing.T) {
	ctx := context.Background()

	_, dsn := setupMigration009OnlyPG(t)
	require.NoError(t, dbpkg.MigrateToVersion(migrationsDir(t), dsn, 12))

	pool, err := dbpkg.Open(ctx, dbpkg.DefaultConfig(dsn))
	require.NoError(t, err)
	defer pool.Close()

	for _, table := range []string{"reeval_run", "reeval_run_item"} {
		var exists bool
		err = pool.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM information_schema.tables
				WHERE table_schema = 'public' AND table_name = $1
			)
		`, table).Scan(&exists)
		require.NoError(t, err)
		require.False(t, exists,
			"pre-migration-013: %q must NOT exist at schema version 012", table)
	}
}

// TestMigration013_PostUp asserts that after migration 013 both audit tables
// exist with the expected columns, the mode CHECK constraint, the FK, and the
// indexes.
func TestMigration013_PostUp(t *testing.T) {
	ctx := context.Background()

	_, dsn := setupMigration009OnlyPG(t)
	require.NoError(t, dbpkg.MigrateToVersion(migrationsDir(t), dsn, 13))

	pool, err := dbpkg.Open(ctx, dbpkg.DefaultConfig(dsn))
	require.NoError(t, err)
	defer pool.Close()

	for _, table := range []string{"reeval_run", "reeval_run_item"} {
		var exists bool
		err = pool.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM information_schema.tables
				WHERE table_schema = 'public' AND table_name = $1
			)
		`, table).Scan(&exists)
		require.NoError(t, err)
		require.True(t, exists, "post-migration-013: %q must exist", table)
	}

	// reeval_run columns.
	for _, col := range []string{"id", "mode", "reverts_run_id", "created_at"} {
		var exists bool
		err = pool.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = 'public' AND table_name = 'reeval_run' AND column_name = $1
			)
		`, col).Scan(&exists)
		require.NoError(t, err)
		require.True(t, exists, "reeval_run.%s must exist", col)
	}

	// reeval_run_item columns.
	for _, col := range []string{"id", "run_id", "skill_id", "prior_status", "new_status"} {
		var exists bool
		err = pool.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = 'public' AND table_name = 'reeval_run_item' AND column_name = $1
			)
		`, col).Scan(&exists)
		require.NoError(t, err)
		require.True(t, exists, "reeval_run_item.%s must exist", col)
	}

	// mode CHECK accepts 'apply' and rejects an unknown mode.
	_, okErr := pool.Exec(ctx, `
		INSERT INTO reeval_run (id, mode, created_at)
		VALUES ('01ARZ3NDEKTSV4RRFFQ69G5RN1', 'apply', now())
	`)
	require.NoError(t, okErr, "mode='apply' must satisfy the CHECK constraint")

	_, badErr := pool.Exec(ctx, `
		INSERT INTO reeval_run (id, mode, created_at)
		VALUES ('01ARZ3NDEKTSV4RRFFQ69G5RN2', 'nope', now())
	`)
	require.Error(t, badErr, "an unknown mode must violate the CHECK constraint")

	// FK: an item referencing a missing run must fail.
	_, fkErr := pool.Exec(ctx, `
		INSERT INTO reeval_run_item (id, run_id, skill_id, prior_status, new_status)
		VALUES ('01ARZ3NDEKTSV4RRFFQ69G5IT1', '01ARZ3NDEKTSV4RRFFQ69G5XXX',
		        '01ARZ3NDEKTSV4RRFFQ69G5SK1', 'active', 'deprecated')
	`)
	require.Error(t, fkErr, "an item with a dangling run_id must violate the FK")

	for _, idx := range []string{"idx_reeval_run_created", "idx_reeval_run_item_run"} {
		var exists bool
		err = pool.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE indexname = $1)
		`, idx).Scan(&exists)
		require.NoError(t, err)
		require.True(t, exists, "index %q must exist", idx)
	}
}

// TestMigration013_RoundTrip asserts up+down leaves the schema at 012:
// both audit tables absent after a single down step.
func TestMigration013_RoundTrip(t *testing.T) {
	ctx := context.Background()

	_, dsn := setupMigration009OnlyPG(t)
	require.NoError(t, dbpkg.MigrateToVersion(migrationsDir(t), dsn, 13))
	require.NoError(t, dbpkg.MigrateDown(migrationsDir(t), dsn, 1))

	pool, err := dbpkg.Open(ctx, dbpkg.DefaultConfig(dsn))
	require.NoError(t, err)
	defer pool.Close()

	for _, table := range []string{"reeval_run", "reeval_run_item"} {
		var exists bool
		err = pool.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM information_schema.tables
				WHERE table_schema = 'public' AND table_name = $1
			)
		`, table).Scan(&exists)
		require.NoError(t, err)
		require.False(t, exists,
			"after down migration 013: %q must not exist", table)
	}
}
