package extractor

// curated_loader.go — loads convention patterns from .claude/skills/ files.
//
// Curated patterns have source=curated-skill and confidence=1.0. They take
// precedence over all detected-from-code patterns for the same pattern key
// (source ladder — see extractor.go).
//
// Detection criteria: a skills file is considered a blocking-rule source when
// it contains at least one line with a MUST or MUST NOT keyword (case-insensitive).
// Every qualifying file produces one PatternEntry; the Evidence slice contains
// the single path to that skills file.

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/convention"
)

var (
	reMustRule     = regexp.MustCompile(`(?i)\bMUST\b`)
	rePatternKeyIn = regexp.MustCompile(`\b([a-z][a-z0-9]+-(?:[a-z][a-z0-9-]+))\b`)
)

// loadCuratedSkills walks <repoRoot>/.claude/skills/ and emits a PatternEntry
// for each file that contains a blocking (MUST/MUST NOT) rule. Returns an empty
// (non-nil) slice when the directory is absent or contains no qualifying files.
//
// Pattern-key derivation (in order of preference):
//  1. If the file content explicitly names a known lowercase-hyphen pattern key
//     (e.g. "nestjs-extends-crudservice"), that key is used.
//  2. Otherwise the key is derived from the file name: curated-<stem>.
//
// One PatternEntry is emitted per qualifying file. Multiple pattern-key
// mentions in a single file still produce one entry (the first mentioned key).
func loadCuratedSkills(repoRoot string) []convention.PatternEntry {
	skillsDir := filepath.Join(repoRoot, ".claude", "skills")

	var entries []convention.PatternEntry

	_ = filepath.WalkDir(skillsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Directory does not exist or is unreadable — silently skip.
			return nil
		}
		if d.IsDir() {
			return nil
		}

		content, readErr := readFileBytes(path)
		if readErr != nil {
			return nil
		}

		if !reMustRule.Match(content) {
			return nil
		}

		// Derive a stable pattern key.
		patternKey := derivePatternKey(path, content)

		// Derive a human-readable rule from the file name.
		base := filepath.Base(path)
		rule := "Follow blocking conventions defined in " + base +
			". All MUST/MUST NOT rules in this file are non-negotiable."

		rel, _ := filepath.Rel(repoRoot, path)

		entries = append(entries, convention.PatternEntry{
			Pattern:    patternKey,
			Source:     convention.SourceCuratedSkill,
			Confidence: convention.ComputeConfidence(convention.SourceCuratedSkill, 1),
			Evidence:   []string{rel},
			Rule:       rule,
		})
		return nil
	})

	if entries == nil {
		return []convention.PatternEntry{}
	}
	return entries
}

// derivePatternKey extracts a lowercase-hyphen pattern key from a skills file.
// It prefers an explicit pattern key mentioned in the content (e.g.
// "nestjs-extends-crudservice") over a file-name-derived key. A candidate key
// from content must contain at least one hyphen and consist only of [a-z0-9-].
func derivePatternKey(path string, content []byte) string {
	// Scan content for a pattern-key-shaped token: lowercase-hyphen, ≥2 segments.
	matches := rePatternKeyIn.FindAllSubmatch(content, -1)
	for _, m := range matches {
		candidate := string(m[1])
		// Must have at least two hyphen-separated segments.
		parts := strings.Split(candidate, "-")
		if len(parts) >= 2 && isLowercaseHyphen(candidate) {
			return candidate
		}
	}

	// Fallback: derive from file stem.
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return "curated-" + strings.ToLower(strings.ReplaceAll(strings.TrimSuffix(base, ext), " ", "-"))
}

// isLowercaseHyphen reports whether s consists only of [a-z0-9-].
func isLowercaseHyphen(s string) bool {
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			return false
		}
	}
	return true
}
