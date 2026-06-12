package pg

// skill_matcher_versiongate_test.go — T3.7 RED: optional version gate in
// structuralMatches (DG-C7-9).
//
// Test layer: unit, no DB (structuralMatches is a pure function after the
// version-gate addition — the WARN is emitted to slog.Default()).
// RED — version gate does not exist until T3.8 GREEN.

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/structural"
)

// captureWarnLog installs a temporary slog handler that captures log output
// into buf. The returned restore function restores the previous default logger.
func captureWarnLog(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	buf := &bytes.Buffer{}
	h := slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	old := slog.Default()
	slog.SetDefault(slog.New(h))
	return buf, func() { slog.SetDefault(old) }
}

// ── (a) nil map → name-only result identical to pre-change ───────────────────

// TestVersionGate_NilMap_MatchesNameOnly asserts that a skill with nil
// FrameworkMinVersion matches (and mismatches) purely by framework name,
// regardless of detected version. Gate must be byte-for-byte inactive.
func TestVersionGate_NilMap_MatchesNameOnly(t *testing.T) {
	// Skill with no FrameworkMinVersion — old behaviour must be preserved.
	aw := skill.AppliesWhen{
		Framework: []string{"angular"},
	}

	// Must match with a version present.
	q := discipline.SkillQuery{
		StructuralContext: &structural.StructuralContext{
			Frameworks: []structural.FrameworkInfo{{Name: "angular", Version: "22.0.0"}},
		},
	}
	reason, ok := structuralMatches(aw, q)
	assert.True(t, ok, "nil FrameworkMinVersion: name match must pass")
	assert.Empty(t, reason)

	// Must also match with an empty version string (version not captured).
	q2 := discipline.SkillQuery{
		StructuralContext: &structural.StructuralContext{
			Frameworks: []structural.FrameworkInfo{{Name: "angular", Version: ""}},
		},
	}
	reason2, ok2 := structuralMatches(aw, q2)
	assert.True(t, ok2, "nil FrameworkMinVersion: must match even when detected version empty")
	assert.Empty(t, reason2)
}

// ── (b) empty map → gate inactive ────────────────────────────────────────────

// TestVersionGate_EmptyMap_GateInactive asserts that an explicitly empty
// FrameworkMinVersion map does not activate the version gate.
func TestVersionGate_EmptyMap_GateInactive(t *testing.T) {
	aw := skill.AppliesWhen{
		Framework:           []string{"angular"},
		FrameworkMinVersion: map[string]string{},
	}
	q := discipline.SkillQuery{
		StructuralContext: &structural.StructuralContext{
			Frameworks: []structural.FrameworkInfo{{Name: "angular", Version: "18.0.0"}},
		},
	}
	reason, ok := structuralMatches(aw, q)
	assert.True(t, ok, "empty FrameworkMinVersion map must leave gate inactive")
	assert.Empty(t, reason)
}

// ── (c) gate pass ─────────────────────────────────────────────────────────────

// TestVersionGate_Pass_Equal asserts that detected major == min major passes.
func TestVersionGate_Pass_Equal(t *testing.T) {
	aw := skill.AppliesWhen{
		Framework:           []string{"angular"},
		FrameworkMinVersion: map[string]string{"angular": "22.0.0"},
	}
	q := discipline.SkillQuery{
		StructuralContext: &structural.StructuralContext{
			Frameworks: []structural.FrameworkInfo{{Name: "angular", Version: "22.0.0"}},
		},
	}
	reason, ok := structuralMatches(aw, q)
	assert.True(t, ok, "detected major == min major must pass gate")
	assert.Empty(t, reason)
}

