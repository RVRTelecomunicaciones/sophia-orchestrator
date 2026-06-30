// Package convention_test covers the ConventionProfile aggregate and its
// invariants at 100% statement coverage. All tests are pure — no I/O, no
// real filesystem, no time.Now().
package convention_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/convention"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
)

// ── helpers ────────────────────────────────────────────────────────────────────

var (
	fixedTime = time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	fixedClock = shared.FixedClock(fixedTime)

	validProjectID = "project-cajachica"
	validFramework = "nestjs"
	validVersion   = "11"

	oneEvidence = []string{"src/motivo/motivo.service.ts"}
)

// validEntry returns a PatternEntry that passes all invariants.
func validEntry() convention.PatternEntry {
	return convention.PatternEntry{
		Pattern:    "nestjs-extends-crudservice",
		Source:     convention.SourceDetectedFromCode,
		Confidence: 0.60,
		Evidence:   oneEvidence,
		Rule:       "All services MUST extend CrudService<Entity, CreateDTO, UpdateDTO>.",
	}
}

// ── Task 1.1: Constructor invariants ──────────────────────────────────────────

func TestNewConventionProfile_ValidProfileWithOnePattern(t *testing.T) {
	p, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{validEntry()},
	)
	require.NoError(t, err)
	require.NotNil(t, p)
	require.Len(t, p.Patterns(), 1)
	require.Equal(t, "nestjs-extends-crudservice", p.Patterns()[0].Pattern)
}

func TestNewConventionProfile_EmptyProjectIDRejected(t *testing.T) {
	_, err := convention.NewConventionProfile(
		"", validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{validEntry()},
	)
	require.ErrorIs(t, err, convention.ErrEmptyProjectID)
}

func TestNewConventionProfile_EmptyFrameworkRejected(t *testing.T) {
	_, err := convention.NewConventionProfile(
		validProjectID, "", validVersion, fixedClock,
		[]convention.PatternEntry{validEntry()},
	)
	require.ErrorIs(t, err, convention.ErrEmptyFramework)
}

func TestNewConventionProfile_ZeroEvidencePatternRejected(t *testing.T) {
	entry := validEntry()
	entry.Evidence = []string{}
	_, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{entry},
	)
	require.ErrorIs(t, err, convention.ErrEmptyEvidence)
}

func TestNewConventionProfile_NilEvidencePatternRejected(t *testing.T) {
	entry := validEntry()
	entry.Evidence = nil
	_, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{entry},
	)
	require.ErrorIs(t, err, convention.ErrEmptyEvidence)
}

func TestNewConventionProfile_ConfidenceAboveOneRejected(t *testing.T) {
	entry := validEntry()
	entry.Confidence = 1.05
	_, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{entry},
	)
	require.ErrorIs(t, err, convention.ErrInvalidConfidence)
}

func TestNewConventionProfile_ConfidenceBelowZeroRejected(t *testing.T) {
	entry := validEntry()
	entry.Confidence = -0.01
	_, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{entry},
	)
	require.ErrorIs(t, err, convention.ErrInvalidConfidence)
}

func TestNewConventionProfile_InvalidSourceRejected(t *testing.T) {
	entry := validEntry()
	entry.Source = convention.Source("invented")
	_, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{entry},
	)
	require.ErrorIs(t, err, convention.ErrInvalidSource)
}

func TestNewConventionProfile_DegradedProfileEmptyPatternsAllowed(t *testing.T) {
	p, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{},
	)
	require.NoError(t, err)
	require.NotNil(t, p)
	require.Empty(t, p.Patterns())
}

func TestNewConventionProfile_NilPatternsAllowed(t *testing.T) {
	p, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, p)
	require.Empty(t, p.Patterns())
}

// ── Getters ────────────────────────────────────────────────────────────────────

func TestConventionProfile_Getters(t *testing.T) {
	p, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{validEntry()},
	)
	require.NoError(t, err)

	require.Equal(t, validProjectID, p.ProjectID())
	require.Equal(t, validFramework, p.Framework())
	require.Equal(t, validVersion, p.Version())
	require.Equal(t, fixedTime, p.DetectedAt())
	require.Equal(t, convention.ProfileSchemaV1, p.SchemaVersion())
}

// Patterns() must return a defensive copy.
func TestConventionProfile_PatternsDefensiveCopy(t *testing.T) {
	p, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{validEntry()},
	)
	require.NoError(t, err)

	got := p.Patterns()
	got[0].Pattern = "mutated"
	require.Equal(t, "nestjs-extends-crudservice", p.Patterns()[0].Pattern)
}

// ── Task 1.2: Confidence scoring table tests ───────────────────────────────────

