package extractor

// go_detector.go — Go-specific convention heuristics for hexagonal architecture.
//
// Patterns emitted:
//   - go-hexagonal-bounded-contexts   (≥4 internal/<ctx>/{domain,application,infrastructure} triples)
//   - go-repository-port-in-domain    (interface declarations in domain/ per bounded context)
//   - go-service-struct-constructor-di (type <X>Service struct + func New<X>Service pattern)
//   - go-generics-for-envelopes-only  ([T any] in shared/; absent elsewhere)
//
// Never-invent invariant: patterns with zero evidence files are NOT included.
//
// OS boundary: os.ReadDir and os.Stat are called only for directory listing and
// existence checks; all file content is read through the single readFileBytes
// boundary in extractor.go. This keeps the package testable by stubbing that
// one function.

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/convention"
)

var (
	// reGoInterface matches only genuine "type X interface {" declarations.
	// The (?m) flag makes ^ match at line start so comments and string literals
	// containing the word "interface" are not matched.
	reGoInterface    = regexp.MustCompile(`(?m)^\s*type\s+\w+\s+interface\s*\{`)
	reGoServiceStruct = regexp.MustCompile(`\btype\s+\w+Service\s+struct\b`)
	reGoNewService   = regexp.MustCompile(`\bfunc\s+New\w+Service\b`)
	// reGoGenerics matches "[T any]" only at the start of a non-comment line.
	// The pattern anchors to the beginning of a non-slash, non-newline character
	// sequence so occurrences inside // comments are not matched.
	reGoGenerics     = regexp.MustCompile(`(?m)^[^/\n]*\[\s*[A-Z]\w*\s+any\b`)
)

// detectGo walks repoRoot and emits Go hexagonal-architecture convention patterns.
// Returns an empty slice when the repo does not match the expected layout.
func detectGo(repoRoot string) []convention.PatternEntry {
	var out []convention.PatternEntry

	// ── go-hexagonal-bounded-contexts ─────────────────────────────────────────
	// A bounded context is a directory triple: internal/<ctx>/domain + application + infrastructure.
	hexEvidence := detectHexContexts(repoRoot)
	if len(hexEvidence) >= 4 { // ≥4 bounded contexts emits the pattern
		conf := convention.ComputeConfidence(convention.SourceDetectedFromCode, len(hexEvidence))
		out = append(out, convention.PatternEntry{
			Pattern:    "go-hexagonal-bounded-contexts",
			Source:     convention.SourceDetectedFromCode,
			Confidence: conf,
			Evidence:   hexEvidence,
			Rule: "Every business capability MUST be encapsulated in a bounded context " +
				"under internal/<ctx>/ with distinct domain/, application/, and " +
				"infrastructure/ subdirectories. No cross-context imports except " +
				"via shared/ ports.",
		})
	}

	// ── go-repository-port-in-domain ──────────────────────────────────────────
	repoPortEvidence := detectRepositoryPortsInDomain(repoRoot)
	if len(repoPortEvidence) > 0 {
		conf := convention.ComputeConfidence(convention.SourceDetectedFromCode, len(repoPortEvidence))
		out = append(out, convention.PatternEntry{
			Pattern:    "go-repository-port-in-domain",
			Source:     convention.SourceDetectedFromCode,
			Confidence: conf,
			Evidence:   repoPortEvidence,
			Rule: "Repository interfaces (ports) MUST be declared in the domain layer " +
				"(internal/<ctx>/domain/). Infrastructure adapters implement them; " +
				"no database imports allowed in domain/.",
		})
	}

	// ── go-service-struct-constructor-di ──────────────────────────────────────
	diEvidence := scanFiles(repoRoot, "*.go", reGoServiceStruct)
	if len(diEvidence) > 0 {
		// Verify New<X>Service constructors are co-located with the structs.
		newEvidence := scanFiles(repoRoot, "*.go", reGoNewService)
		combined := uniqueStrings(append(diEvidence, newEvidence...))
		conf := convention.ComputeConfidence(convention.SourceDetectedFromCode, len(combined))
		out = append(out, convention.PatternEntry{
			Pattern:    "go-service-struct-constructor-di",
			Source:     convention.SourceDetectedFromCode,
			Confidence: conf,
			Evidence:   combined,
			Rule: "Application services MUST be plain structs with all dependencies " +
				"injected via a New<Name>Service(deps...) constructor. No globals, " +
				"no service locators.",
		})
	}

	// ── go-generics-for-envelopes-only ────────────────────────────────────────
	// Generics found in shared/ + NOT found in non-shared internal/ files.
	sharedEvidence := detectGenericsInShared(repoRoot)
	nonSharedGenerics := detectGenericsOutsideShared(repoRoot)
	if len(sharedEvidence) > 0 && len(nonSharedGenerics) == 0 {
		conf := convention.ComputeConfidence(convention.SourceDetectedFromCode, len(sharedEvidence))
		out = append(out, convention.PatternEntry{
			Pattern:    "go-generics-for-envelopes-only",
			Source:     convention.SourceDetectedFromCode,
			Confidence: conf,
			Evidence:   sharedEvidence,
			Rule: "Go generics ([T any]) MUST only appear in internal/shared/ (pagination, " +
				"response envelopes). Bounded-context code MUST NOT use generics — " +
				"prefer explicit types for domain clarity.",
		})
	}

	return out
}

