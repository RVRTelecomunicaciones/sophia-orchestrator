package pg

import (
	"context"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
)

// SkillProvider wraps a SkillRepo and satisfies the discipline.SkillProvider port.
// It is the pg-layer adapter that the bootstrap package injects into phase.Service
// and apply.RunService when SOPHIA_SKILLS_ENABLED=true.
type SkillProvider struct {
	repo *SkillRepo
}

// NewSkillProvider constructs a SkillProvider backed by the given SkillRepo.
func NewSkillProvider(repo *SkillRepo) *SkillProvider {
	if repo == nil {
		panic("pg.SkillProvider: nil repo")
	}
	return &SkillProvider{repo: repo}
}

// SkillsForPhase delegates to SkillRepo.FindByPhase and returns the matching
// skills. Satisfies discipline.SkillProvider.
func (p *SkillProvider) SkillsForPhase(ctx context.Context, pt phase.PhaseType) ([]*skill.Skill, error) {
	return p.repo.FindByPhase(ctx, pt)
}

// Verify SkillProvider satisfies the discipline.SkillProvider port at compile time.
var _ discipline.SkillProvider = (*SkillProvider)(nil)