func TestConventionProfile_ConfidenceScoring_OneFileyieldsBase(t *testing.T) {
	// 1 evidence file → base = 0.60
	entry := validEntry()
	entry.Source = convention.SourceDetectedFromCode
	entry.Evidence = oneEvidence  // 1 file
	entry.Confidence = convention.ComputeConfidence(convention.SourceDetectedFromCode, len(oneEvidence))

	p, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{entry},
	)
	require.NoError(t, err)
	require.InDelta(t, 0.60, p.Patterns()[0].Confidence, 0.001)
}

func TestConventionProfile_ConfidenceScoring_FiveFilesYields0_80(t *testing.T) {
	// 5 evidence files → 0.6 + 4*0.05 = 0.80
	fiveEvidence := []string{"a.ts", "b.ts", "c.ts", "d.ts", "e.ts"}
	entry := validEntry()
	entry.Source = convention.SourceDetectedFromCode
	entry.Evidence = fiveEvidence
	entry.Confidence = convention.ComputeConfidence(convention.SourceDetectedFromCode, len(fiveEvidence))

	p, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{entry},
	)
	require.NoError(t, err)
	require.InDelta(t, 0.80, p.Patterns()[0].Confidence, 0.001)
}

func TestConventionProfile_ConfidenceScoring_NineFilesYieldsCap0_95(t *testing.T) {
	// 9 evidence files → 0.6 + 8*0.05 = 1.00, capped to 0.95
	nineEvidence := make([]string, 9)
	for i := range nineEvidence {
		nineEvidence[i] = "file.ts"
	}
	entry := validEntry()
	entry.Source = convention.SourceDetectedFromCode
	entry.Evidence = nineEvidence
	entry.Confidence = convention.ComputeConfidence(convention.SourceDetectedFromCode, len(nineEvidence))

	p, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{entry},
	)
	require.NoError(t, err)
	require.InDelta(t, 0.95, p.Patterns()[0].Confidence, 0.001)
}

func TestConventionProfile_ConfidenceScoring_CuratedSkillAlways1_0(t *testing.T) {
	// curated-skill → always 1.0 regardless of evidence count
	entry := validEntry()
	entry.Source = convention.SourceCuratedSkill
	entry.Evidence = oneEvidence
	entry.Confidence = convention.ComputeConfidence(convention.SourceCuratedSkill, len(oneEvidence))

	p, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{entry},
	)
	require.NoError(t, err)
	require.InDelta(t, 1.0, p.Patterns()[0].Confidence, 0.001)
}

func TestConventionProfile_ConfidenceScoring_BaselineFrameworkDocs0_5(t *testing.T) {
	// baseline-framework-docs → ~0.5
	entry := validEntry()
	entry.Source = convention.SourceBaselineFrameworkDocs
	entry.Evidence = oneEvidence
	entry.Confidence = convention.ComputeConfidence(convention.SourceBaselineFrameworkDocs, len(oneEvidence))

	p, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{entry},
	)
	require.NoError(t, err)
	require.InDelta(t, 0.5, p.Patterns()[0].Confidence, 0.001)
}

// ── Source enum validation ─────────────────────────────────────────────────────

func TestSource_IsValid_AllValidValues(t *testing.T) {
	validSources := []convention.Source{
		convention.SourceCuratedSkill,
		convention.SourceDetectedFromCode,
		convention.SourceBaselineFrameworkDocs,
	}
	for _, s := range validSources {
		require.True(t, s.IsValid(), "expected %q to be valid", s)
	}
}

func TestSource_IsValid_EmptyStringInvalid(t *testing.T) {
	require.False(t, convention.Source("").IsValid())
}

func TestSource_IsValid_UnknownValueInvalid(t *testing.T) {
	require.False(t, convention.Source("invented-source").IsValid())
}

// ── Multiple patterns — partial invalid rejects whole profile ──────────────────

func TestNewConventionProfile_MultiplePatterns_OneInvalidRejectsAll(t *testing.T) {
	good := validEntry()
	bad := validEntry()
	bad.Evidence = nil

	_, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{good, bad},
	)
	require.ErrorIs(t, err, convention.ErrEmptyEvidence)
}

func TestNewConventionProfile_MultiplePatterns_AllValidConstructs(t *testing.T) {
	e1 := validEntry()
	e2 := convention.PatternEntry{
		Pattern:    "nestjs-partialtype-update-dto",
		Source:     convention.SourceDetectedFromCode,
		Confidence: convention.ComputeConfidence(convention.SourceDetectedFromCode, 4),
		Evidence:   []string{"a.dto.ts", "b.dto.ts", "c.dto.ts", "d.dto.ts"},
		Rule:       "Update DTOs MUST extend PartialType(CreateDTO).",
	}

	p, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{e1, e2},
	)
	require.NoError(t, err)
	require.Len(t, p.Patterns(), 2)
}

