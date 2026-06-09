package discipline_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
)

// TestSkillQuery_ZeroValueCompiles verifies the zero-value SkillQuery is
// constructable and passes go vet without any required fields missing.
func TestSkillQuery_ZeroValueCompiles(t *testing.T) {
	var q discipline.SkillQuery
	// Zero-value must be a valid, usable query (no required fields).
	assert.Equal(t, phase.PhaseType(""), q.Phase)
	assert.Empty(t, q.ProjectID)
	assert.Empty(t, q.RepoID)
	assert.Nil(t, q.StructuralContext)
	assert.Empty(t, q.FeatureType)
	assert.Nil(t, q.TouchedPaths)
	assert.Equal(t, skill.RiskLevel(""), q.MaxRiskLevel)
}

// TestSkillQuery_ExplicitFields verifies all 7 SkillQuery fields can be set.
func TestSkillQuery_ExplicitFields(t *testing.T) {
	ref := &discipline.StructuralContextRef{}
	q := discipline.SkillQuery{
		Phase:            phase.PhaseApply,
		ProjectID:        "proj-123",
		RepoID:           "repo-456",
		StructuralContext: ref,
		FeatureType:      "bugfix",
		TouchedPaths:     []string{"internal/domain/**", "internal/adapters/**"},
		MaxRiskLevel:     skill.RiskHigh,
	}

	require.Equal(t, phase.PhaseApply, q.Phase)
	require.Equal(t, "proj-123", q.ProjectID)
	require.Equal(t, "repo-456", q.RepoID)
	require.Equal(t, ref, q.StructuralContext)
	require.Equal(t, "bugfix", q.FeatureType)
	require.Len(t, q.TouchedPaths, 2)
	require.Equal(t, skill.RiskHigh, q.MaxRiskLevel)
}

// TestSkippedSkill_Constructable verifies SkippedSkill is constructable with
// SkillID and Reason fields.
func TestSkippedSkill_Constructable(t *testing.T) {
	s := discipline.SkippedSkill{
		SkillID: "01JXSKLLP000000000000APLY0",
		Reason:  discipline.SkipReasonScopeMismatch,
	}
	assert.Equal(t, "01JXSKLLP000000000000APLY0", s.SkillID)
	assert.Equal(t, discipline.SkipReasonScopeMismatch, s.Reason)
}

// TestSkippedSkill_AllReasonConstants verifies the 3 skip-reason constants are
// non-empty and distinct.
func TestSkippedSkill_AllReasonConstants(t *testing.T) {
	reasons := []string{
		discipline.SkipReasonScopeMismatch,
		discipline.SkipReasonAppliesWhenFailed,
		discipline.SkipReasonStatusNotActive,
	}
	seen := make(map[string]struct{}, len(reasons))
	for _, r := range reasons {
		assert.NotEmpty(t, r, "skip-reason constant must be non-empty")
		_, dup := seen[r]
		assert.False(t, dup, "skip-reason constant must be unique: %q", r)
		seen[r] = struct{}{}
	}
}

// TestStructuralContextRef_NotRedeclared verifies StructuralContextRef in the
// discipline package is the SAME type as the one used by PriorContext.StructuralCtx.
// If SkillMatcher redeclared a separate StructuralContextRef type, this test
// would fail with a type-incompatibility compile error — the two fields must
// accept the same *StructuralContextRef pointer without any conversion.
func TestStructuralContextRef_NotRedeclared(t *testing.T) {
	// Constructing a PriorContext with a StructuralContextRef from this same
	// package proves they are the same type (one declaration, not two).
	ref := &discipline.StructuralContextRef{}

	// This assignment compiles only when SkillQuery.StructuralContext and
	// PriorContext.StructuralCtx both accept the SAME *StructuralContextRef type.
	qRef := discipline.SkillQuery{StructuralContext: ref}
	pc := discipline.PriorContext{StructuralCtx: ref}

	// Both fields accept the same pointer — they share the same type declaration.
	assert.Same(t, qRef.StructuralContext, pc.StructuralCtx,
		"SkillQuery.StructuralContext and PriorContext.StructuralCtx must be the same *StructuralContextRef pointer")
}
