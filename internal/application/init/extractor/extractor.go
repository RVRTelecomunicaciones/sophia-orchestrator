// Package extractor implements ProfileExtractor: it walks a target repo's
// source tree, runs framework-specific detection heuristics, loads curated
// skills, applies the source-ladder precedence rule, and constructs an
// evidence-based ConventionProfile via the domain aggregate constructor.
//
// The never-invent invariant is enforced here (and again by the domain
// constructor): any candidate PatternEntry with zero evidence is silently
// dropped before ConventionProfile is constructed.
//
// OS boundary: file content is read through the single readFileBytes function
// (which calls os.ReadFile). Directory listing uses os.ReadDir and os.Stat only
// for existence checks. This scopes the OS surface to a single content-reading
// boundary, making the package testable by stubbing readFileBytes alone.
package extractor

import (
	"context"
	"os"
	"strings"

	initphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/detector"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/convention"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
)

// Compile-time assertion: Extractor must satisfy the initphase.ProfileExtractor port.
var _ initphase.ProfileExtractor = (*Extractor)(nil)

// Extractor implements the initphase.ProfileExtractor port interface.
// Construct via New; all dependencies are injected at construction time.
type Extractor struct {
	projectID string
	clock     shared.Clock
}

// New constructs an Extractor. projectID is the target project identifier
// used to stamp the ConventionProfile. clock is injected for deterministic
// time access (no time.Now() inside this package).
func New(projectID string, clock shared.Clock) *Extractor {
	return &Extractor{
		projectID: projectID,
		clock:     clock,
	}
}

// Extract walks repoRoot and produces a ConventionProfile.
//
// Sequence:
//  1. Load curated skills from <repoRoot>/.claude/skills/ (source=curated-skill).
//  2. If sc.Frameworks identifies a supported framework (nestjs), run the
//     corresponding detector.
//  3. Apply the source ladder: curated-skill > detected-from-code for the same
//     pattern key.
//  4. Drop any PatternEntry with zero evidence (never-invent invariant).
//  5. Construct ConventionProfile via NewConventionProfile.
//
// Returns a degraded profile (empty Patterns) for unknown/unsupported frameworks
// rather than an error. Only genuine FS failures surface as errors.
func (e *Extractor) Extract(ctx context.Context, repoRoot string, sc detector.StructuralContext) (*convention.ConventionProfile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Step 1: load curated skills (raw form carries explicit-key flag).
	curatedRaw := loadCuratedSkillsRaw(repoRoot)

	// Step 2: framework-specific detection.
	var detected []convention.PatternEntry
	framework, version := primaryFramework(sc)
	switch strings.ToLower(framework) {
	case "nestjs":
		detected = detectNestJS(repoRoot)
	case "go":
		detected = detectGo(repoRoot)
	default:
		// Unknown or missing framework → degraded profile (zero detected patterns).
	}

	// Step 3: merge via source ladder.
	merged := applySourceLadder(curatedRaw, detected)

	// Step 4: drop zero-evidence patterns (never-invent invariant).
	var safe []convention.PatternEntry
	for _, pe := range merged {
		if len(pe.Evidence) > 0 {
			safe = append(safe, pe)
		}
	}

	// Step 5: construct domain aggregate.
	if framework == "" {
		framework = "unknown"
	}
	profile, err := convention.NewConventionProfile(
		e.projectID,
		framework,
		version,
		e.clock,
		safe,
	)
	if err != nil {
		return nil, err
	}
	return profile, nil
}

// primaryFramework extracts the first framework name and version from sc.
// Returns ("", "") when sc.Frameworks is empty.
func primaryFramework(sc detector.StructuralContext) (name, version string) {
	if len(sc.Frameworks) == 0 {
		return "", ""
	}
	fw := sc.Frameworks[0]
	return strings.ToLower(fw.Name), fw.Version
}

// applySourceLadder merges curated and detected patterns using the source-ladder
// precedence rule:
//
//	curated-skill (1.0) > detected-from-code (0.6–0.95) > baseline-framework-docs (0.5)
//
// Dedup behaviour depends on how the curated key was derived:
//
//   - Explicit-token key (e.g. "nestjs-extends-crudservice" found in file content):
//     The detected entry whose canonical key equals the curated key is suppressed.
//     The canonical key is the curated Pattern with any leading "curated-" prefix
//     stripped, so a curated file that names "nestjs-extends-crudservice" suppresses
//     a detected entry with Pattern="nestjs-extends-crudservice".
//
//   - Filename-fallback key (e.g. "curated-global", derived from the file stem):
//     The curated entry is emitted as-is (with its inferred-key Warning already set
//     by loadCuratedSkillsRaw). The detected entries are NOT suppressed — we cannot
//     assert sameness without an explicit token match (never-invent-honest behavior).
func applySourceLadder(curated []curatedEntry, detected []convention.PatternEntry) []convention.PatternEntry {
	// Build the covered set from explicit-token curated keys only.
	// We index by canonical key: strip a leading "curated-" prefix so that a
	// curated token "nestjs-extends-crudservice" matches the detected key of the
	// same name.
	covered := make(map[string]bool, len(curated))
	for _, ce := range curated {
		if ce.explicitKey {
			canonical := strings.TrimPrefix(ce.entry.Pattern, "curated-")
			covered[canonical] = true
		}
	}

	out := make([]convention.PatternEntry, 0, len(curated)+len(detected))
	for _, ce := range curated {
		out = append(out, ce.entry)
	}

	for _, pe := range detected {
		canonical := strings.TrimPrefix(pe.Pattern, "curated-")
		if !covered[canonical] {
			out = append(out, pe)
		}
	}
	return out
}

// readFileBytes reads path using os.ReadFile. Returns (nil, error) on failure.
// This is the single os call boundary for file reading in this package — it
// keeps the package testable by replacing os.ReadFile only at this boundary.
func readFileBytes(path string) ([]byte, error) {
	return os.ReadFile(path) // #nosec G304 -- path comes from WalkDir, never user input
}
