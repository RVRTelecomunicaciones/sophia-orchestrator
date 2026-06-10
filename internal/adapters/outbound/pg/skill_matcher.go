package pg

import (
	"context"
	"sort"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
)

// riskOrder maps RiskLevel values to sortable integers (low < medium < high < critical).
var riskOrder = map[skill.RiskLevel]int{
	skill.RiskLow:      0,
	skill.RiskMedium:   1,
	skill.RiskHigh:     2,
	skill.RiskCritical: 3,
}

// PGSkillMatcher implements discipline.SkillMatcher backed by Postgres.
// It loads all active skills via the SkillRepo and applies in-memory filtering
// and sorting to produce the final match/skip result.
//
// In-memory filtering is appropriate for M1 because the skills table is small
// (<100 rows in V1 usage) and avoids complex JSONB query construction. M2+ can
// push scope/applies_when filters to SQL with GIN index pushdown.
//
//nolint:revive // PGSkillMatcher is the correct name per design.md (PG qualifier is intentional).
type PGSkillMatcher struct {
	repo *SkillRepo
}

// NewPGSkillMatcher constructs a PGSkillMatcher. The pool parameter is typed as
// *pgxpool.Pool per D-M2-13 fix S3; it is accepted for forward compatibility
// with M2+ SQL push-down queries but remains unused in M1 in-memory filtering.
func NewPGSkillMatcher(_ *pgxpool.Pool, repo *SkillRepo) *PGSkillMatcher {
	if repo == nil {
		panic("pg.PGSkillMatcher: nil repo")
	}
	return &PGSkillMatcher{repo: repo}
}

// SkillsForContext implements discipline.SkillMatcher.
//
// Algorithm (M1 — full in-memory filter after loading all skills):
//  1. Load ALL skills via List (no server-side status or phase pre-filter).
//  2. For each skill: check status, phase membership, scope, applies_when.
//  3. Skills failing any filter are appended to skipped with the first failing reason.
//  4. Matched skills are sorted by riskOrder asc → lastValidatedAt desc (NULLs
//     last) → id asc.
//
// Loading all skills (vs. a pre-filtered FindByPhase) enables complete observability:
// non-active and non-phase skills appear in the skipped list so callers can log
// the full evaluation trace. M2+ can push filters to SQL when row counts grow.
func (m *PGSkillMatcher) SkillsForContext(ctx context.Context, q discipline.SkillQuery) ([]*skill.Skill, []discipline.SkippedSkill, error) {
	// Load all skills — status and phase filtering is done in-process.
	candidates, err := m.repo.List(ctx)
	if err != nil {
		return nil, nil, wrapErr("PGSkillMatcher.SkillsForContext", err)
	}

	matched := make([]*skill.Skill, 0, len(candidates))
	skipped := make([]discipline.SkippedSkill, 0)

	for _, s := range candidates {
		// Status gate: only active skills are eligible (D-M1-6).
		if s.Status() != skill.StatusActive {
			skipped = append(skipped, discipline.SkippedSkill{
				SkillID: s.ID().String(),
				Reason:  discipline.SkipReasonStatusNotActive,
			})
			continue
		}

		// Phase membership: if q.Phase is set, the skill must declare that phase.
		if q.Phase != "" && !s.AppliesTo(phase.PhaseType(q.Phase)) {
			skipped = append(skipped, discipline.SkippedSkill{
				SkillID: s.ID().String(),
				Reason:  discipline.SkipReasonScopeMismatch,
			})
			continue
		}

		// Scope filter (project_id, repo_id).
		if reason, ok := scopeMatches(s.Scope(), q); !ok {
			skipped = append(skipped, discipline.SkippedSkill{
				SkillID: s.ID().String(),
				Reason:  reason,
			})
			continue
		}

		// AppliesWhen filter (feature_type, touched_paths, exclude_paths).
		if reason, ok := appliesWhenMatches(s.AppliesWhen(), q); !ok {
			skipped = append(skipped, discipline.SkippedSkill{
				SkillID: s.ID().String(),
				Reason:  reason,
			})
			continue
		}

		// MaxRiskLevel filter (D-M2-13 M1 warning W1): skip skills whose
		// risk_level exceeds the inclusive upper bound.
		if q.MaxRiskLevel != "" {
			if riskOrder[s.RiskLevel()] > riskOrder[q.MaxRiskLevel] {
				skipped = append(skipped, discipline.SkippedSkill{
					SkillID: s.ID().String(),
					Reason:  discipline.SkipReasonRiskExceeded,
				})
				continue
			}
		}

		matched = append(matched, s)
	}

	sortSkills(matched)
	return matched, skipped, nil
}

