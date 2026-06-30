package convention

import (
	"strings"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
)

// ProfileSchemaV1 is the current schema version for ConventionProfile.
// Bump on breaking schema changes.
const ProfileSchemaV1 = 1

// confidenceBase is the base confidence for a detected-from-code pattern with
// exactly one evidence file.
const confidenceBase = 0.60

// confidenceBonus is the bonus added per additional unique evidence file beyond
// the first, for detected-from-code patterns only.
const confidenceBonus = 0.05

// confidenceCap is the maximum confidence reachable by detected-from-code
// patterns. Only curated-skill may reach 1.0.
const confidenceCap = 0.95

// ── Source enum ───────────────────────────────────────────────────────────────

// Source is the provenance of a detected pattern. It is a closed enum.
type Source string

const (
	// SourceCuratedSkill marks patterns loaded from a .claude/skills/ directory.
	// These are human-authored blocking rules; confidence is always 1.0.
	SourceCuratedSkill Source = "curated-skill"

	// SourceDetectedFromCode marks patterns inferred from a heuristic scan of
	// the repository's source tree. Confidence is derived from evidence count
	// via ComputeConfidence and capped at 0.95.
	SourceDetectedFromCode Source = "detected-from-code"

	// SourceBaselineFrameworkDocs marks patterns that come from the framework's
	// canonical documentation (context7 bootstrap). Confidence is approximately
	// 0.5 — lower than any file-evidence-backed pattern.
	SourceBaselineFrameworkDocs Source = "baseline-framework-docs"
)

// IsValid reports whether s is one of the three closed enum values.
func (s Source) IsValid() bool {
	switch s {
	case SourceCuratedSkill, SourceDetectedFromCode, SourceBaselineFrameworkDocs:
		return true
	}
	return false
}

// String returns the underlying string value.
func (s Source) String() string { return string(s) }

// ── PatternEntry value object ─────────────────────────────────────────────────

// PatternEntry is a single detected convention entry within a ConventionProfile.
// Every field is a value type; PatternEntry is immutable once embedded in the
// profile snapshot. The never-invent invariant requires Evidence to be non-empty
// — the ConventionProfile constructor enforces this for every entry.
type PatternEntry struct {
	// Pattern is the stable lowercase-hyphen key for this convention.
	// Example: "nestjs-extends-crudservice".
	Pattern string

	// Source identifies the provenance of this pattern (closed enum).
	Source Source

	// Confidence is the estimated accuracy of this pattern, in [0.0, 1.0].
	// Use ComputeConfidence to derive the correct value from source and evidence
	// count.
	Confidence float64

	// Evidence holds the relative file paths (from repoRoot) that substantiate
	// this pattern. Must be non-empty — zero evidence means the pattern is
	// invented and MUST be rejected by the constructor.
	Evidence []string

	// Rule is an actionable instruction for the apply agent describing what MUST
	// or MUST NOT be done when this pattern applies.
	Rule string

	// SiblingExamples holds canonical file paths the apply agent may use as
	// generation reference material. May be empty.
	SiblingExamples []string

	// RejectedAssumptions lists assumptions that initially seemed plausible but
	// are contradicted by the evidence. Helps the apply agent avoid false paths.
	// May be empty.
	RejectedAssumptions []string

	// Warnings holds non-fatal notes such as "auth context is messy — prefer
	// cotizacion as a reference". May be empty.
	Warnings []string
}

// ── ConventionProfile aggregate root ─────────────────────────────────────────

// ConventionProfile is the aggregate root produced once per INIT extraction run.
// It is immutable after construction — no mutating methods, only constructors
// and getters. This mirrors the Skill aggregate pattern.
//
// ConventionProfile lives in its own package because its lifecycle differs from
// Skill: a profile is machine-extracted at INIT and refreshed on every repo
// change, whereas a Skill is human-promoted through a formal review pipeline.
type ConventionProfile struct {
	projectID     string
	framework     string // canonical lowercase: "nestjs", "go", "angular"
	version       string
	detectedAt    time.Time
	schemaVersion int
	patterns      []PatternEntry
}

