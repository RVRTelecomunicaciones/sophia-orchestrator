//go:build integration

package pg_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/pg"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
)

// matcherIDs — stable ULID-format IDs for matcher integration tests.
var (
	matcherID1 = "01ARZ3NDEKTSV4RRFFQ69G5SM1"
	matcherID2 = "01ARZ3NDEKTSV4RRFFQ69G5SM2"
	matcherID3 = "01ARZ3NDEKTSV4RRFFQ69G5SM3"
	matcherID4 = "01ARZ3NDEKTSV4RRFFQ69G5SM4"
	matcherNow = time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
)

func mustMatcherSkillID(t *testing.T, raw string) ids.SkillID {
	t.Helper()
	id, err := ids.ParseSkillID(raw)
	require.NoError(t, err)
	return id
}

// newActiveSkill creates an active skill with the given phases and lifecycle fields.
func newActiveSkill(t *testing.T, rawID, name string, phases []phase.PhaseType, lc skill.LifecycleInput) *skill.Skill {
	t.Helper()
	s, err := skill.New(
		mustMatcherSkillID(t, rawID),
		name,
		phases,
		"matcher test content",
		[]skill.Technique{skill.TechniqueInlineWhy},
		lc,
		matcherNow,
	)
	require.NoError(t, err)
	return s
}

// newLegacyMatcherSkill creates a legacy-seeded active skill (V4.1 §7 payload).
func newLegacyMatcherSkill(t *testing.T, rawID, name string, phases []phase.PhaseType) *skill.Skill {
	t.Helper()
	s, err := skill.NewLegacy(
		mustMatcherSkillID(t, rawID),
		name,
		phases,
		"matcher test content",
		[]skill.Technique{skill.TechniqueInlineWhy},
		matcherNow,
	)
	require.NoError(t, err)
	return s
}

// setupMatcherPG returns a pool + seeded repo + matcher for integration tests.
func setupMatcherPG(t *testing.T) (*pg.SkillRepo, *pg.PGSkillMatcher) {
	t.Helper()
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	matcher := pg.NewPGSkillMatcher(pool, repo)
	return repo, matcher
}

// ── H.1: status filter ────────────────────────────────────────────────────────

// TestPGSkillMatcher_ReturnsOnlyActiveSkills verifies that SkillsForContext
// returns only active skills; a candidate skill appears in the skipped list
// with reason status_not_active.
func TestPGSkillMatcher_ReturnsOnlyActiveSkills(t *testing.T) {
	repo, matcher := setupMatcherPG(t)
	ctx := context.Background()

	active := newLegacyMatcherSkill(t, matcherID1, "active-skill", []phase.PhaseType{phase.PhaseApply})
	require.NoError(t, repo.Upsert(ctx, active))

	candidate := newActiveSkill(t, matcherID2, "candidate-skill", []phase.PhaseType{phase.PhaseApply},
		skill.LifecycleInput{Status: skill.StatusCandidate, Version: "v1"},
	)
	require.NoError(t, repo.Upsert(ctx, candidate))

	matched, skipped, err := matcher.SkillsForContext(ctx, discipline.SkillQuery{})
	require.NoError(t, err)

	// Only the active skill must be in matched.
	require.Len(t, matched, 1)
	assert.Equal(t, "active-skill", matched[0].Name())

	// The candidate must appear in skipped with status_not_active.
	require.Len(t, skipped, 1)
	assert.Equal(t, discipline.SkipReasonStatusNotActive, skipped[0].Reason)
	assert.Equal(t, candidate.ID().String(), skipped[0].SkillID)
}

// ── H.2: scope wildcard ───────────────────────────────────────────────────────

// TestPGSkillMatcher_ScopeWildcard_MatchesAnyProjectID verifies that a skill
// with scope.project_id="*" is matched for any non-empty ProjectID in the query.
func TestPGSkillMatcher_ScopeWildcard_MatchesAnyProjectID(t *testing.T) {
	repo, matcher := setupMatcherPG(t)
	ctx := context.Background()

	// NewLegacy creates scope with project_id="*" (V4.1 §7 wildcard).
	s := newLegacyMatcherSkill(t, matcherID1, "wildcard-skill", []phase.PhaseType{phase.PhaseApply})
	require.NoError(t, repo.Upsert(ctx, s))

	matched, _, err := matcher.SkillsForContext(ctx, discipline.SkillQuery{
		ProjectID: "any-project-id-here",
	})
	require.NoError(t, err)
	require.Len(t, matched, 1, "wildcard project_id must match any query ProjectID")
}

// ── H.3: scope mismatch ───────────────────────────────────────────────────────

