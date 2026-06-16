package skill_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	skillapp "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/skill"
	domainskill "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skillusage"
)

// hydrateSkill builds a minimal active Skill aggregate for provider tests. Only
// ID, Status and Metrics.AvgRetryReduction are read by the provider under test.
func hydrateSkill(t *testing.T, rawID string, status domainskill.Status, avgRetry float64) *domainskill.Skill {
	t.Helper()
	return domainskill.Hydrate(
		mustSkillID(t, rawID),
		"test-skill",
		nil, // phases
		"content",
		nil, // techniques
		status,
		"v1",
		domainskill.Scope{},
		domainskill.AppliesWhen{},
		domainskill.RiskLow,
		domainskill.SourceManual,
		domainskill.Metrics{AvgRetryReduction: avgRetry},
		nil, nil, // lastUsedAt, lastValidatedAt
		testNow, testNow,
	)
}

// TestRepoEvidenceProvider_BuildsPerSkillEvidence verifies the concrete provider
// maps each skill to its current status/metric and the max per-change
// apply_attempts basis across the changes it was used in (mirrors ME max()).
func TestRepoEvidenceProvider_BuildsPerSkillEvidence(t *testing.T) {
	skillRepo := newFakeSkillRepo()
	sk := hydrateSkill(t, testSkillID1, domainskill.StatusActive, 0.333)
	skillRepo.byID[testSkillID1] = sk

	usageRepo := newFakeUsageRepo()
	// skill1 was used in two changes: testChangeID (sum 4) and a second (sum 2).
	const changeID2 = "01ARZ3NDEKTSV4RRFFQ69G5UC2"
	usageRepo.bySkill[testSkillID1] = []*skillusage.SkillUsage{
		skillusage.New(mustUsageID(t, testUsageID1), mustChangeID(t, testChangeID), "apply", mustSkillID(t, testSkillID1), "v1", testNow),
		skillusage.New(mustUsageID(t, testUsageID2), mustChangeID(t, changeID2), "apply", mustSkillID(t, testSkillID1), "v1", testNow),
	}
	usageRepo.attemptsSum[testChangeID] = 4
	usageRepo.attemptsSum[changeID2] = 2

	provider := skillapp.NewRepoEvidenceProvider(skillRepo, usageRepo)
	rows, err := provider.Rows(context.Background())
	require.NoError(t, err)
	require.Len(t, rows, 1)

	assert.Equal(t, testSkillID1, rows[0].SkillID)
	assert.Equal(t, domainskill.StatusActive, rows[0].CurrentStatus)
	assert.Equal(t, 0.333, rows[0].CurrentMetric)
	assert.Equal(t, 4, rows[0].ApplyAttempts, "max per-change apply_attempts across evidence changes")
}
