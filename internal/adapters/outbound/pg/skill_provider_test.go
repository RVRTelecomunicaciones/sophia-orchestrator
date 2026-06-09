//go:build integration

package pg_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/pg"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
)

var providerID1 = "01ARZ3NDEKTSV4RRFFQ69G5SP1"

// TestSkillProvider_SkillsForPhase_MatchesMatcher verifies that the deprecated
// SkillsForPhase wrapper returns the same []*skill.Skill slice as calling
// SkillsForContext(SkillQuery{Phase: pt}) would return.
func TestSkillProvider_SkillsForPhase_MatchesMatcher(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	matcher := pg.NewPGSkillMatcher(pool, repo)
	provider := pg.NewSkillProvider(matcher)
	ctx := context.Background()

	// Seed one active apply skill.
	id, err := ids.ParseSkillID(providerID1)
	require.NoError(t, err)
	s, err := skill.NewLegacy(id, "provider-apply-skill", []phase.PhaseType{phase.PhaseApply},
		"test content", []skill.Technique{skill.TechniqueInlineWhy}, matcherNow)
	require.NoError(t, err)
	require.NoError(t, repo.Upsert(ctx, s))

	// SkillsForPhase result.
	fromProvider, err := provider.SkillsForPhase(ctx, phase.PhaseApply)
	require.NoError(t, err)

	// SkillsForContext result for the same phase.
	fromMatcher, _, err := matcher.SkillsForContext(ctx, discipline.SkillQuery{Phase: phase.PhaseApply})
	require.NoError(t, err)

	// Both must return the same set of skill IDs.
	require.Len(t, fromProvider, len(fromMatcher),
		"SkillsForPhase and SkillsForContext must return the same number of skills")

	fromProviderIDs := make(map[string]struct{}, len(fromProvider))
	for _, sk := range fromProvider {
		fromProviderIDs[sk.ID().String()] = struct{}{}
	}
	for _, sk := range fromMatcher {
		require.Contains(t, fromProviderIDs, sk.ID().String(),
			"SkillsForPhase must include all skills from SkillsForContext")
	}
}

// TestSkillProvider_SkillsForPhase_PropagatesError verifies that an error from
// the underlying SkillsForContext propagates unchanged through SkillsForPhase.
func TestSkillProvider_SkillsForPhase_PropagatesError(t *testing.T) {
	// Use a fake matcher that always errors.
	errMatcher := &errorMatcher{err: errors.New("db: connection lost")}
	provider := pg.NewSkillProvider(errMatcher)
	ctx := context.Background()

	_, err := provider.SkillsForPhase(ctx, phase.PhaseApply)
	require.Error(t, err)
	require.Contains(t, err.Error(), "db: connection lost",
		"SkillsForPhase must propagate the underlying error unchanged")
}

// ── Test doubles ──────────────────────────────────────────────────────────────

// errorMatcher is a discipline.SkillMatcher that always returns an error.
type errorMatcher struct{ err error }

func (e *errorMatcher) SkillsForContext(_ context.Context, _ discipline.SkillQuery) ([]*skill.Skill, []discipline.SkippedSkill, error) {
	return nil, nil, e.err
}

var _ discipline.SkillMatcher = (*errorMatcher)(nil)
