//go:build integration

package integration_test

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
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	dbpkg "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/db"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// migrationsPath returns the absolute path to migrations/postgres relative
// to this test file. Required because golang-migrate's file:// source needs
// an absolute path.
func migrationsPath(t *testing.T) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	abs, err := filepath.Abs(filepath.Join(filepath.Dir(here), "..", "..", "migrations", "postgres"))
	require.NoError(t, err)
	require.DirExists(t, abs)
	return abs
}

// setupPG spins a Postgres 16 testcontainer, applies migrations, and returns
// the connected pool plus a t.Cleanup-bound teardown.
func setupPG(t *testing.T) *pgxpool.Pool {
	t.Helper()
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

	require.NoError(t, dbpkg.MigrateUp(migrationsPath(t), dsn))

	pool, err := dbpkg.Open(ctx, dbpkg.DefaultConfig(dsn))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func mkChangeID(t *testing.T, raw string) ids.ChangeID {
	t.Helper()
	id, err := ids.ParseChangeID(raw)
	require.NoError(t, err)
	return id
}

func mkPhaseID(t *testing.T, raw string) ids.PhaseID {
	t.Helper()
	id, err := ids.ParsePhaseID(raw)
	require.NoError(t, err)
	return id
}

func TestChangeRepo_RoundTrip(t *testing.T) {
	pool := setupPG(t)
	repo := pg.NewChangeRepo(pool)

	id := mkChangeID(t, "01ARZ3NDEKTSV4RRFFQ69G5C01")
	now := time.Now().UTC().Truncate(time.Second)
	c, err := change.New(id, "feat-x", "proj", change.ArtifactStoreMemoryEngine, "main", now)
	require.NoError(t, err)
	require.NoError(t, repo.Save(context.Background(), c))

	got, err := repo.FindByID(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, c.Name(), got.Name())
	require.Equal(t, c.Project(), got.Project())
	require.Equal(t, c.Status(), got.Status())
	require.Equal(t, c.ArtifactStore(), got.ArtifactStore())

	byPair, err := repo.FindByProjectName(context.Background(), "proj", "feat-x")
	require.NoError(t, err)
	require.Equal(t, id, byPair.ID())

	list, err := repo.List(context.Background(), "proj", "active", 50, 0)
	require.NoError(t, err)
	require.Len(t, list, 1)
}

func TestChangeRepo_NotFound(t *testing.T) {
	pool := setupPG(t)
	repo := pg.NewChangeRepo(pool)
	_, err := repo.FindByID(context.Background(), mkChangeID(t, "01ARZ3NDEKTSV4RRFFQ69G5MIS"))
	require.ErrorIs(t, err, outbound.ErrNotFound)
}

func TestChangeRepo_UniqueProjectName(t *testing.T) {
	pool := setupPG(t)
	repo := pg.NewChangeRepo(pool)
	now := time.Now().UTC().Truncate(time.Second)

	c1, _ := change.New(mkChangeID(t, "01ARZ3NDEKTSV4RRFFQ69G5C01"), "feat-x", "proj", change.ArtifactStoreMemoryEngine, "", now)
	require.NoError(t, repo.Save(context.Background(), c1))

	c2, _ := change.New(mkChangeID(t, "01ARZ3NDEKTSV4RRFFQ69G5C02"), "feat-x", "proj", change.ArtifactStoreMemoryEngine, "", now)
	err := repo.Save(context.Background(), c2)
	require.Error(t, err, "duplicate (project,name) must fail UNIQUE")
}

func TestPhaseRepo_RoundTrip(t *testing.T) {
	pool := setupPG(t)
	cRepo := pg.NewChangeRepo(pool)
	pRepo := pg.NewPhaseRepo(pool)

	now := time.Now().UTC().Truncate(time.Second)
	cid := mkChangeID(t, "01ARZ3NDEKTSV4RRFFQ69G5C01")
	c, _ := change.New(cid, "feat-x", "proj", change.ArtifactStoreMemoryEngine, "", now)
	require.NoError(t, cRepo.Save(context.Background(), c))

	pid := mkPhaseID(t, "01ARZ3NDEKTSV4RRFFQ69G5P01")
	p, err := phase.New(pid, cid, phase.PhaseSpec, 3)
	require.NoError(t, err)
	require.NoError(t, p.Start(now))
	require.NoError(t, pRepo.Save(context.Background(), p))

	got, err := pRepo.FindByID(context.Background(), pid)
	require.NoError(t, err)
	require.Equal(t, phase.PhaseStatusRunning, got.Status())
	require.Equal(t, 1, got.Attempts())

	// Complete the phase.
	env := &envelope.Envelope{
		SchemaVersion: envelope.SchemaVersionV1,
		Phase:         "spec",
		ChangeName:    "feat-x", Project: "proj",
		Status: envelope.StatusDone, Confidence: 0.85,
	}
	require.NoError(t, p.Complete(env, now.Add(time.Minute)))
	require.NoError(t, pRepo.Save(context.Background(), p))

	got, err = pRepo.FindByID(context.Background(), pid)
	require.NoError(t, err)
	require.Equal(t, phase.PhaseStatusDone, got.Status())
	require.NotNil(t, got.Envelope())
	require.Equal(t, envelope.StatusDone, got.Envelope().Status)

	byType, err := pRepo.FindByChangeAndType(context.Background(), cid, phase.PhaseSpec)
	require.NoError(t, err)
	require.Equal(t, pid, byType.ID())

	// FindRunningByChange should now return ErrNotFound (phase is done).
	_, err = pRepo.FindRunningByChange(context.Background(), cid)
	require.ErrorIs(t, err, outbound.ErrNotFound)
}

func TestPhaseRepo_AdvisoryLock(t *testing.T) {
	pool := setupPG(t)
	pRepo := pg.NewPhaseRepo(pool)

	cid := mkChangeID(t, "01ARZ3NDEKTSV4RRFFQ69G5C01")
	// Advisory lock requires a transaction; we exec it standalone here, and
	// the lock is released when the connection returns to the pool.
	require.NoError(t, pRepo.LockByChange(context.Background(), cid))
}

func TestAuditLog_Append(t *testing.T) {
	pool := setupPG(t)
	a := pg.NewAuditLog(pool)
	cid := mkChangeID(t, "01ARZ3NDEKTSV4RRFFQ69G5C01")
	require.NoError(t, a.Append(context.Background(), outbound.AuditEvent{
		ChangeID:   &cid,
		EventType:  "phase.started",
		Payload:    []byte(`{"phase":"spec"}`),
		OccurredAt: time.Now().UTC(),
	}))

	var n int
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM audit_log").Scan(&n))
	require.Equal(t, 1, n)
}

func TestSpawnGovernorState_AcquireRelease(t *testing.T) {
	pool := setupPG(t)
	r := pg.NewSpawnGovernorRepo(pool)
	ctx := context.Background()

	ok1, n1, err := r.Acquire(ctx, 2)
	require.NoError(t, err)
	require.True(t, ok1)
	require.Equal(t, 1, n1)

	ok2, n2, err := r.Acquire(ctx, 2)
	require.NoError(t, err)
	require.True(t, ok2)
	require.Equal(t, 2, n2)

	// At cap — third acquire fails.
	ok3, _, err := r.Acquire(ctx, 2)
	require.NoError(t, err)
	require.False(t, ok3)

	require.NoError(t, r.Release(ctx))
	active, err := r.Active(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, active)

	// Release below zero floors at 0.
	require.NoError(t, r.Release(ctx))
	require.NoError(t, r.Release(ctx)) // extra release; should not go negative
	active, err = r.Active(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, active)
}

// Verifies that two acquires + the cap-reached path return the same
// numerical state without mutating shared infrastructure.
func TestSpawnGovernorState_HighConcurrency(t *testing.T) {
	pool := setupPG(t)
	r := pg.NewSpawnGovernorRepo(pool)
	ctx := context.Background()

	const goroutines = 8
	const max = 4
	results := make(chan bool, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			ok, _, err := r.Acquire(ctx, max)
			require.NoError(t, err)
			results <- ok
		}()
	}
	acquired := 0
	for i := 0; i < goroutines; i++ {
		if <-results {
			acquired++
		}
	}
	require.Equal(t, max, acquired, "exactly max=4 acquires must succeed under concurrency")
	active, _ := r.Active(ctx)
	require.Equal(t, max, active)
}

// _ disposes unused helpers to silence lint warnings on partial tests.
var _ = os.Getenv
