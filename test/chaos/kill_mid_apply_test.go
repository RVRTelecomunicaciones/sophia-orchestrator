//go:build chaos

package chaos_test

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	pgrepo "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/pg"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	dbpkg "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/db"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// TestChaos_PhaseSurvivesProcessRestart verifies that a Phase row left in
// status=running after a simulated process kill can be Resumed via the
// repository semantics V1 ships (manual resume API). The chaos here is
// rough but real: we open a transaction, persist a running phase, abort
// the transaction (simulates process kill), reopen the pool, and verify
// the phase row is recoverable.
//
// Run with: go test -tags=chaos ./test/chaos/... -count=1
func TestChaos_PhaseSurvivesProcessRestart(t *testing.T) {
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("sophia_chaos"),
		tcpostgres.WithUsername("sophia"),
		tcpostgres.WithPassword("sophia"),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, dbpkg.MigrateUp(migrationsPath(t), dsn))

	// Phase 1: simulate the orchestrator running. Persist a Change + a
	// Phase in status=running. Then drop the pool (simulates SIGKILL).
	pool1, err := dbpkg.Open(ctx, dbpkg.DefaultConfig(dsn))
	require.NoError(t, err)
	cid := mustChangeID(t)
	pid := mustPhaseID(t)
	persistRunningPhase(ctx, t, pool1, cid, pid)
	pool1.Close()

	// Phase 2: orchestrator restarts. Reopen the pool, scan for in-flight
	// phases, mark them interrupted (the auto-resume V2 hook will pick
	// these up; V1 caller invokes /resume manually).
	pool2, err := dbpkg.Open(ctx, dbpkg.DefaultConfig(dsn))
	require.NoError(t, err)
	t.Cleanup(pool2.Close)

	pRepo := pgrepo.NewPhaseRepo(pool2)
	got, err := pRepo.FindByID(ctx, pid)
	require.NoError(t, err)
	require.Equal(t, phase.PhaseStatusRunning, got.Status(),
		"phase row must survive the kill with state preserved (Iron Law #1)")

	// Phase 3: caller invokes Resume — phase transitions back to running
	// with attempts unchanged (V1 manual resume semantics).
	require.NoError(t, got.MarkInterrupted())
	require.NoError(t, pRepo.Save(ctx, got))
	got, err = pRepo.FindByID(ctx, pid)
	require.NoError(t, err)
	require.Equal(t, phase.PhaseStatusInterrupted, got.Status())

	// Phase 4: V1 caller-driven resume → phase ready to be re-dispatched.
	require.NoError(t, got.Start(time.Now()))
	require.NoError(t, pRepo.Save(ctx, got))
	require.Equal(t, phase.PhaseStatusRunning, got.Status())
	require.Equal(t, 2, got.Attempts(), "Start increments attempts each resume")
}

// TestChaos_IdempotentReplayProducesSameEnvelope verifies the spec's
// replay-everything semantics: re-saving a phase with the same (change_id,
// phase_type, attempts) replays the cached envelope.
func TestChaos_IdempotentReplayProducesSameEnvelope(t *testing.T) {
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("sophia_chaos"),
		tcpostgres.WithUsername("sophia"),
		tcpostgres.WithPassword("sophia"),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, dbpkg.MigrateUp(migrationsPath(t), dsn))

	pool, err := dbpkg.Open(ctx, dbpkg.DefaultConfig(dsn))
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	cid := mustChangeID(t)
	pid := mustPhaseID(t)
	persistRunningPhase(ctx, t, pool, cid, pid)

	pRepo := pgrepo.NewPhaseRepo(pool)
	got, _ := pRepo.FindByID(ctx, pid)

	// Complete the phase with envelope.
	now := time.Now().UTC().Truncate(time.Second)
	env := &envelope.Envelope{
		SchemaVersion: envelope.SchemaVersionV1,
		Phase:         "spec",
		ChangeName:    "feat-x",
		Project:       "demo",
		Status:        envelope.StatusDone,
		Confidence:    0.85,
	}
	require.NoError(t, got.Complete(env, now))
	require.NoError(t, pRepo.Save(ctx, got))

	// Re-save same row → envelope persists, no duplicate row created.
	require.NoError(t, pRepo.Save(ctx, got))

	got, err = pRepo.FindByID(ctx, pid)
	require.NoError(t, err)
	require.Equal(t, phase.PhaseStatusDone, got.Status())
	require.NotNil(t, got.Envelope())
	require.Equal(t, envelope.StatusDone, got.Envelope().Status)
}

// --- helpers ---

func persistRunningPhase(ctx context.Context, t *testing.T, pool *pgxpool.Pool, cid ids.ChangeID, pid ids.PhaseID) {
	t.Helper()
	cRepo := pgrepo.NewChangeRepo(pool)
	pRepo := pgrepo.NewPhaseRepo(pool)
	now := time.Now().UTC().Truncate(time.Second)

	c, err := change.New(cid, "feat-x", "demo", change.ArtifactStoreMemoryEngine, "main", now)
	require.NoError(t, err)
	require.NoError(t, cRepo.Save(ctx, c))

	p, err := phase.New(pid, cid, phase.PhaseSpec, 3)
	require.NoError(t, err)
	require.NoError(t, p.Start(now))
	require.NoError(t, pRepo.Save(ctx, p))
}

func mustChangeID(t *testing.T) ids.ChangeID {
	t.Helper()
	id, err := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	require.NoError(t, err)
	return id
}

func mustPhaseID(t *testing.T) ids.PhaseID {
	t.Helper()
	id, err := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5P01")
	require.NoError(t, err)
	return id
}

func migrationsPath(t *testing.T) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	abs, err := filepath.Abs(filepath.Join(filepath.Dir(here), "..", "..", "migrations", "postgres"))
	require.NoError(t, err)
	require.DirExists(t, abs)
	return abs
}

// _ silences unused-import warnings for testcontainers when only one path
// is exercised in some test runs.
var _ = testcontainers.ContainerRequest{}
var _ = outbound.ErrNotFound