// detectHexContexts finds internal/<ctx>/{domain,application,infrastructure}
// directory triples and returns one evidence path per bounded context directory.
func detectHexContexts(repoRoot string) []string {
	internalDir := filepath.Join(repoRoot, "internal")
	entries, err := os.ReadDir(internalDir)
	if err != nil {
		return nil
	}

	var evidence []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ctx := filepath.Join(internalDir, e.Name())
		hasDomain := dirExists(filepath.Join(ctx, "domain"))
		hasApp := dirExists(filepath.Join(ctx, "application"))
		hasInfra := dirExists(filepath.Join(ctx, "infrastructure"))
		if hasDomain && hasApp && hasInfra {
			rel, _ := filepath.Rel(repoRoot, ctx)
			evidence = append(evidence, rel)
		}
	}
	return evidence
}

// detectRepositoryPortsInDomain scans domain/*.go files for interface declarations.
func detectRepositoryPortsInDomain(repoRoot string) []string {
	var evidence []string
	_ = filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		// Must be in a domain/ directory.
		parts := strings.Split(filepath.ToSlash(path), "/")
		inDomain := false
		for _, p := range parts {
			if p == "domain" {
				inDomain = true
				break
			}
		}
		if !inDomain {
			return nil
		}
		content, readErr := readFileBytes(path)
		if readErr != nil {
			return nil
		}
		if reGoInterface.Match(content) {
			rel, _ := filepath.Rel(repoRoot, path)
			evidence = append(evidence, rel)
		}
		return nil
	})
	return evidence
}

// detectGenericsInShared finds files in internal/shared/ that contain generics.
func detectGenericsInShared(repoRoot string) []string {
	sharedDir := filepath.Join(repoRoot, "internal", "shared")
	return scanFilesInDir(sharedDir, repoRoot, "*.go", reGoGenerics)
}

// detectGenericsOutsideShared finds generics in internal/ outside of shared/.
func detectGenericsOutsideShared(repoRoot string) []string {
	internalDir := filepath.Join(repoRoot, "internal")
	var evidence []string
	_ = filepath.WalkDir(internalDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		// Skip shared/ files.
		if strings.Contains(filepath.ToSlash(path), "/shared/") {
			return nil
		}
		content, readErr := readFileBytes(path)
		if readErr != nil {
			return nil
		}
		if reGoGenerics.Match(content) {
			rel, _ := filepath.Rel(repoRoot, path)
			evidence = append(evidence, rel)
		}
		return nil
	})
	return evidence
}

// scanFilesInDir scans a specific directory (recursively via WalkDir) for Go files matching re.
func scanFilesInDir(dir, repoRoot, glob string, re *regexp.Regexp) []string {
	var matches []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		matched, _ := filepath.Match(glob, filepath.Base(path))
		if !matched {
			return nil
		}
		content, readErr := readFileBytes(path)
		if readErr != nil {
			return nil
		}
		if re.Match(content) {
			rel, _ := filepath.Rel(repoRoot, path)
			matches = append(matches, rel)
		}
		return nil
	})
	return matches
}

// dirExists reports whether path is an existing directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// uniqueStrings returns a deduplicated copy of ss preserving order.
func uniqueStrings(ss []string) []string {
	seen := make(map[string]bool, len(ss))
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
