package pg

// Group F — M1 WARNINGS fixes (unit tests, white-box, no DB required)
// F.1: MaxRiskLevel=medium excludes high/critical skills with SkipReasonRiskExceeded
// F.2: MaxRiskLevel=0 (unset) is a no-op — all risk levels pass
// F.3: sort tertiary key: usage_count desc, NULL sorts last

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
)

// ── helpers ────────────────────────────────────────────────────────────────────

func mustParseMatcherSkillID(t *testing.T, s string) ids.SkillID {
	t.Helper()
	id, err := ids.ParseSkillID(s)
	require.NoError(t, err)
	return id
}

func makeSkillWithRisk(t *testing.T, rawID, name string, riskLevel skill.RiskLevel) *skill.Skill {
	t.Helper()
	s, err := skill.New(
		mustParseMatcherSkillID(t, rawID),
		name,
		[]phase.PhaseType{phase.PhaseApply},
		"test content",
		[]skill.Technique{skill.TechniqueInlineWhy},
		skill.LifecycleInput{
			Status:           skill.StatusActive,
			Version:          "v1",
			RiskLevel:        riskLevel,
			ActivationSource: skill.SourceLegacySeed,
			Scope:            skill.Scope{ProjectID: "*", RepoID: "*"},
		},
		time.Now(),
	)
	require.NoError(t, err)
	return s
}

func makeSkillWithMetrics(t *testing.T, rawID, name string, usageCount int) *skill.Skill {
	t.Helper()
	s, err := skill.New(
		mustParseMatcherSkillID(t, rawID),
		name,
		[]phase.PhaseType{phase.PhaseApply},
		"test content",
		[]skill.Technique{skill.TechniqueInlineWhy},
		skill.LifecycleInput{
			Status:           skill.StatusActive,
			Version:          "v1",
			RiskLevel:        skill.RiskLow,
			ActivationSource: skill.SourceLegacySeed,
			Scope:            skill.Scope{ProjectID: "*", RepoID: "*"},
			Metrics:          skill.Metrics{UsageCount: usageCount},
		},
		time.Now(),
	)
	require.NoError(t, err)
	return s
}

// ── F.1: MaxRiskLevel filter ──────────────────────────────────────────────────

// F.1a — SkillsForContext with MaxRiskLevel=medium excludes high+critical.
func TestPGSkillMatcher_MaxRiskLevel_ExcludesHighCritical(t *testing.T) {
	low := makeSkillWithRisk(t, "01ARZ3NDEKTSV4RRFFQ69G5MU1", "low-skill", skill.RiskLow)
	medium := makeSkillWithRisk(t, "01ARZ3NDEKTSV4RRFFQ69G5MU2", "medium-skill", skill.RiskMedium)
	high := makeSkillWithRisk(t, "01ARZ3NDEKTSV4RRFFQ69G5MU3", "high-skill", skill.RiskHigh)
	critical := makeSkillWithRisk(t, "01ARZ3NDEKTSV4RRFFQ69G5MU4", "critical-skill", skill.RiskCritical)

	allSkills := []*skill.Skill{low, medium, high, critical}

	q := discipline.SkillQuery{MaxRiskLevel: skill.RiskMedium}
	matched, skipped := applyRiskFilter(allSkills, q)

	// Only low+medium pass.
	require.Len(t, matched, 2)
	assert.Equal(t, "low-skill", matched[0].Name())
	assert.Equal(t, "medium-skill", matched[1].Name())

	// High+critical must appear in skipped with SkipReasonRiskExceeded.
	require.Len(t, skipped, 2)
	skippedIDs := make(map[string]string)
	for _, sk := range skipped {
		skippedIDs[sk.SkillID] = sk.Reason
	}
	assert.Equal(t, discipline.SkipReasonRiskExceeded, skippedIDs[high.ID().String()])
	assert.Equal(t, discipline.SkipReasonRiskExceeded, skippedIDs[critical.ID().String()])
}

// F.2 — MaxRiskLevel=0 (unset) is a no-op: all risk levels pass.
func TestPGSkillMatcher_MaxRiskLevel_ZeroIsNoop(t *testing.T) {
	low := makeSkillWithRisk(t, "01ARZ3NDEKTSV4RRFFQ69G5MU5", "low", skill.RiskLow)
	medium := makeSkillWithRisk(t, "01ARZ3NDEKTSV4RRFFQ69G5MU6", "medium", skill.RiskMedium)
	high := makeSkillWithRisk(t, "01ARZ3NDEKTSV4RRFFQ69G5MU7", "high", skill.RiskHigh)
	critical := makeSkillWithRisk(t, "01ARZ3NDEKTSV4RRFFQ69G5MU8", "critical", skill.RiskCritical)

	allSkills := []*skill.Skill{low, medium, high, critical}

	// MaxRiskLevel="" (zero value) disables the filter.
	q := discipline.SkillQuery{MaxRiskLevel: ""}
	matched, skipped := applyRiskFilter(allSkills, q)

	assert.Len(t, matched, 4, "all skills must pass when MaxRiskLevel is unset")
	assert.Empty(t, skipped, "no skills should be skipped when MaxRiskLevel is unset")
}

// ── F.3: sort tertiary by usage_count desc ─────────────────────────────────────

// F.3a — Two skills with same primary+secondary sort: higher usage_count sorts first.
func TestSortSkills_UsageCountDesc_HigherFirst(t *testing.T) {
	// Both are RiskLow + same (nil) last_validated_at → tertiary key decides.
	sk10 := makeSkillWithMetrics(t, "01ARZ3NDEKTSV4RRFFQ69G5MS1", "high-usage", 10)
	sk2 := makeSkillWithMetrics(t, "01ARZ3NDEKTSV4RRFFQ69G5MS2", "low-usage", 2)

	skills := []*skill.Skill{sk2, sk10} // intentionally reversed
	sortSkills(skills)

	require.Len(t, skills, 2)
	assert.Equal(t, "high-usage", skills[0].Name(), "skill with usage_count=10 must sort before usage_count=2")
	assert.Equal(t, "low-usage", skills[1].Name())
}

// F.3b — Null usage_count (zero value) sorts last.
func TestSortSkills_NullUsageCount_SortsLast(t *testing.T) {
	skWith := makeSkillWithMetrics(t, "01ARZ3NDEKTSV4RRFFQ69G5MS3", "with-usage", 5)
	skNull := makeSkillWithMetrics(t, "01ARZ3NDEKTSV4RRFFQ69G5MS4", "null-usage", 0) // zero = "null" in M1

	skills := []*skill.Skill{skNull, skWith}
	sortSkills(skills)

	require.Len(t, skills, 2)
	assert.Equal(t, "with-usage", skills[0].Name(), "skill with usage_count=5 must sort before usage_count=0")
	assert.Equal(t, "null-usage", skills[1].Name())
}
