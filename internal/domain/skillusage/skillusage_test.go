package skillusage_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skillusage"
)

// B.1 RED — SkillUsage entity validates outcome enum.

func TestNewSkillUsage_ValidOutcomes(t *testing.T) {
	validOutcomes := []skillusage.Outcome{
		skillusage.OutcomePending,
		skillusage.OutcomeSuccess,
		skillusage.OutcomeFailure,
		skillusage.OutcomeBlocked,
	}

	su := mustNewUsage(t)
	for _, o := range validOutcomes {
		err := su.SetOutcome(o)
		assert.NoError(t, err, "outcome %q must be valid", o)
	}
}

func TestNewSkillUsage_InvalidOutcomeRejected(t *testing.T) {
	su := mustNewUsage(t)
	err := su.SetOutcome(skillusage.Outcome("unknown"))
	require.Error(t, err)
	assert.ErrorIs(t, err, skillusage.ErrInvalidOutcome)
}

func TestNewSkillUsage_DefaultOutcomeIsPending(t *testing.T) {
	su := mustNewUsage(t)
	assert.Equal(t, skillusage.OutcomePending, su.Outcome())
}

func TestNewSkillUsage_Fields(t *testing.T) {
	usageID := mustSkillUsageID(t, "01ARZ3NDEKTSV4RRFFQ69G5SU1")
	changeID := mustChangeID(t, "01ARZ3NDEKTSV4RRFFQ69G5CA1")
	skillID := mustSkillID(t, "01ARZ3NDEKTSV4RRFFQ69G5SK1")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	su := skillusage.New(usageID, changeID, "apply", skillID, "v1", now)

	assert.Equal(t, usageID, su.ID())
	assert.Equal(t, changeID, su.ChangeID())
	assert.Equal(t, "apply", su.PhaseType())
	assert.Equal(t, skillID, su.SkillID())
	assert.Equal(t, "v1", su.SkillVersion())
	assert.Equal(t, now, su.InjectedAt())
	assert.Equal(t, skillusage.OutcomePending, su.Outcome())
}

// ── helpers ─────────────────────────────────────────────────────────────────

func mustNewUsage(t *testing.T) *skillusage.SkillUsage {
	t.Helper()
	return skillusage.New(
		mustSkillUsageID(t, "01ARZ3NDEKTSV4RRFFQ69G5SU1"),
		mustChangeID(t, "01ARZ3NDEKTSV4RRFFQ69G5CA1"),
		"apply",
		mustSkillID(t, "01ARZ3NDEKTSV4RRFFQ69G5SK1"),
		"v1",
		time.Now(),
	)
}

func mustSkillUsageID(t *testing.T, raw string) ids.SkillUsageID {
	t.Helper()
	id, err := ids.ParseSkillUsageID(raw)
	require.NoError(t, err)
	return id
}

func mustChangeID(t *testing.T, raw string) ids.ChangeID {
	t.Helper()
	id, err := ids.ParseChangeID(raw)
	require.NoError(t, err)
	return id
}

func mustSkillID(t *testing.T, raw string) ids.SkillID {
	t.Helper()
	id, err := ids.ParseSkillID(raw)
	require.NoError(t, err)
	return id
}