// applyRiskFilter applies the MaxRiskLevel filter to a slice of skills.
// It is extracted as a pure function for unit testing (no DB required).
// When q.MaxRiskLevel is empty the filter is a no-op (all skills pass).
func applyRiskFilter(candidates []*skill.Skill, q discipline.SkillQuery) ([]*skill.Skill, []discipline.SkippedSkill) {
	if q.MaxRiskLevel == "" {
		return candidates, nil
	}
	matched := make([]*skill.Skill, 0, len(candidates))
	var skipped []discipline.SkippedSkill
	for _, s := range candidates {
		if riskOrder[s.RiskLevel()] > riskOrder[q.MaxRiskLevel] {
			skipped = append(skipped, discipline.SkippedSkill{
				SkillID: s.ID().String(),
				Reason:  discipline.SkipReasonRiskExceeded,
			})
			continue
		}
		matched = append(matched, s)
	}
	return matched, skipped
}

// scopeMatches returns ("", true) when the skill's scope passes the query's
// project/repo dimensions, or (reason, false) when it does not.
// Phase membership is handled at the call-site via FindByPhase / AppliesTo.
func scopeMatches(sc skill.Scope, q discipline.SkillQuery) (string, bool) {
	// ProjectID filter: empty query dimension = no filter.
	if q.ProjectID != "" {
		if sc.ProjectID != "" && sc.ProjectID != "*" && sc.ProjectID != q.ProjectID {
			return discipline.SkipReasonScopeMismatch, false
		}
	}
	// RepoID filter: same semantics.
	if q.RepoID != "" {
		if sc.RepoID != "" && sc.RepoID != "*" && sc.RepoID != q.RepoID {
			return discipline.SkipReasonScopeMismatch, false
		}
	}
	return "", true
}

// appliesWhenMatches returns ("", true) when the skill's applies_when passes
// the query's feature_type + touched_paths dimensions, or (reason, false) otherwise.
func appliesWhenMatches(aw skill.AppliesWhen, q discipline.SkillQuery) (string, bool) {
	// FeatureType: if skill specifies a non-empty list, query must appear in it.
	if len(aw.FeatureType) > 0 && q.FeatureType != "" {
		found := false
		for _, ft := range aw.FeatureType {
			if ft == q.FeatureType {
				found = true
				break
			}
		}
		if !found {
			return discipline.SkipReasonAppliesWhenFailed, false
		}
	}

	// TouchedPaths + ExcludePaths: only evaluated when the query carries paths
	// AND the skill specifies at least one pattern.
	if len(q.TouchedPaths) > 0 {
		// ExcludePaths wins: if any query path matches an exclude glob, skip.
		if len(aw.ExcludePaths) > 0 {
			for _, qPath := range q.TouchedPaths {
				if anyGlobMatch(aw.ExcludePaths, qPath) {
					return discipline.SkipReasonAppliesWhenFailed, false
				}
			}
		}

		// TouchedPaths: if skill specifies patterns, at least one query path
		// must match at least one skill pattern.
		if len(aw.TouchedPaths) > 0 {
			matched := false
			for _, qPath := range q.TouchedPaths {
				if anyGlobMatch(aw.TouchedPaths, qPath) {
					matched = true
					break
				}
			}
			if !matched {
				return discipline.SkipReasonAppliesWhenFailed, false
			}
		}
	}

	return "", true
}

// anyGlobMatch returns true when path matches any of the glob patterns via
// doublestar.Match. Invalid patterns are treated as non-matching (not errors).
func anyGlobMatch(patterns []string, path string) bool {
	for _, pat := range patterns {
		ok, _ := doublestar.Match(pat, path)
		if ok {
			return true
		}
	}
	return false
}

// sortSkills sorts skills in-place:
//
//	primary:   risk_level asc  (low=0 < medium=1 < high=2 < critical=3)
//	secondary: last_validated_at desc (NULLs last)
//	tertiary:  usage_count desc, NULL/zero last (D-M2-13 M1 warning S1)
//	stable tiebreaker: id asc
func sortSkills(skills []*skill.Skill) {
	sort.SliceStable(skills, func(i, j int) bool {
		ri, rj := riskOrder[skills[i].RiskLevel()], riskOrder[skills[j].RiskLevel()]
		if ri != rj {
			return ri < rj
		}
		// last_validated_at desc (NULLs last).
		ti, tj := skills[i].LastValidatedAt(), skills[j].LastValidatedAt()
		if ti != nil && tj == nil {
			return true // i has validated date, j doesn't → i first
		}
		if ti == nil && tj != nil {
			return false // j has validated date → j first
		}
		if ti != nil && tj != nil {
			if !ti.Equal(*tj) {
				return ti.After(*tj) // desc
			}
		}
		// Tertiary: usage_count desc, zero sorts last (D-M2-13 S1 fix).
		ui, uj := skills[i].Metrics().UsageCount, skills[j].Metrics().UsageCount
		if ui != uj {
			// Higher usage first; zero is treated as NULL (sorts last).
			if ui == 0 {
				return false // i has zero → sort after j
			}
			if uj == 0 {
				return true // j has zero → sort after i
			}
			return ui > uj // desc
		}
		// Stable tiebreaker: id asc.
		return skills[i].ID().String() < skills[j].ID().String()
	})
}

// Verify PGSkillMatcher satisfies the SkillMatcher port at compile time.
var _ discipline.SkillMatcher = (*PGSkillMatcher)(nil)
