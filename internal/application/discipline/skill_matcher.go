package discipline

import (
	"context"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
)

// SkillMatcher is the M1 application port for context-aware skill selection.
//
// SkillsForContext returns the Skills that match the provided SkillQuery along
// with a list of active skills that were explicitly skipped and the reason why.
// This richer return type allows callers to log skip decisions for observability
// without any logic leaking into the port.
//
// Contract:
//   - Returns an empty matched slice (not an error) when no skills satisfy the query.
//   - Returns an empty skipped slice when all active skills matched.
//   - Returns a non-nil error only on infrastructure failure (DB timeout, etc.).
//   - The matched slice is sorted by risk_level ascending (low → medium → high →
//     critical), then last_validated_at descending (most recently validated first,
//     NULLs last), then usage_count descending, then id ascending as stable
//     tiebreaker.
//
// See SkillProvider for the legacy phase-only wrapper.
type SkillMatcher interface {
	SkillsForContext(ctx context.Context, q SkillQuery) ([]*skill.Skill, []SkippedSkill, error)
}

// SkillQuery carries all context dimensions used by SkillMatcher to filter and
// rank matching Skills. All fields are optional: a zero-value query returns all
// active skills without any dimension-based filtering.
type SkillQuery struct {
	// Phase restricts results to skills whose phases array contains this value.
	// Empty string disables the phase filter (returns skills for any phase).
	Phase phase.PhaseType

	// ProjectID restricts results to skills whose scope.project_id equals this
	// value OR is the wildcard "*". Empty string disables the project filter.
	ProjectID string

	// RepoID restricts results to skills whose scope.repo_id equals this value
	// OR is the wildcard "*". Empty string disables the repo filter.
	RepoID string

	// StructuralContext is an opaque marker for M3 StructuralContext-aware
	// filtering. Always nil in M1 (opaque per D-M1-7 / D-M05-2); the adapter
	// MUST silently ignore this field until M3 wires it.
	StructuralContext *StructuralContextRef

	// FeatureType matches against skill.AppliesWhen.FeatureType inclusion list.
	// Empty string disables the feature_type filter.
	FeatureType string

	// TouchedPaths is the list of file paths changed in this context. Each path
	// is matched against skill.AppliesWhen.TouchedPaths globs via doublestar.
	// Nil or empty slice disables the touched_paths filter.
	TouchedPaths []string

	// MaxRiskLevel is the inclusive upper bound on RiskLevel. Only skills whose
	// risk_level is ≤ MaxRiskLevel are returned. Empty string disables this
	// filter (returns skills at any risk level).
	MaxRiskLevel skill.RiskLevel
}

// SkippedSkill records a skill that was evaluated but not selected by
// SkillsForContext, together with the machine-readable reason for the skip.
// Callers log these entries for observability; no business logic should branch
// on Reason values.
type SkippedSkill struct {
	// SkillID is the string form of the skipped skill's ID.
	SkillID string
	// Reason is one of the SkipReason* constants.
	Reason string
}

// SkipReasonScopeMismatch is returned when a skill's scope (project_id, repo_id,
// or phase membership) does not match the query dimensions.
const SkipReasonScopeMismatch = "scope_mismatch"

// SkipReasonAppliesWhenFailed is returned when a skill's applies_when predicate
// (feature_type, touched_paths, exclude_paths) does not match the query.
const SkipReasonAppliesWhenFailed = "applies_when_failed"

// SkipReasonStatusNotActive is returned when a skill's status is not "active".
// Only active skills are eligible for selection (D-M1-6).
const SkipReasonStatusNotActive = "status_not_active"

// SkipReasonRiskExceeded is returned when a skill's risk_level exceeds the
// SkillQuery.MaxRiskLevel inclusive bound (M2 D-M2-13 fix W1).
const SkipReasonRiskExceeded = "risk_exceeded"
