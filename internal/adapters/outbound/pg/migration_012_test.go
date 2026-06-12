//go:build integration

package pg_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	dbpkg "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/db"
)

// TestMigration012_PreState asserts the pre-migration-012 baseline:
// webhook_outbox must NOT exist before migration 012 is applied.
func TestMigration012_PreState(t *testing.T) {
	ctx := context.Background()

	// Apply exactly up to migration 011 so we get the pre-012 baseline.
	_, dsn := setupMigration009OnlyPG(t)
	require.NoError(t, dbpkg.MigrateToVersion(migrationsDir(t), dsn, 11))

	pool, err := dbpkg.Open(ctx, dbpkg.DefaultConfig(dsn))
	require.NoError(t, err)
	defer pool.Close()

	var tableExists bool
	err = pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM information_schema.tables
			WHERE table_schema = 'public'
			  AND table_name   = 'webhook_outbox'
		)
	`).Scan(&tableExists)
	require.NoError(t, err)
	require.False(t, tableExists,
		"pre-migration-012: webhook_outbox table must NOT exist at schema version 011")
}

// TestMigration012_PostUp asserts that after migration 012 is applied:
// webhook_outbox exists with all required columns, the status CHECK
// constraint, and the partial pending index.
func TestMigration012_PostUp(t *testing.T) {
	pool := setupSkillPG(t) // applies all migrations including 012
	ctx := context.Background()

	var tableExists bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM information_schema.tables
			WHERE table_schema = 'public'
			  AND table_name   = 'webhook_outbox'
		)
	`).Scan(&tableExists)
	require.NoError(t, err)
	require.True(t, tableExists, "post-migration-012: webhook_outbox table must exist")

	requiredCols := []string{
		"id", "event_type", "payload", "status",
		"attempts", "next_attempt_at", "created_at", "delivered_at",
	}
	for _, col := range requiredCols {
		var colExists bool
		err = pool.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1
				FROM information_schema.columns
				WHERE table_schema = 'public'
				  AND table_name   = 'webhook_outbox'
				  AND column_name  = $1
			)
		`, col).Scan(&colExists)
		require.NoError(t, err)
		require.True(t, colExists,
			"post-migration-012: column %q must exist in webhook_outbox", col)
	}

	// Partial pending index on (next_attempt_at) WHERE status='pending'.
	var idxExists bool
	err = pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM pg_indexes
			WHERE tablename = 'webhook_outbox'
			  AND indexname = 'idx_webhook_outbox_pending'
		)
	`).Scan(&idxExists)
	require.NoError(t, err)
	require.True(t, idxExists,
		"post-migration-012: partial pending index must exist")

	// status CHECK accepts 'pending'/'delivered', rejects anything else.
	_, okErr := pool.Exec(ctx, `
		INSERT INTO webhook_outbox (id, event_type, payload, status, attempts, next_attempt_at, created_at)
		VALUES ('01ARZ3NDEKTSV4RRFFQ69G5OB1', 'phase.archived', '\x7b7d'::bytea, 'pending', 0, now(), now())
	`)
	require.NoError(t, okErr, "inserting row with status='pending' must succeed")

	_, badErr := pool.Exec(ctx, `
		INSERT INTO webhook_outbox (id, event_type, payload, status, attempts, next_attempt_at, created_at)
		VALUES ('01ARZ3NDEKTSV4RRFFQ69G5OB2', 'phase.archived', '\x7b7d'::bytea, 'bogus', 0, now(), now())
	`)
	require.Error(t, badErr, "inserting row with invalid status must fail CHECK constraint")
}

// TestMigration012_RoundTrip asserts up+down leaves the schema at 011:
// webhook_outbox table and its index absent after down.
func TestMigration012_RoundTrip(t *testing.T) {
	ctx := context.Background()

	_, dsn := setupMigration009OnlyPG(t)
	require.NoError(t, dbpkg.MigrateUp(migrationsDir(t), dsn))

	// Down one step: removes migration 012.
	require.NoError(t, dbpkg.MigrateDown(migrationsDir(t), dsn, 1))

	pool, err := dbpkg.Open(ctx, dbpkg.DefaultConfig(dsn))
	require.NoError(t, err)
	defer pool.Close()

	var tableExists bool
	err = pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = 'webhook_outbox'
		)
	`).Scan(&tableExists)
	require.NoError(t, err)
	require.False(t, tableExists,
		"after down migration 012: webhook_outbox must not exist")

	var idxExists bool
	err = pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE tablename = 'webhook_outbox' AND indexname = 'idx_webhook_outbox_pending')
	`).Scan(&idxExists)
	require.NoError(t, err)
	require.False(t, idxExists,
		"after down migration 012: partial pending index must be absent")
}
