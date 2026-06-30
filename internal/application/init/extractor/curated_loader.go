package extractor

// curated_loader.go — loads convention patterns from .claude/skills/ files.
//
// Curated patterns have source=curated-skill and confidence=1.0. They take
// precedence over all detected-from-code patterns for the same pattern key
// (source ladder — see extractor.go and applySourceLadder).
//
// Detection criteria: a skills file is considered a blocking-rule source when
// it contains at least one line with a MUST or MUST NOT keyword (case-insensitive).
// Every qualifying file produces one PatternEntry; the Evidence slice contains
// the single path to that skills file.
//
// Pattern-key derivation (in order of preference):
//  1. If the file content explicitly names a known lowercase-hyphen pattern key
//     (e.g. "nestjs-extends-crudservice"), that key is used AND the entry is
//     flagged as explicit so applySourceLadder can suppress the matching
//     detected-from-code entry.
//  2. Otherwise the key is derived from the file name: curated-<stem>. In this
//     fallback case the entry carries a Warning explaining the inferred key so
//     the caller can surface the ambiguity. The detected-from-code entry for any
//     conceptually-equivalent pattern is NOT suppressed (we can't assert sameness
//     without a token match — never-invent-honest behavior).

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

// curatedEntry is an internal carrier that pairs a PatternEntry with a flag
// indicating whether its pattern key was derived from an explicit token in the
// file content (true) or inferred from the filename (false).
type curatedEntry struct {
	entry       convention.PatternEntry
	explicitKey bool // true → content-derived token; false → filename fallback
}

// loadCuratedSkills walks <repoRoot>/.claude/skills/ and emits a PatternEntry
// for each file that contains a blocking (MUST/MUST NOT) rule. Returns an empty
// (non-nil) slice when the directory is absent or contains no qualifying files.
//
// One PatternEntry is emitted per qualifying file. Multiple pattern-key
// mentions in a single file still produce one entry (the first mentioned key).
//
// When the key is derived from the filename (fallback), the entry carries a
// Warning: "pattern key inferred from filename; may overlap a detected pattern
// — verify". Explicit-token entries carry no such warning.
func loadCuratedSkills(repoRoot string) []convention.PatternEntry {
	skillsDir := filepath.Join(repoRoot, ".claude", "skills")

	var raw []curatedEntry

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

		// Derive a stable pattern key, noting whether it came from content.
		patternKey, explicit := derivePatternKey(path, content)

		// Derive a human-readable rule from the file name.
		base := filepath.Base(path)
		rule := "Follow blocking conventions defined in " + base +
			". All MUST/MUST NOT rules in this file are non-negotiable."

		rel, _ := filepath.Rel(repoRoot, path)

		var warnings []string
		if !explicit {
			warnings = []string{
				"pattern key inferred from filename; may overlap a detected pattern — verify",
			}
		}

		raw = append(raw, curatedEntry{
			entry: convention.PatternEntry{
				Pattern:    patternKey,
				Source:     convention.SourceCuratedSkill,
				Confidence: convention.ComputeConfidence(convention.SourceCuratedSkill, 1),
				Evidence:   []string{rel},
				Rule:       rule,
				Warnings:   warnings,
			},
			explicitKey: explicit,
		})
		return nil
	})

	if raw == nil {
		return []convention.PatternEntry{}
	}

	out := make([]convention.PatternEntry, len(raw))
	for i, r := range raw {
		out[i] = r.entry
	}
	return out
}

// loadCuratedSkillsRaw is the internal variant that returns curatedEntry records
// so applySourceLadder can differentiate explicit-token keys from filename fallbacks.
func loadCuratedSkillsRaw(repoRoot string) []curatedEntry {
	skillsDir := filepath.Join(repoRoot, ".claude", "skills")

	var raw []curatedEntry

	_ = filepath.WalkDir(skillsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
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

		patternKey, explicit := derivePatternKey(path, content)

		base := filepath.Base(path)
		rule := "Follow blocking conventions defined in " + base +
			". All MUST/MUST NOT rules in this file are non-negotiable."

		rel, _ := filepath.Rel(repoRoot, path)

		var warnings []string
		if !explicit {
			warnings = []string{
				"pattern key inferred from filename; may overlap a detected pattern — verify",
			}
		}

		raw = append(raw, curatedEntry{
			entry: convention.PatternEntry{
				Pattern:    patternKey,
				Source:     convention.SourceCuratedSkill,
				Confidence: convention.ComputeConfidence(convention.SourceCuratedSkill, 1),
				Evidence:   []string{rel},
				Rule:       rule,
				Warnings:   warnings,
			},
			explicitKey: explicit,
		})
		return nil
	})

	return raw
}

// derivePatternKey extracts a lowercase-hyphen pattern key from a skills file.
// It returns (key, true) when an explicit token is found in the content, and
// (key, false) when the key falls back to the file stem.
//
// A candidate key from content must have at least two hyphen-separated segments
// and consist only of [a-z0-9-].
func derivePatternKey(path string, content []byte) (key string, explicit bool) {
	// Scan content for a pattern-key-shaped token: lowercase-hyphen, ≥2 segments.
	matches := rePatternKeyIn.FindAllSubmatch(content, -1)
	for _, m := range matches {
		candidate := string(m[1])
		parts := strings.Split(candidate, "-")
		if len(parts) >= 2 && isLowercaseHyphen(candidate) {
			return candidate, true
		}
	}

	// Fallback: derive from file stem.
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := strings.ToLower(strings.ReplaceAll(strings.TrimSuffix(base, ext), " ", "-"))
	return "curated-" + stem, false
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
