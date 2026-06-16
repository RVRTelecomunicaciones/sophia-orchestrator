//go:build integration

package pg_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/pg"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
)

func mustParsePhaseID(t *testing.T, raw string) ids.PhaseID {
	t.Helper()
	id, err := ids.ParsePhaseID(raw)
	require.NoError(t, err)
	return id
}

func mustParseChangeIDConcern(t *testing.T, raw string) ids.ChangeID {
	t.Helper()
	id, err := ids.ParseChangeID(raw)
	require.NoError(t, err)
	return id
}

// TestPhaseRepo_Concerns_RoundTrip asserts that a phase carrying advisory
// concerns persists the concern detail and reads it back identically, while a
// phase WITHOUT concerns reads back an empty/nil slice (opted-out parity).
func TestPhaseRepo_Concerns_RoundTrip(t *testing.T) {
	pool := setupSkillPG(t) // applies all migrations including 014
	ctx := context.Background()
	repo := pg.NewPhaseRepo(pool)

	const changeID = "01ARZ3NDEKTSV4RRFFQ69G5CC1"
	_, err := pool.Exec(ctx, `
		INSERT INTO changes (id, name, project, status, artifact_store, created_at, updated_at)
		VALUES ($1, 'concern-repo', 'proj', 'active', 'engram', now(), now())
	`, changeID)
	require.NoError(t, err)

	cid := mustParseChangeIDConcern(t, changeID)
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)

	env := &envelope.Envelope{
		SchemaVersion: envelope.SchemaVersionV1,
		Phase:         string(phase.PhaseSpec),
		Status:        envelope.StatusDoneWithConcerns,
		Confidence:    0.9,
	}

	// Phase WITH concerns.
	withID := "01ARZ3NDEKTSV4RRFFQ69G5PW1"
	pWith := phase.Hydrate(
		mustParsePhaseID(t, withID), cid, phase.PhaseSpec,
		phase.PhaseStatusDoneWithConcerns, env, 0.9, 3, 1, &now, &now,
	)
	concerns := []phase.Concern{
		{Severity: "high", Category: "risk", Message: "risky", Evidence: "risks[0].level=high"},
		{Severity: "medium", Category: "confidence", Message: "low conf", Evidence: "confidence=0.4"},
	}
	pWith.SetConcerns(concerns)
	require.NoError(t, repo.Save(ctx, pWith))

	got, err := repo.FindByID(ctx, mustParsePhaseID(t, withID))
	require.NoError(t, err)
	require.Equal(t, concerns, got.Concerns(),
		"persisted concerns must round-trip identically")

	// Phase WITHOUT concerns reads back nil/empty (opted-out parity).
	envClean := &envelope.Envelope{
		SchemaVersion: envelope.SchemaVersionV1,
		Phase:         string(phase.PhaseSpec),
		Status:        envelope.StatusDone,
		Confidence:    0.95,
	}
	noneID := "01ARZ3NDEKTSV4RRFFQ69G5PN1"
	pNone := phase.Hydrate(
		mustParsePhaseID(t, noneID), cid, phase.PhaseSpec,
		phase.PhaseStatusDone, envClean, 0.95, 3, 0, &now, &now,
	)
	require.NoError(t, repo.Save(ctx, pNone))

	gotNone, err := repo.FindByID(ctx, mustParsePhaseID(t, noneID))
	require.NoError(t, err)
	require.Empty(t, gotNone.Concerns(),
		"phase without concerns must read back empty (NULL column parity)")
}
