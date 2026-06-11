package skill_test

// semver_test.go — T3.1 RED: domain semver helpers MajorOf and DriftsForward.
//
// Test layer: unit, pure functions, no I/O.
// RED — functions do not exist until T3.2 GREEN adds semver.go.

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
)

// ── MajorOf — table-driven ────────────────────────────────────────────────────

func TestMajorOf(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input    string
		wantMaj  int
		wantOK   bool
		desc     string
	}{
		// Canonical semver
		{"22.0.0", 22, true, "plain semver major=22"},
		{"3.2.1", 3, true, "plain semver major=3"},
		{"0.9.0", 0, true, "zero major semver"},

		// Go version evidence ("go M.m")
		{"go 1.26", 1, true, "go lang prefix"},
		{"go 1.21.0", 1, true, "go lang prefix with patch"},

		// Caret prefix
		{"^18", 18, true, "caret prefix only"},
		{"^18.0.0", 18, true, "caret prefix with semver"},

		// v-prefix
		{"v3.2", 3, true, "v-prefix"},
		{"v3.2.1", 3, true, "v-prefix with patch"},

		// >= prefix
		{">=22.0.0", 22, true, "gte prefix"},
		{">=2", 2, true, "gte prefix integer only"},

		// Other operator prefixes
		{"~1.2.3", 1, true, "tilde prefix"},
		{"<10", 10, true, "lt prefix single integer"},

		// Unparseable → false
		{"", 0, false, "empty string"},
		{"edge", 0, false, "non-numeric string"},
		{"latest", 0, false, "symbolic tag"},
		{"go", 0, false, "go keyword without version"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()
			got, ok := skill.MajorOf(tc.input)
			assert.Equal(t, tc.wantOK, ok, "ok mismatch for input %q", tc.input)
			if tc.wantOK {
				assert.Equal(t, tc.wantMaj, got, "major mismatch for input %q", tc.input)
			}
		})
	}
}

// ── DriftsForward — table-driven ─────────────────────────────────────────────

func TestDriftsForward(t *testing.T) {
	t.Parallel()

	cases := []struct {
		detected string
		activeMin string
		want      bool
		desc      string
	}{
		// Detected strictly newer major → drifts
		{"23.0.0", "22", true, "major jump 22→23"},
		{"23.0.0", "22.0.0", true, "major jump with full semver activeMin"},
		{"2.0.0", "1.5.0", true, "major jump 1→2"},

		// Same major → no drift
		{"22.3.1", "22.0.0", false, "same major, minor differs"},
		{"22.0.0", "22.0.0", false, "exact same version"},
		{"1.26.0", "1.0.0", false, "same major 1, minor differs"},

		// Unparseable detected → never drifts (fail-safe)
		{"edge", "22", false, "unparseable detected"},
		{"", "22", false, "empty detected"},

		// Unparseable activeMin → never drifts (fail-safe)
		{"23.0.0", "edge", false, "unparseable activeMin"},
		{"23.0.0", "", false, "empty activeMin"},

		// Both unparseable
		{"edge", "latest", false, "both unparseable"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()
			got := skill.DriftsForward(tc.detected, tc.activeMin)
			assert.Equal(t, tc.want, got, "DriftsForward(%q, %q)", tc.detected, tc.activeMin)
		})
	}
}
