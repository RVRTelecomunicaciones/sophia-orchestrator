package discipline_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/structural"
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
// StructuralContext is now *structural.StructuralContext (E.4 — domain move).
func TestSkillQuery_ExplicitFields(t *testing.T) {
	sc := &structural.StructuralContext{
		SchemaVersion: structural.SchemaV1,
		ProjectID:     "proj-123",
	}
	q := discipline.SkillQuery{
		Phase:             phase.PhaseApply,
		ProjectID:         "proj-123",
		RepoID:            "repo-456",
		StructuralContext: sc,
		FeatureType:       "bugfix",
		TouchedPaths:      []string{"internal/domain/**", "internal/adapters/**"},
		MaxRiskLevel:      skill.RiskHigh,
	}

	require.Equal(t, phase.PhaseApply, q.Phase)
	require.Equal(t, "proj-123", q.ProjectID)
	require.Equal(t, "repo-456", q.RepoID)
	require.Equal(t, sc, q.StructuralContext)
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

// TestStructuralContext_SharedType verifies that SkillQuery.StructuralContext and
// PriorContext.StructuralCtx both accept the SAME *structural.StructuralContext
// pointer (E.3 + E.4 post-domain-move). Written to replace the prior
// TestStructuralContextRef_NotRedeclared which tested the now-deleted stub type.
func TestStructuralContext_SharedType(t *testing.T) {
	// A single *structural.StructuralContext value must be assignable to both
	// SkillQuery.StructuralContext and PriorContext.StructuralCtx without
	// any conversion — they are the same type (not separate aliases).
	sc := &structural.StructuralContext{SchemaVersion: structural.SchemaV1}

	qRef := discipline.SkillQuery{StructuralContext: sc}
	pc := discipline.PriorContext{StructuralCtx: sc}

	assert.Same(t, qRef.StructuralContext, pc.StructuralCtx,
		"SkillQuery.StructuralContext and PriorContext.StructuralCtx must share the same *structural.StructuralContext pointer")
}