// ── PatternEntry optional fields ───────────────────────────────────────────────

func TestNewConventionProfile_PatternEntryOptionalFieldsMayBeEmpty(t *testing.T) {
	entry := convention.PatternEntry{
		Pattern:             "nestjs-extends-crudservice",
		Source:              convention.SourceCuratedSkill,
		Confidence:          1.0,
		Evidence:            oneEvidence,
		Rule:                "Services MUST extend CrudService.",
		SiblingExamples:     nil,
		RejectedAssumptions: nil,
		Warnings:            nil,
	}
	p, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{entry},
	)
	require.NoError(t, err)
	require.NotNil(t, p)
}

// ── Confidence boundary values ─────────────────────────────────────────────────

func TestNewConventionProfile_ConfidenceExactlyZeroAllowed(t *testing.T) {
	entry := validEntry()
	entry.Confidence = 0.0
	p, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{entry},
	)
	require.NoError(t, err)
	require.NotNil(t, p)
}

func TestNewConventionProfile_ConfidenceExactlyOneAllowed(t *testing.T) {
	entry := validEntry()
	entry.Source = convention.SourceCuratedSkill
	entry.Confidence = 1.0
	p, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{entry},
	)
	require.NoError(t, err)
	require.NotNil(t, p)
}

// ── DetectedAt uses Clock (no time.Now()) ──────────────────────────────────────

func TestNewConventionProfile_DetectedAtUsesInjectedClock(t *testing.T) {
	customTime := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	customClock := shared.FixedClock(customTime)

	p, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, customClock,
		[]convention.PatternEntry{validEntry()},
	)
	require.NoError(t, err)
	require.Equal(t, customTime, p.DetectedAt())
}

// ── ComputeConfidence edge cases ───────────────────────────────────────────────

func TestComputeConfidence_DetectedFromCode_CapAt0_95(t *testing.T) {
	tests := []struct {
		name          string
		evidenceCount int
		wantConf      float64
	}{
		{"1 file → 0.60", 1, 0.60},
		{"2 files → 0.65", 2, 0.65},
		{"3 files → 0.70", 3, 0.70},
		{"4 files → 0.75", 4, 0.75},
		{"5 files → 0.80", 5, 0.80},
		{"6 files → 0.85", 6, 0.85},
		{"7 files → 0.90", 7, 0.90},
		{"8 files → 0.95 (cap)", 8, 0.95},
		{"9 files → 0.95 (cap)", 9, 0.95},
		{"20 files → 0.95 (cap)", 20, 0.95},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convention.ComputeConfidence(convention.SourceDetectedFromCode, tt.evidenceCount)
			require.InDelta(t, tt.wantConf, got, 0.001, "evidenceCount=%d", tt.evidenceCount)
		})
	}
}

func TestComputeConfidence_CuratedSkill_Always1_0(t *testing.T) {
	for _, n := range []int{0, 1, 5, 100} {
		require.InDelta(t, 1.0, convention.ComputeConfidence(convention.SourceCuratedSkill, n), 0.001)
	}
}

func TestComputeConfidence_BaselineFrameworkDocs_Always0_5(t *testing.T) {
	for _, n := range []int{0, 1, 5, 100} {
		require.InDelta(t, 0.5, convention.ComputeConfidence(convention.SourceBaselineFrameworkDocs, n), 0.001)
	}
}

func TestComputeConfidence_DetectedFromCode_ZeroOrNegativeEvidence_Returns0(t *testing.T) {
	// evidenceCount <= 0 should not produce a negative or NaN confidence.
	got := convention.ComputeConfidence(convention.SourceDetectedFromCode, 0)
	require.InDelta(t, 0.0, got, 0.001)
}

// ── Source.String() ────────────────────────────────────────────────────────────

func TestSource_String(t *testing.T) {
	require.Equal(t, "curated-skill", convention.SourceCuratedSkill.String())
	require.Equal(t, "detected-from-code", convention.SourceDetectedFromCode.String())
	require.Equal(t, "baseline-framework-docs", convention.SourceBaselineFrameworkDocs.String())
}

// ── Fix 1: Deep-copy of nested []string fields ────────────────────────────────

// Mutating a returned PatternEntry's Evidence must not affect the profile.
func TestConventionProfile_Patterns_DeepCopyEvidence(t *testing.T) {
	p, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{validEntry()},
	)
	require.NoError(t, err)

	got := p.Patterns()
	got[0].Evidence[0] = "mutated"
	require.Equal(t, "src/motivo/motivo.service.ts", p.Patterns()[0].Evidence[0])
}

