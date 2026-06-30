// Package extractor implements ProfileExtractor: it walks a target repo's
// source tree, runs framework-specific detection heuristics, loads curated
// skills, applies the source-ladder precedence rule, and constructs an
// evidence-based ConventionProfile via the domain aggregate constructor.
//
// The never-invent invariant is enforced here (and again by the domain
// constructor): any candidate PatternEntry with zero evidence is silently
// dropped before ConventionProfile is constructed.
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
	_ = ctx // reserved for future cancellation propagation

	// Step 1: load curated skills.
	curated := loadCuratedSkills(repoRoot)

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
	merged := applySourceLadder(curated, detected)

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

// applySourceLadder merges curated and detected patterns, ensuring that a
// curated-skill entry takes precedence over a detected-from-code entry for
// the same pattern key. The source-ladder order is:
//
//	curated-skill (1.0) > detected-from-code (0.6–0.95) > baseline-framework-docs (0.5)
//
// The function starts with all curated entries (highest precedence) and then
// appends detected entries whose pattern key is NOT already covered by a
// curated entry.
func applySourceLadder(curated, detected []convention.PatternEntry) []convention.PatternEntry {
	// Index curated keys for fast lookup.
	covered := make(map[string]bool, len(curated))
	for _, pe := range curated {
		covered[pe.Pattern] = true
	}

	out := make([]convention.PatternEntry, 0, len(curated)+len(detected))
	out = append(out, curated...)

	for _, pe := range detected {
		if !covered[pe.Pattern] {
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