// NewConventionProfile constructs a validated ConventionProfile. It enforces all
// domain invariants:
//   - projectID must be non-empty
//   - framework must be non-empty
//   - detectedAt is injected via clock (no time.Now() in this package)
//   - patterns may be nil or empty (degraded profile — allowed)
//   - every PatternEntry in patterns must pass validatePatternEntry
func NewConventionProfile(
	projectID string,
	framework string,
	version string,
	clock shared.Clock,
	patterns []PatternEntry,
) (*ConventionProfile, error) {
	if projectID == "" {
		return nil, ErrEmptyProjectID
	}
	if framework == "" {
		return nil, ErrEmptyFramework
	}

	// Normalise nil to empty slice so callers always see a non-nil Patterns().
	entries := make([]PatternEntry, 0, len(patterns))
	for _, pe := range patterns {
		if err := validatePatternEntry(pe); err != nil {
			return nil, err
		}
		// Deep-copy nested []string fields so the stored profile is not aliased
		// to the caller's slices.
		pe.Evidence = copyStrings(pe.Evidence)
		pe.SiblingExamples = copyStrings(pe.SiblingExamples)
		pe.RejectedAssumptions = copyStrings(pe.RejectedAssumptions)
		pe.Warnings = copyStrings(pe.Warnings)
		entries = append(entries, pe)
	}

	return &ConventionProfile{
		projectID:     projectID,
		framework:     framework,
		version:       version,
		detectedAt:    clock.Now(),
		schemaVersion: ProfileSchemaV1,
		patterns:      entries,
	}, nil
}

// ── Getters ───────────────────────────────────────────────────────────────────

// ProjectID returns the target project identifier.
func (p *ConventionProfile) ProjectID() string { return p.projectID }

// Framework returns the canonical lowercase framework name (e.g. "nestjs").
func (p *ConventionProfile) Framework() string { return p.framework }

// Version returns the framework version string (e.g. "11").
func (p *ConventionProfile) Version() string { return p.version }

// DetectedAt returns the time the profile was extracted. The value is set by
// the injected Clock, never by time.Now() directly.
func (p *ConventionProfile) DetectedAt() time.Time { return p.detectedAt }

// SchemaVersion returns the schema version constant (currently ProfileSchemaV1 = 1).
func (p *ConventionProfile) SchemaVersion() int { return p.schemaVersion }

// Patterns returns a defensive copy of the pattern slice. Mutating the returned
// slice or any nested []string field does not affect the profile.
func (p *ConventionProfile) Patterns() []PatternEntry {
	out := make([]PatternEntry, len(p.patterns))
	for i, pe := range p.patterns {
		pe.Evidence = copyStrings(pe.Evidence)
		pe.SiblingExamples = copyStrings(pe.SiblingExamples)
		pe.RejectedAssumptions = copyStrings(pe.RejectedAssumptions)
		pe.Warnings = copyStrings(pe.Warnings)
		out[i] = pe
	}
	return out
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// validatePatternEntry enforces PatternEntry invariants: non-empty pattern key,
// non-empty rule, non-empty evidence, valid source enum, and confidence in
// [0.0, 1.0].
func validatePatternEntry(pe PatternEntry) error {
	if strings.TrimSpace(pe.Pattern) == "" {
		return ErrEmptyPattern
	}
	if strings.TrimSpace(pe.Rule) == "" {
		return ErrEmptyRule
	}
	if len(pe.Evidence) == 0 {
		return ErrEmptyEvidence
	}
	if !pe.Source.IsValid() {
		return ErrInvalidSource
	}
	if pe.Confidence < 0.0 || pe.Confidence > 1.0 {
		return ErrInvalidConfidence
	}
	return nil
}

// copyStrings returns a new slice containing the same string values as src.
// Returns nil when src is nil so callers that test for nil are unaffected.
func copyStrings(src []string) []string {
	if src == nil {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}

// ── Confidence formula ────────────────────────────────────────────────────────

// ComputeConfidence derives the canonical confidence value for a pattern given
// its source and the number of unique evidence files.
//
// Formula:
//
//	curated-skill             → 1.0 (always)
//	baseline-framework-docs   → 0.5 (always)
//	detected-from-code        → min(0.6 + (evidenceCount-1)*0.05, 0.95)
//
// Any unknown or unspecified source value falls through to the
// detected-from-code formula as the default branch.
//
// This function is exported so callers (extractors) can compute confidence
// before constructing a PatternEntry. The ConventionProfile constructor then
// validates the final value is within [0.0, 1.0].
func ComputeConfidence(source Source, evidenceCount int) float64 {
	switch source {
	case SourceCuratedSkill:
		return 1.0
	case SourceBaselineFrameworkDocs:
		return 0.5
	default: // SourceDetectedFromCode
		if evidenceCount <= 0 {
			return 0.0
		}
		conf := confidenceBase + float64(evidenceCount-1)*confidenceBonus
		if conf > confidenceCap {
			return confidenceCap
		}
		return conf
	}
}