// TestPGSkillMatcher_ScopeMismatch_SkippedWithReason verifies that a skill
// with a specific project_id that does not match the query is skipped with
// scope_mismatch reason.
func TestPGSkillMatcher_ScopeMismatch_SkippedWithReason(t *testing.T) {
	repo, matcher := setupMatcherPG(t)
	ctx := context.Background()

	// Skill with exact project_id "proj-A".
	s := newActiveSkill(t, matcherID1, "scoped-skill", []phase.PhaseType{phase.PhaseApply},
		skill.LifecycleInput{
			Status:  skill.StatusActive,
			Version: "v1",
			Scope: skill.Scope{
				ProjectID: "proj-A",
				RepoID:    "*",
				Phases:    []string{string(phase.PhaseApply)},
			},
		},
	)
	require.NoError(t, repo.Upsert(ctx, s))

	// Query with different project ID.
	matched, skipped, err := matcher.SkillsForContext(ctx, discipline.SkillQuery{
		ProjectID: "proj-B",
	})
	require.NoError(t, err)
	assert.Empty(t, matched)
	require.Len(t, skipped, 1)
	assert.Equal(t, discipline.SkipReasonScopeMismatch, skipped[0].Reason)
}

// ── H.4: phase filter ─────────────────────────────────────────────────────────

// TestPGSkillMatcher_PhaseFilter_SkipsWrongPhase verifies that a skill with
// phases=["apply","verify"] is skipped when query.Phase="spec".
func TestPGSkillMatcher_PhaseFilter_SkipsWrongPhase(t *testing.T) {
	repo, matcher := setupMatcherPG(t)
	ctx := context.Background()

	s := newLegacyMatcherSkill(t, matcherID1, "apply-verify-skill",
		[]phase.PhaseType{phase.PhaseApply, phase.PhaseVerify})
	require.NoError(t, repo.Upsert(ctx, s))

	matched, skipped, err := matcher.SkillsForContext(ctx, discipline.SkillQuery{
		Phase: phase.PhaseSpec,
	})
	require.NoError(t, err)
	assert.Empty(t, matched)
	require.Len(t, skipped, 1)
	assert.Equal(t, discipline.SkipReasonScopeMismatch, skipped[0].Reason)
}

// ── H.5: feature_type mismatch ────────────────────────────────────────────────

// TestPGSkillMatcher_FeatureTypeMismatch_SkippedWithReason verifies that a
// skill with applies_when.feature_type set is skipped when query.FeatureType
// does not appear in that list.
func TestPGSkillMatcher_FeatureTypeMismatch_SkippedWithReason(t *testing.T) {
	repo, matcher := setupMatcherPG(t)
	ctx := context.Background()

	s := newActiveSkill(t, matcherID1, "bugfix-skill", []phase.PhaseType{phase.PhaseApply},
		skill.LifecycleInput{
			Status:  skill.StatusActive,
			Version: "v1",
			AppliesWhen: skill.AppliesWhen{
				FeatureType: []string{"bugfix"},
			},
		},
	)
	require.NoError(t, repo.Upsert(ctx, s))

	matched, skipped, err := matcher.SkillsForContext(ctx, discipline.SkillQuery{
		FeatureType: "refactor",
	})
	require.NoError(t, err)
	assert.Empty(t, matched)
	require.Len(t, skipped, 1)
	assert.Equal(t, discipline.SkipReasonAppliesWhenFailed, skipped[0].Reason)
}

// ── H.7: touched_paths glob ───────────────────────────────────────────────────

// TestPGSkillMatcher_TouchedPaths_GlobMatch verifies that applies_when.touched_paths
// glob patterns are matched against query.TouchedPaths via doublestar.
func TestPGSkillMatcher_TouchedPaths_GlobMatch(t *testing.T) {
	repo, matcher := setupMatcherPG(t)
	ctx := context.Background()

	s := newActiveSkill(t, matcherID1, "domain-skill", []phase.PhaseType{phase.PhaseApply},
		skill.LifecycleInput{
			Status:  skill.StatusActive,
			Version: "v1",
			AppliesWhen: skill.AppliesWhen{
				TouchedPaths: []string{"internal/domain/**"},
			},
		},
	)
	require.NoError(t, repo.Upsert(ctx, s))

	matched, _, err := matcher.SkillsForContext(ctx, discipline.SkillQuery{
		TouchedPaths: []string{"internal/domain/skill/skill.go"},
	})
	require.NoError(t, err)
	require.Len(t, matched, 1, `"internal/domain/**" must match "internal/domain/skill/skill.go"`)
}

// ── H.8: exclude_paths wins ───────────────────────────────────────────────────

// TestPGSkillMatcher_ExcludePaths_WinsOverInclude verifies that a skill with
// applies_when.exclude_paths matching the query is skipped even when
// touched_paths would otherwise match.
func TestPGSkillMatcher_ExcludePaths_WinsOverInclude(t *testing.T) {
	repo, matcher := setupMatcherPG(t)
	ctx := context.Background()

	s := newActiveSkill(t, matcherID1, "no-vendor-skill", []phase.PhaseType{phase.PhaseApply},
		skill.LifecycleInput{
			Status:  skill.StatusActive,
			Version: "v1",
			AppliesWhen: skill.AppliesWhen{
				TouchedPaths:  []string{"**"},
				ExcludePaths: []string{"vendor/**"},
			},
		},
	)
	require.NoError(t, repo.Upsert(ctx, s))

	matched, skipped, err := matcher.SkillsForContext(ctx, discipline.SkillQuery{
		TouchedPaths: []string{"vendor/lib/foo.go"},
	})
	require.NoError(t, err)
	assert.Empty(t, matched)
	require.Len(t, skipped, 1)
	assert.Equal(t, discipline.SkipReasonAppliesWhenFailed, skipped[0].Reason)
}