// TestVersionGate_Pass_DetectedNewer asserts that detected major > min major passes.
func TestVersionGate_Pass_DetectedNewer(t *testing.T) {
	aw := skill.AppliesWhen{
		Framework:           []string{"angular"},
		FrameworkMinVersion: map[string]string{"angular": "22.0.0"},
	}
	q := discipline.SkillQuery{
		StructuralContext: &structural.StructuralContext{
			Frameworks: []structural.FrameworkInfo{{Name: "angular", Version: "23.1.0"}},
		},
	}
	reason, ok := structuralMatches(aw, q)
	assert.True(t, ok, "detected major > min major must pass gate")
	assert.Empty(t, reason)
}

// ── (d) gate fail ─────────────────────────────────────────────────────────────

// TestVersionGate_Fail_DetectedOlder asserts that detected major < min major
// results in SkipReasonStructuralMismatch.
func TestVersionGate_Fail_DetectedOlder(t *testing.T) {
	aw := skill.AppliesWhen{
		Framework:           []string{"angular"},
		FrameworkMinVersion: map[string]string{"angular": "22.0.0"},
	}
	q := discipline.SkillQuery{
		StructuralContext: &structural.StructuralContext{
			Frameworks: []structural.FrameworkInfo{{Name: "angular", Version: "18.2.0"}},
		},
	}
	reason, ok := structuralMatches(aw, q)
	assert.False(t, ok, "detected major < min major must fail gate")
	assert.Equal(t, discipline.SkipReasonStructuralMismatch, reason)
}

// ── (e) name mismatch → gate never evaluated ──────────────────────────────────

// TestVersionGate_NameMismatch_GateNotEvaluated asserts that when the name
// gate fails, the version gate is irrelevant (skill not returned).
func TestVersionGate_NameMismatch_GateNotEvaluated(t *testing.T) {
	aw := skill.AppliesWhen{
		Framework:           []string{"react"},
		FrameworkMinVersion: map[string]string{"react": "18.0.0"},
	}
	q := discipline.SkillQuery{
		StructuralContext: &structural.StructuralContext{
			Frameworks: []structural.FrameworkInfo{{Name: "angular", Version: "22.0.0"}},
		},
	}
	reason, ok := structuralMatches(aw, q)
	assert.False(t, ok, "name mismatch must fail before version gate")
	assert.Equal(t, discipline.SkipReasonStructuralMismatch, reason)
}

// ── (f) per-framework selectivity ─────────────────────────────────────────────

// TestVersionGate_Selective_AngularNoGate asserts that when FrameworkMinVersion
// only has an entry for "react", an angular skill is returned via name-only path.
func TestVersionGate_Selective_AngularNoGate(t *testing.T) {
	aw := skill.AppliesWhen{
		Framework:           []string{"angular"},
		FrameworkMinVersion: map[string]string{"react": "18.0.0"},
	}
	q := discipline.SkillQuery{
		StructuralContext: &structural.StructuralContext{
			Frameworks: []structural.FrameworkInfo{{Name: "angular", Version: "15.0.0"}},
		},
	}
	reason, ok := structuralMatches(aw, q)
	assert.True(t, ok, "angular must pass when only react has a version gate")
	assert.Empty(t, reason)
}

// ── (g) fail-open: unparseable versions produce WARN and return skill ─────────

// TestVersionGate_FailOpen_UnparseableDetected asserts that an unparseable
// detected version (e.g. "edge") causes fail-open: skill returned + WARN logged.
func TestVersionGate_FailOpen_UnparseableDetected(t *testing.T) {
	buf, restore := captureWarnLog(t)
	defer restore()

	aw := skill.AppliesWhen{
		Framework:           []string{"angular"},
		FrameworkMinVersion: map[string]string{"angular": "22.0.0"},
	}
	q := discipline.SkillQuery{
		StructuralContext: &structural.StructuralContext{
			Frameworks: []structural.FrameworkInfo{{Name: "angular", Version: "edge"}},
		},
	}
	reason, ok := structuralMatches(aw, q)
	assert.True(t, ok, "unparseable detected version must fail open (skill returned)")
	assert.Empty(t, reason)
	assert.Contains(t, buf.String(), "WARN", "a WARN must be logged for unparseable detected version")
}

