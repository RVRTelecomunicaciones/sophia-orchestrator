//go:build integration

package pg_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	dbpkg "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/db"
)

// TestMigration014_PreState asserts the pre-migration-014 baseline:
// phases.concerns must NOT exist before migration 014 is applied.
func TestMigration014_PreState(t *testing.T) {
	ctx := context.Background()

	// Apply up to the highest migration that exists before 014 in this
	// worktree (012). Migration 013 is reserved by a concurrent change and is
	// not present here, so golang-migrate's contiguous-version requirement
	// means 012 is the real pre-014 baseline.
	_, dsn := setupMigration009OnlyPG(t)
	require.NoError(t, dbpkg.MigrateToVersion(migrationsDir(t), dsn, 12))

	pool, err := dbpkg.Open(ctx, dbpkg.DefaultConfig(dsn))
	require.NoError(t, err)
	defer pool.Close()

	var colExists bool
	err = pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = 'public'
			  AND table_name   = 'phases'
			  AND column_name  = 'concerns'
		)
	`).Scan(&colExists)
	require.NoError(t, err)
	require.False(t, colExists,
		"pre-migration-014: phases.concerns column must NOT exist at schema version 013")
}

// TestMigration014_PostUp asserts that after migration 014 is applied the
// phases table has a nullable JSONB concerns column, that existing rows (and
// new rows that omit concerns) read back NULL, and that a JSONB concerns
// payload round-trips through the column.
func TestMigration014_PostUp(t *testing.T) {
	pool := setupSkillPG(t) // applies all migrations including 014
	ctx := context.Background()

	// Column exists and is JSONB and nullable.
	var dataType, isNullable string
	err := pool.QueryRow(ctx, `
		SELECT data_type, is_nullable
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND table_name   = 'phases'
		  AND column_name  = 'concerns'
	`).Scan(&dataType, &isNullable)
	require.NoError(t, err)
	require.Equal(t, "jsonb", dataType, "concerns must be JSONB")
	require.Equal(t, "YES", isNullable, "concerns must be nullable (additive, backward-compatible)")

	// Seed a parent change so the FK is satisfied.
	const changeID = "01ARZ3NDEKTSV4RRFFQ69G5F14"
	_, err = pool.Exec(ctx, `
		INSERT INTO changes (id, name, project, status, artifact_store, created_at, updated_at)
		VALUES ($1, 'concern-mig', 'proj', 'active', 'engram', now(), now())
	`, changeID)
	require.NoError(t, err)

	// A phase row that omits concerns reads back NULL (opted-out parity).
	const phaseNull = "01ARZ3NDEKTSV4RRFFQ69G5P01"
	_, err = pool.Exec(ctx, `
		INSERT INTO phases (id, change_id, phase_type, status, retry_budget, attempts)
		VALUES ($1, $2, 'spec', 'done', 3, 0)
	`, phaseNull, changeID)
	require.NoError(t, err)

	var nullConcerns *string
	err = pool.QueryRow(ctx,
		`SELECT concerns FROM phases WHERE id = $1`, phaseNull).Scan(&nullConcerns)
	require.NoError(t, err)
	require.Nil(t, nullConcerns, "phase without concerns must read back NULL")

	// A phase row with a JSONB concerns payload round-trips.
	const phaseWith = "01ARZ3NDEKTSV4RRFFQ69G5P02"
	const payload = `[{"severity":"high","category":"risk","message":"m","evidence":"risks[0].level=high"}]`
	_, err = pool.Exec(ctx, `
		INSERT INTO phases (id, change_id, phase_type, status, retry_budget, attempts, concerns)
		VALUES ($1, $2, 'spec', 'done_with_concerns', 3, 1, $3::jsonb)
	`, phaseWith, changeID, payload)
	require.NoError(t, err)

	var got string
	err = pool.QueryRow(ctx,
		`SELECT concerns::text FROM phases WHERE id = $1`, phaseWith).Scan(&got)
	require.NoError(t, err)
	require.JSONEq(t, payload, got, "concerns JSONB payload must round-trip")
}

// TestMigration014_RoundTrip asserts up+down leaves the schema at 013:
// the phases.concerns column is absent after down.
func TestMigration014_RoundTrip(t *testing.T) {
	ctx := context.Background()

	// Apply migrations up to exactly 014, then roll back one step. Because
	// migration 013 is reserved elsewhere and absent here, the single down
	// step removes 014 and lands on 012 — the schema before phases.concerns.
	_, dsn := setupMigration009OnlyPG(t)
	require.NoError(t, dbpkg.MigrateToVersion(migrationsDir(t), dsn, 14))

	// Down one step: removes migration 014.
	require.NoError(t, dbpkg.MigrateDown(migrationsDir(t), dsn, 1))

	pool, err := dbpkg.Open(ctx, dbpkg.DefaultConfig(dsn))
	require.NoError(t, err)
	defer pool.Close()

	var colExists bool
	err = pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public'
			  AND table_name   = 'phases'
			  AND column_name  = 'concerns'
		)
	`).Scan(&colExists)
	require.NoError(t, err)
	require.False(t, colExists,
		"after down migration 014: phases.concerns column must be absent")
}