// ── H.9: sort order ───────────────────────────────────────────────────────────

// TestPGSkillMatcher_SortOrder_RiskLevelAscending verifies that matched skills
// are sorted by risk_level ascending: low → medium → high.
func TestPGSkillMatcher_SortOrder_RiskLevelAscending(t *testing.T) {
	repo, matcher := setupMatcherPG(t)
	ctx := context.Background()

	high := newActiveSkill(t, matcherID1, "high-risk-skill", []phase.PhaseType{phase.PhaseApply},
		skill.LifecycleInput{Status: skill.StatusActive, Version: "v1", RiskLevel: skill.RiskHigh},
	)
	low := newActiveSkill(t, matcherID2, "low-risk-skill", []phase.PhaseType{phase.PhaseApply},
		skill.LifecycleInput{Status: skill.StatusActive, Version: "v1", RiskLevel: skill.RiskLow},
	)
	medium := newActiveSkill(t, matcherID3, "medium-risk-skill", []phase.PhaseType{phase.PhaseApply},
		skill.LifecycleInput{Status: skill.StatusActive, Version: "v1", RiskLevel: skill.RiskMedium},
	)
	require.NoError(t, repo.Upsert(ctx, high))
	require.NoError(t, repo.Upsert(ctx, low))
	require.NoError(t, repo.Upsert(ctx, medium))

	matched, _, err := matcher.SkillsForContext(ctx, discipline.SkillQuery{})
	require.NoError(t, err)
	require.Len(t, matched, 3)

	// Verify ascending risk order.
	assert.Equal(t, skill.RiskLow, matched[0].RiskLevel(), "first must be risk=low")
	assert.Equal(t, skill.RiskMedium, matched[1].RiskLevel(), "second must be risk=medium")
	assert.Equal(t, skill.RiskHigh, matched[2].RiskLevel(), "third must be risk=high")
}

// ── H.10: full integration query against seeded skills ───────────────────────

// TestPGSkillMatcher_FullQuery_SeededSkills verifies that querying with
// Phase="apply" against the seeded legacy skills returns ≥1 match and
// total matched + skipped equals total active skills.
func TestPGSkillMatcher_FullQuery_SeededSkills(t *testing.T) {
	repo, matcher := setupMatcherPG(t)
	ctx := context.Background()

	// Seed all 9 canonical skills via NewLegacy.
	for i, def := range []struct {
		rawID  string
		name   string
		phases []phase.PhaseType
	}{
		{"01JXSKLLP000000000000000IN", "init-bootstrap-context", []phase.PhaseType{phase.PhaseInit}},
		{"01JXSKLLP000000000000EXPL0", "explore-investigate", []phase.PhaseType{phase.PhaseExplore}},
		{"01JXSKLLP000000000000PROP0", "proposal-draft-options", []phase.PhaseType{phase.PhaseProposal}},
		{"01JXSKLLP000000000000SPEC0", "spec-write-requirements", []phase.PhaseType{phase.PhaseSpec}},
		{"01JXSKLLP000000000000DSGN0", "design-architect-system", []phase.PhaseType{phase.PhaseDesign}},
		{"01JXSKLLP000000000000TASK0", "tasks-decompose-work", []phase.PhaseType{phase.PhaseTasks}},
		{"01JXSKLLP000000000000APLY0", "apply-implement-safely", []phase.PhaseType{phase.PhaseApply}},
		{"01JXSKLLP000000000000VRFY0", "verify-chain-validation", []phase.PhaseType{phase.PhaseVerify}},
		{"01JXSKLLP000000000000ARCH0", "archive-finalize-deltas", []phase.PhaseType{phase.PhaseArchive}},
	} {
		id, err := ids.ParseSkillID(def.rawID)
		require.NoError(t, err, "seed ID %d must parse", i)
		s, err := skill.NewLegacy(id, def.name, def.phases, "test content", []skill.Technique{skill.TechniqueInlineWhy}, matcherNow)
		require.NoError(t, err)
		require.NoError(t, repo.Upsert(ctx, s))
	}

	matched, skipped, err := matcher.SkillsForContext(ctx, discipline.SkillQuery{
		Phase:     phase.PhaseApply,
		ProjectID: "*",
		RepoID:    "*",
	})
	require.NoError(t, err)

	// The apply skill must be in matched.
	require.GreaterOrEqual(t, len(matched), 1, "apply query must return ≥1 match")

	// All 9 seeds are active; those not matching apply phase appear in skipped.
	total := len(matched) + len(skipped)
	assert.Equal(t, 9, total, "matched + skipped must equal total active skills (9)")
}
