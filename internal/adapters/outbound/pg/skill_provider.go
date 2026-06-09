package pg

import (
	"context"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
)

// SkillProvider wraps a SkillMatcher and satisfies the discipline.SkillProvider
// port via the deprecated SkillsForPhase method.
//
// The underlying matcher is discipline.SkillMatcher so the provider composes
// with PGSkillMatcher (and any future in-memory or remote matcher) without
// depending on the concrete SkillRepo type.
type SkillProvider struct {
	matcher discipline.SkillMatcher
}

// NewSkillProvider constructs a SkillProvider backed by the given SkillMatcher.
// The matcher is typically a *PGSkillMatcher wired in bootstrap.Wire().
func NewSkillProvider(matcher discipline.SkillMatcher) *SkillProvider {
	if matcher == nil {
		panic("pg.SkillProvider: nil matcher")
	}
	return &SkillProvider{matcher: matcher}
}

// SkillsForPhase is a thin deprecated wrapper around SkillMatcher.SkillsForContext.
// It translates the phase-only query into a SkillQuery and discards the skipped
// list, returning only the matched skills.
//
// Deprecated: Use SkillMatcher.SkillsForContext(SkillQuery{Phase: phase}) instead.
// SkillsForPhase will be removed in M3.
func (p *SkillProvider) SkillsForPhase(ctx context.Context, pt phase.PhaseType) ([]*skill.Skill, error) {
	skills, _, err := p.matcher.SkillsForContext(ctx, discipline.SkillQuery{Phase: pt})
	return skills, err
}

// Verify SkillProvider satisfies the discipline.SkillProvider port at compile time.
var _ discipline.SkillProvider = (*SkillProvider)(nil)
