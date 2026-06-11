package skill

// semver.go — pure domain helpers for major-version extraction and drift
// detection (DG-C7-9). No external imports: stdlib only.
//
// Input tolerance (per design): accepts heterogeneous version strings such as
// "22.0.0", "go 1.26", "^18", "v3.2", ">=22.0.0". The algorithm:
//  1. Strip a leading non-digit word token ("go ", "node " etc.).
//  2. Trim leading operator/prefix chars (^~>=<v).
//  3. Read the leading integer run as the major version.

import (
	"strconv"
	"strings"
	"unicode"
)

// MajorOf extracts the major-version integer from a version string.
// It returns (major, true) on success, or (0, false) if the string cannot be
// parsed to a major integer (empty string, symbolic tag like "edge", etc.).
//
// Supported formats:
//   - Plain semver:     "22.0.0"  → 22
//   - Go version:       "go 1.26" → 1
//   - Caret/tilde:      "^18"     → 18
//   - v-prefix:         "v3.2"    → 3
//   - Operator prefix:  ">=22"    → 22
func MajorOf(version string) (int, bool) {
	s := strings.TrimSpace(version)
	if s == "" {
		return 0, false
	}

	// Step 1: strip a leading non-digit word token (e.g. "go ", "node ").
	// A word token ends at the first space; if the token contains no digits it
	// is considered a language/runtime qualifier and is discarded.
	if idx := strings.IndexByte(s, ' '); idx >= 0 {
		prefix := s[:idx]
		rest := strings.TrimSpace(s[idx+1:])
		// Only strip if prefix is entirely non-digit (qualifier word).
		if !containsDigit(prefix) && rest != "" {
			s = rest
		}
	}

	// Step 2: trim leading operator / prefix chars: ^~>=<v (case-insensitive v).
	s = strings.TrimLeftFunc(s, func(r rune) bool {
		return r == '^' || r == '~' || r == '>' || r == '=' || r == '<' || r == 'v' || r == 'V'
	})

	if s == "" {
		return 0, false
	}

	// Step 3: read the leading integer run (up to first non-digit character).
	end := 0
	for end < len(s) && unicode.IsDigit(rune(s[end])) {
		end++
	}
	if end == 0 {
		return 0, false
	}

	major, err := strconv.Atoi(s[:end])
	if err != nil {
		return 0, false
	}
	return major, true
}

// DriftsForward reports whether the detected version's major is strictly
// greater than the activeMin version's major.
//
// Returns false when either string cannot be parsed — unparseable versions
// are treated conservatively (never drifts) to avoid spurious bootstrap calls.
func DriftsForward(detected, activeMin string) bool {
	dMaj, dOK := MajorOf(detected)
	aMaj, aOK := MajorOf(activeMin)
	if !dOK || !aOK {
		return false
	}
	return dMaj > aMaj
}

// containsDigit returns true when s contains at least one ASCII digit.
func containsDigit(s string) bool {
	for _, r := range s {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
