package pg

// skill_matcher_structural_test.go — Group F RED tests (D-M3-4)
//
// Unit tests for the structuralMatches pure function that enforces
// applies_when.framework and applies_when.language filtering against a live
// StructuralContext. Written FIRST (RED) — structuralMatches does not exist
// until F.5 GREEN.
//
// Test layer: Unit (white-box, no DB). structuralMatches is a pure function.

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/structural"
)

// ── F.1: Framework match passes filter ───────────────────────────────────────

// TestStructuralMatches_FrameworkMatch_Passes asserts that a skill declaring
// applies_when.framework=["nextjs"] passes when StructuralContext.Frameworks
// contains "nextjs". MUST FAIL until F.5 GREEN adds structuralMatches.
func TestStructuralMatches_FrameworkMatch_Passes(t *testing.T) {
	aw := skill.AppliesWhen{
		Framework: []string{"nextjs"},
	}
	q := discipline.SkillQuery{
		StructuralContext: &structural.StructuralContext{
			Frameworks: []structural.FrameworkInfo{
				{Name: "nextjs"},
				{Name: "react"},
			},
		},
	}

	reason, ok := structuralMatches(aw, q)

	assert.True(t, ok, "framework match must pass the structural filter")
	assert.Empty(t, reason, "no skip reason when filter passes")
}

// ── F.2: Framework mismatch skips skill with SkipReasonStructuralMismatch ────

// TestStructuralMatches_FrameworkMismatch_SkipsWithReason asserts that a skill
// declaring applies_when.framework=["rails"] is excluded when
// StructuralContext.Frameworks contains only "nextjs".
// MUST FAIL until F.5 GREEN.
func TestStructuralMatches_FrameworkMismatch_SkipsWithReason(t *testing.T) {
	aw := skill.AppliesWhen{
		Framework: []string{"rails"},
	}
	q := discipline.SkillQuery{
		StructuralContext: &structural.StructuralContext{
			Frameworks: []structural.FrameworkInfo{
				{Name: "nextjs"},
			},
		},
	}

	reason, ok := structuralMatches(aw, q)

	assert.False(t, ok, "framework mismatch must fail the structural filter")
	assert.Equal(t, discipline.SkipReasonStructuralMismatch, reason,
		"skip reason must be SkipReasonStructuralMismatch")
}

// ── F.3: Nil StructuralContext is a no-op (fail-open) ────────────────────────

// TestStructuralMatches_NilContext_SkipsFilter asserts that when
// q.StructuralContext is nil, the structural filter is a no-op — the skill
// passes regardless of its declared framework/language constraints.
// MUST FAIL until F.5 GREEN.
func TestStructuralMatches_NilContext_SkipsFilter(t *testing.T) {
	// Skill with a declared framework constraint.
	aw := skill.AppliesWhen{
		Framework: []string{"rails"},
	}
	q := discipline.SkillQuery{
		StructuralContext: nil, // no structural data → no filtering
	}

	reason, ok := structuralMatches(aw, q)

	assert.True(t, ok, "nil StructuralContext must pass structural filter (fail-open)")
	assert.Empty(t, reason, "no skip reason when filter is skipped")
}

// ── F.4: Language match and mismatch ─────────────────────────────────────────

// TestStructuralMatches_LanguageMatch_Passes asserts that a skill declaring
// applies_when.language=["typescript"] passes when StructuralContext.Languages
// contains "typescript". MUST FAIL until F.5 GREEN.
func TestStructuralMatches_LanguageMatch_Passes(t *testing.T) {
	aw := skill.AppliesWhen{
		Language: []string{"typescript"},
	}
	q := discipline.SkillQuery{
		StructuralContext: &structural.StructuralContext{
			Languages: []structural.LanguageInfo{
				{Name: "typescript"},
			},
		},
	}

	reason, ok := structuralMatches(aw, q)

	assert.True(t, ok, "language match must pass the structural filter")
	assert.Empty(t, reason, "no skip reason when filter passes")
}

// TestStructuralMatches_LanguageMismatch_SkipsWithReason asserts that a skill
// declaring applies_when.language=["python"] is excluded when
// StructuralContext.Languages contains only "typescript".
// MUST FAIL until F.5 GREEN.
func TestStructuralMatches_LanguageMismatch_SkipsWithReason(t *testing.T) {
	aw := skill.AppliesWhen{
		Language: []string{"python"},
	}
	q := discipline.SkillQuery{
		StructuralContext: &structural.StructuralContext{
			Languages: []structural.LanguageInfo{
				{Name: "typescript"},
			},
		},
	}

	reason, ok := structuralMatches(aw, q)

	assert.False(t, ok, "language mismatch must fail the structural filter")
	assert.Equal(t, discipline.SkipReasonStructuralMismatch, reason,
		"skip reason must be SkipReasonStructuralMismatch")
}

// ── Triangulation: empty applies_when passes unconditionally ─────────────────

// TestStructuralMatches_EmptyAppliesWhen_Passes asserts that a skill with
// empty Framework and Language in applies_when always passes the structural
// filter, regardless of StructuralContext content.
func TestStructuralMatches_EmptyAppliesWhen_Passes(t *testing.T) {
	aw := skill.AppliesWhen{} // no framework, no language constraints
	q := discipline.SkillQuery{
		StructuralContext: &structural.StructuralContext{
			Frameworks: []structural.FrameworkInfo{{Name: "rails"}},
			Languages:  []structural.LanguageInfo{{Name: "ruby"}},
		},
	}

	reason, ok := structuralMatches(aw, q)

	assert.True(t, ok, "empty applies_when.framework/language must pass unconditionally")
	assert.Empty(t, reason)
}

// TestStructuralMatches_FrameworkPresentInList_CaseInsensitive asserts that
// framework matching is case-insensitive: skill declares "NextJS", context has
// "nextjs" — should match.
func TestStructuralMatches_FrameworkPresentInList_CaseInsensitive(t *testing.T) {
	aw := skill.AppliesWhen{
		Framework: []string{"NextJS"}, // declared with different case
	}
	q := discipline.SkillQuery{
		StructuralContext: &structural.StructuralContext{
			Frameworks: []structural.FrameworkInfo{
				{Name: "nextjs"}, // detected lowercase
			},
		},
	}

	reason, ok := structuralMatches(aw, q)

	assert.True(t, ok, "framework matching must be case-insensitive")
	assert.Empty(t, reason)
}
