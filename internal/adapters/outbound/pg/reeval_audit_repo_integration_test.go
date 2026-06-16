//go:build integration

package pg_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/pg"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// TestReevalAuditRepo_SaveAndFindByID round-trips a run + items through Postgres.
func TestReevalAuditRepo_SaveAndFindByID(t *testing.T) {
	pool := setupSkillPG(t) // applies all migrations including 013
	ctx := context.Background()
	repo := pg.NewReevalAuditRepo(pool)

	created := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	run := outbound.ReevalRun{
		ID:        "01ARZ3NDEKTSV4RRFFQ69G5RN1",
		Mode:      "apply",
		CreatedAt: created,
		Items: []outbound.ReevalRunItem{
			{ID: "01ARZ3NDEKTSV4RRFFQ69G5IT1", SkillID: "01ARZ3NDEKTSV4RRFFQ69G5SK1", PriorStatus: "active", NewStatus: "deprecated"},
			{ID: "01ARZ3NDEKTSV4RRFFQ69G5IT2", SkillID: "01ARZ3NDEKTSV4RRFFQ69G5SK2", PriorStatus: "validated", NewStatus: "active"},
		},
	}
	require.NoError(t, repo.Save(ctx, run))

	got, err := repo.FindByID(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, run.ID, got.ID)
	assert.Equal(t, "apply", got.Mode)
	assert.WithinDuration(t, created, got.CreatedAt, time.Second)
	require.Len(t, got.Items, 2)
}

// TestReevalAuditRepo_FindLatest returns the most recently created run.
func TestReevalAuditRepo_FindLatest(t *testing.T) {
	pool := setupSkillPG(t)
	ctx := context.Background()
	repo := pg.NewReevalAuditRepo(pool)

	older := outbound.ReevalRun{
		ID: "01ARZ3NDEKTSV4RRFFQ69G5RN1", Mode: "apply",
		CreatedAt: time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC),
		Items: []outbound.ReevalRunItem{
			{ID: "01ARZ3NDEKTSV4RRFFQ69G5IT1", SkillID: "01ARZ3NDEKTSV4RRFFQ69G5SK1", PriorStatus: "active", NewStatus: "deprecated"},
		},
	}
	newer := outbound.ReevalRun{
		ID: "01ARZ3NDEKTSV4RRFFQ69G5RN2", Mode: "revert", RevertsRunID: older.ID,
		CreatedAt: time.Date(2026, 6, 16, 11, 0, 0, 0, time.UTC),
		Items: []outbound.ReevalRunItem{
			{ID: "01ARZ3NDEKTSV4RRFFQ69G5IT2", SkillID: "01ARZ3NDEKTSV4RRFFQ69G5SK1", PriorStatus: "deprecated", NewStatus: "active"},
		},
	}
	require.NoError(t, repo.Save(ctx, older))
	require.NoError(t, repo.Save(ctx, newer))

	got, err := repo.FindLatest(ctx)
	require.NoError(t, err)
	assert.Equal(t, newer.ID, got.ID)
	assert.Equal(t, "revert", got.Mode)
	assert.Equal(t, older.ID, got.RevertsRunID)
}

// TestReevalAuditRepo_FindByID_NotFound surfaces ErrNotFound for an unknown id.
func TestReevalAuditRepo_FindByID_NotFound(t *testing.T) {
	pool := setupSkillPG(t)
	ctx := context.Background()
	repo := pg.NewReevalAuditRepo(pool)

	_, err := repo.FindByID(ctx, "01ARZ3NDEKTSV4RRFFQ69G5XXX")
	assert.ErrorIs(t, err, outbound.ErrNotFound)
}