// TestVersionGate_FailOpen_UnparseableMin asserts that an unparseable
// FrameworkMinVersion value causes fail-open: skill returned + WARN logged.
func TestVersionGate_FailOpen_UnparseableMin(t *testing.T) {
	buf, restore := captureWarnLog(t)
	defer restore()

	aw := skill.AppliesWhen{
		Framework:           []string{"angular"},
		FrameworkMinVersion: map[string]string{"angular": "not-a-version"},
	}
	q := discipline.SkillQuery{
		StructuralContext: &structural.StructuralContext{
			Frameworks: []structural.FrameworkInfo{{Name: "angular", Version: "22.0.0"}},
		},
	}
	reason, ok := structuralMatches(aw, q)
	assert.True(t, ok, "unparseable min version must fail open (skill returned)")
	assert.Empty(t, reason)
	assert.Contains(t, buf.String(), "WARN", "a WARN must be logged for unparseable min version")
}

// ── (h) no DB/I-O during comparison ──────────────────────────────────────────

// TestVersionGate_NoDBDuringComparison documents that structuralMatches is a
// pure in-memory function. The test runs without any DB backing by construction
// (no pgxpool, no SkillRepo, direct function call only).
func TestVersionGate_NoDBDuringComparison(t *testing.T) {
	aw := skill.AppliesWhen{
		Framework:           []string{"angular"},
		FrameworkMinVersion: map[string]string{"angular": "22.0.0"},
	}
	q := discipline.SkillQuery{
		StructuralContext: &structural.StructuralContext{
			Frameworks: []structural.FrameworkInfo{{Name: "angular", Version: "22.0.0"}},
		},
	}
	// If this completes without DB setup, the function is provably in-memory.
	reason, ok := structuralMatches(aw, q)
	require.True(t, ok)
	require.Empty(t, reason)
}

// ── (i) legacy seeds with no map still returned ───────────────────────────────

// TestVersionGate_LegacySeeds_Unaffected asserts that skills without a
// FrameworkMinVersion (as all legacy seeds would have) are returned normally.
func TestVersionGate_LegacySeeds_Unaffected(t *testing.T) {
	// Legacy seed: framework set, no FrameworkMinVersion.
	aw := skill.AppliesWhen{
		Framework: []string{"angular"},
	}
	q := discipline.SkillQuery{
		StructuralContext: &structural.StructuralContext{
			Frameworks: []structural.FrameworkInfo{
				{Name: "angular", Version: "22.0.0"},
			},
		},
	}
	reason, ok := structuralMatches(aw, q)
	assert.True(t, ok, "legacy seed without FrameworkMinVersion must pass unchanged")
	assert.Empty(t, reason)

	// Another legacy seed: no framework declared at all.
	awEmpty := skill.AppliesWhen{}
	reason2, ok2 := structuralMatches(awEmpty, q)
	assert.True(t, ok2, "legacy seed with no applies_when constraints must always pass")
	assert.Empty(t, reason2)
}

// ── v-prefix stripping ────────────────────────────────────────────────────────

// TestVersionGate_VPrefixStripped asserts that "v" prefixes on either side of
// the comparison are stripped correctly (spec: v22.0.0 vs v22.3.1 → passes).
func TestVersionGate_VPrefixStripped(t *testing.T) {
	aw := skill.AppliesWhen{
		Framework:           []string{"angular"},
		FrameworkMinVersion: map[string]string{"angular": "v22.0.0"},
	}
	q := discipline.SkillQuery{
		StructuralContext: &structural.StructuralContext{
			Frameworks: []structural.FrameworkInfo{{Name: "angular", Version: "v22.3.1"}},
		},
	}
	reason, ok := structuralMatches(aw, q)
	assert.True(t, ok, "v-prefix on both sides must be stripped before comparison")
	assert.Empty(t, reason)
}
