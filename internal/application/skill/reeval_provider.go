package skill

import (
	"context"
	"fmt"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// RepoEvidenceProvider builds re-evaluation evidence from the persisted skills and
// skill_usage tables. For each skill it reports the current status and stored
// avg_retry_reduction, and derives the apply_attempts basis as the maximum
// per-change SUM(tasks.attempts) across the changes the skill was used in. The max
// mirrors the ME computeDeltas consumer, which takes max(ApplyAttempts) per skill.
type RepoEvidenceProvider struct {
	skillRepo outbound.SkillRepository
	usageRepo outbound.SkillUsageRepository
}

// NewRepoEvidenceProvider constructs a RepoEvidenceProvider.
func NewRepoEvidenceProvider(skillRepo outbound.SkillRepository, usageRepo outbound.SkillUsageRepository) *RepoEvidenceProvider {
	return &RepoEvidenceProvider{skillRepo: skillRepo, usageRepo: usageRepo}
}

// Rows returns one Evidence per persisted skill. Skills with no usage history carry
// an apply_attempts basis of 0 (recomputes to the maximum metric, never demoted).
func (p *RepoEvidenceProvider) Rows(ctx context.Context) ([]Evidence, error) {
	skills, err := p.skillRepo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("skill.RepoEvidenceProvider: list skills: %w", err)
	}

	out := make([]Evidence, 0, len(skills))
	for _, sk := range skills {
		usages, err := p.usageRepo.FindBySkill(ctx, sk.ID())
		if err != nil {
			return nil, fmt.Errorf("skill.RepoEvidenceProvider: usage for %s: %w", sk.ID(), err)
		}

		maxAttempts := 0
		seen := map[string]bool{}
		for _, u := range usages {
			cid := u.ChangeID()
			if seen[cid.String()] {
				continue
			}
			seen[cid.String()] = true

			sum, err := p.usageRepo.SumApplyAttemptsByChange(ctx, cid)
			if err != nil {
				return nil, fmt.Errorf("skill.RepoEvidenceProvider: sum attempts for %s: %w", cid, err)
			}
			if sum > maxAttempts {
				maxAttempts = sum
			}
		}

		out = append(out, Evidence{
			SkillID:       sk.ID().String(),
			CurrentStatus: sk.Status(),
			CurrentMetric: sk.Metrics().AvgRetryReduction,
			ApplyAttempts: maxAttempts,
		})
	}
	return out, nil
}

// Verify RepoEvidenceProvider satisfies the EvidenceProvider contract.
var _ EvidenceProvider = (*RepoEvidenceProvider)(nil)

// Verify *Service satisfies the patcher, live-status reader, and metrics patcher
// contracts so Reevaluator() wires an idempotent, drift-correct revert path.
var (
	_ StatusPatcher  = (*Service)(nil)
	_ StatusReader   = (*Service)(nil)
	_ MetricsPatcher = (*Service)(nil)
)

// Reevaluator builds a Reevaluator wired to this Service's repositories. Evidence
// is read from the skills + skill_usage tables and transitions are applied through
// the Service's own PatchStatus, so the 6-enum allowedTransitions guard is reused.
//
// When the audit repository and ID generator are wired (WithReevalAudit), the
// returned Reevaluator records a revertible prior-state snapshot on apply and
// supports Revert/RevertLast (D1). Otherwise it is the dry-run/apply-only
// reevaluator with no revert surface.
func (s *Service) Reevaluator() *Reevaluator {
	provider := NewRepoEvidenceProvider(s.skillRepo, s.skillUsageRepo)
	if s.reevalAudit != nil && s.idGen != nil {
		return NewReevaluatorWithAudit(provider, s, s.reevalAudit, s.clock, s.idGen, s)
	}
	return NewReevaluator(provider, s)
}