// Mutating a returned PatternEntry's Warnings must not affect the profile.
func TestConventionProfile_Patterns_DeepCopyWarnings(t *testing.T) {
	entry := validEntry()
	entry.Warnings = []string{"prefer cotizacion as reference"}

	p, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{entry},
	)
	require.NoError(t, err)

	got := p.Patterns()
	got[0].Warnings[0] = "mutated"
	require.Equal(t, "prefer cotizacion as reference", p.Patterns()[0].Warnings[0])
}

// Mutating caller's input slice after construction must not affect the profile.
func TestNewConventionProfile_DeepCopyCallerEvidence(t *testing.T) {
	evidence := []string{"src/motivo/motivo.service.ts"}
	entry := convention.PatternEntry{
		Pattern:    "nestjs-extends-crudservice",
		Source:     convention.SourceDetectedFromCode,
		Confidence: 0.60,
		Evidence:   evidence,
		Rule:       "All services MUST extend CrudService.",
	}

	p, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{entry},
	)
	require.NoError(t, err)

	evidence[0] = "mutated-by-caller"
	require.Equal(t, "src/motivo/motivo.service.ts", p.Patterns()[0].Evidence[0])
}

// Mutating caller's SiblingExamples slice after construction must not affect the profile.
func TestNewConventionProfile_DeepCopyCallerSiblingExamples(t *testing.T) {
	siblings := []string{"src/cotizacion/cotizacion.service.ts"}
	entry := convention.PatternEntry{
		Pattern:         "nestjs-extends-crudservice",
		Source:          convention.SourceDetectedFromCode,
		Confidence:      0.60,
		Evidence:        oneEvidence,
		Rule:            "All services MUST extend CrudService.",
		SiblingExamples: siblings,
	}

	p, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{entry},
	)
	require.NoError(t, err)

	siblings[0] = "mutated-by-caller"
	require.Equal(t, "src/cotizacion/cotizacion.service.ts", p.Patterns()[0].SiblingExamples[0])
}

// Mutating caller's RejectedAssumptions slice after construction must not affect the profile.
func TestNewConventionProfile_DeepCopyCallerRejectedAssumptions(t *testing.T) {
	rejected := []string{"assumed nestjs uses repositories directly"}
	entry := convention.PatternEntry{
		Pattern:             "nestjs-extends-crudservice",
		Source:              convention.SourceDetectedFromCode,
		Confidence:          0.60,
		Evidence:            oneEvidence,
		Rule:                "All services MUST extend CrudService.",
		RejectedAssumptions: rejected,
	}

	p, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{entry},
	)
	require.NoError(t, err)

	rejected[0] = "mutated-by-caller"
	require.Equal(t, "assumed nestjs uses repositories directly", p.Patterns()[0].RejectedAssumptions[0])
}

// ── Fix 2: Validate Pattern and Rule are non-empty ────────────────────────────

func TestNewConventionProfile_EmptyPatternKeyRejected(t *testing.T) {
	entry := validEntry()
	entry.Pattern = ""
	_, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{entry},
	)
	require.ErrorIs(t, err, convention.ErrEmptyPattern)
}

func TestNewConventionProfile_WhitespacePatternKeyRejected(t *testing.T) {
	entry := validEntry()
	entry.Pattern = "   "
	_, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{entry},
	)
	require.ErrorIs(t, err, convention.ErrEmptyPattern)
}

func TestNewConventionProfile_EmptyRuleRejected(t *testing.T) {
	entry := validEntry()
	entry.Rule = ""
	_, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{entry},
	)
	require.ErrorIs(t, err, convention.ErrEmptyRule)
}

func TestNewConventionProfile_WhitespaceRuleRejected(t *testing.T) {
	entry := validEntry()
	entry.Rule = "   "
	_, err := convention.NewConventionProfile(
		validProjectID, validFramework, validVersion, fixedClock,
		[]convention.PatternEntry{entry},
	)
	require.ErrorIs(t, err, convention.ErrEmptyRule)
}

// ── Fix 3: ComputeConfidence default-branch fallback ─────────────────────────

// Unknown/unspecified sources fall through to the detected-from-code formula.
func TestComputeConfidence_UnknownSource_FallsBackToDetectedFormula(t *testing.T) {
	// 3 evidence files via detected-from-code formula: 0.6 + 2*0.05 = 0.70
	got := convention.ComputeConfidence(convention.Source("invented"), 3)
	require.InDelta(t, 0.70, got, 0.001)
}
