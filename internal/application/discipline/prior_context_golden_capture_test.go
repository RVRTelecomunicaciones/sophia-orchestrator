package discipline_test

// prior_context_golden_capture_test.go — Group A baseline capture.
//
// This file drives the 12 PriorContext fixture scenarios through the CURRENT
// inline-concat logic so golden files reflect the pre-refactor byte sequence.
// Run with GOLDEN_UPDATE=1 to (re-)write fixtures:
//
//	GOLDEN_UPDATE=1 go test ./internal/application/discipline/... \
//	    -run TestPriorContextGoldenCapture -count=1
//
// After Group A is committed, this file is kept as documentation of what the
// baseline looked like before the struct refactor (M0.5).  It does NOT use
// discipline.PriorContext (which does not exist yet when these goldens are
// captured).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// priorContextDir is the subdirectory under testdata/ for PriorContext goldens.
const priorContextDir = "priorcontext"

// setupPriorContextGoldenDir ensures testdata/priorcontext/ exists.
func setupPriorContextGoldenDir(t *testing.T) {
	t.Helper()
	require := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("setupPriorContextGoldenDir: %v", err)
		}
	}
	require(os.MkdirAll(filepath.Join("testdata", priorContextDir), 0o755))
}

// writeGoldenSub writes content to testdata/priorcontext/<name>.
// Reuses goldenPath+writeGolden convention; the subdir must already exist.
func writeGoldenSub(t *testing.T, name, content string) {
	t.Helper()
	writeGolden(t, filepath.Join(priorContextDir, name), content)
}

// readGoldenSub reads testdata/priorcontext/<name>.
func readGoldenSub(t *testing.T, name string) string {
	t.Helper()
	return readGolden(t, filepath.Join(priorContextDir, name))
}

// ---------------------------------------------------------------------------
// Inline-concat helpers — mirror of current production logic BEFORE refactor.
// These are the reference implementations used to capture byte-exact baselines.
// ---------------------------------------------------------------------------

// phaseServiceInlineConcat mirrors buildPriorContext's strings.Builder loop.
// Input: slice of record content strings.
// Returns "" when the slice is nil (simulates error/nil-bundle path).
func phaseServiceInlineConcat(records []string) string {
	if records == nil {
		return ""
	}
	var sb strings.Builder
	for _, rec := range records {
		sb.WriteString(rec)
		sb.WriteString("\n\n")
	}
	return sb.String()
}

// applyLoadInlineConcat mirrors loadPriorContext's section-assembly path.
// phaseKey is "spec" or "design"; changeName is a frozen fixture name.
// Returns "" when sections is empty.
func applyLoadInlineConcat(changeName string, sections []struct{ key, content string }) string {
	if len(sections) == 0 {
		return ""
	}
	formatted := make([]string, 0, len(sections))
	for _, s := range sections {
		formatted = append(formatted,
			fmt.Sprintf("## %s (sdd/%s/%s)\n\n%s", s.key, changeName, s.key, s.content))
	}
	out := formatted[0]
	for _, s := range formatted[1:] {
		out += "\n\n" + s
	}
	return out
}

// applyRefreshInlineConcat mirrors refreshApplyProgress's concat path.
// base is the output of applyLoadInlineConcat. Returns base on empty progress.
func applyRefreshInlineConcat(changeName, base, progressContent string) string {
	if progressContent == "" {
		return base
	}
	section := fmt.Sprintf("## Recent progress (sdd/%s/apply-progress)\n\n%s",
		changeName, progressContent)
	if base == "" {
		return section
	}
	return base + "\n\n" + section
}

// ---------------------------------------------------------------------------
// Frozen fixture data (deterministic — no Clock, no IDGenerator needed).
// ---------------------------------------------------------------------------

const (
	fixtureChangeName = "feat-prior-context-fixture"

	fixtureSpecContent    = "Spec content: introduce PriorContext struct for structured assembly of prior-context content."
	fixtureDesignContent  = "Design content: single file prior_context.go with Render method and 8 stub types."
	fixtureProgressContent = "Progress: Group A baseline capture complete. Group B struct implementation in progress."

	fixtureRecord1 = "fix flaky test in apply phase — root cause: race condition in goroutine fan-out"
	fixtureRecord2 = "heuristic: always freeze Clock and IDGenerator in golden tests to ensure byte-exact reproducibility"
	fixtureRecord3 = "decision: PriorContext struct in discipline package, render-at-boundary preserves downstream signatures"
	fixtureRecord4 = "pattern: use strings.Builder for inline-concat paths to minimize allocations"
	fixtureRecord5 = "discovery: golangci-lint v2.12 enforces wrapcheck on all error returns from outbound ports"
	fixtureUnicode = "café ☕ 日本語テスト — unicode memory record preserved byte-exact through assembly"
)

// ---------------------------------------------------------------------------
// TestPriorContextGoldenCapture — Group A baseline capture.
// ---------------------------------------------------------------------------

// TestPriorContextGoldenCapture captures the 12 golden baselines from the
// CURRENT inline-concat logic. Run with GOLDEN_UPDATE=1 to write files;
// run without to verify files are byte-exact (used in Group C read-back check).
func TestPriorContextGoldenCapture(t *testing.T) {
	setupPriorContextGoldenDir(t)

	cases := []struct {
		name   string
		output string
	}{
		// --- Phase-service path (5 cases) ---

		{
			name: "phase_empty_memory_bundle.golden.txt",
			// nil records → error/nil-bundle path → ""
			output: phaseServiceInlineConcat(nil),
		},
		{
			name:   "phase_single_memory_record.golden.txt",
			output: phaseServiceInlineConcat([]string{fixtureRecord1}),
		},
		{
			name: "phase_multi_memory_record.golden.txt",
			output: phaseServiceInlineConcat([]string{
				fixtureRecord1,
				fixtureRecord2,
				fixtureRecord3,
				fixtureRecord4,
				fixtureRecord5,
			}),
		},
		{
			name:   "phase_memory_with_unicode.golden.txt",
			output: phaseServiceInlineConcat([]string{fixtureUnicode}),
		},
		{
			name: "phase_memory_error_returns_empty.golden.txt",
			// Error path: nil → ""
			output: phaseServiceInlineConcat(nil),
		},

		// --- Apply path (7 cases) ---

		{
			name: "apply_spec_only.golden.txt",
			output: applyLoadInlineConcat(fixtureChangeName, []struct{ key, content string }{
				{key: "spec", content: fixtureSpecContent},
			}),
		},
		{
			name: "apply_design_only.golden.txt",
			output: applyLoadInlineConcat(fixtureChangeName, []struct{ key, content string }{
				{key: "design", content: fixtureDesignContent},
			}),
		},
		{
			name: "apply_both_no_progress.golden.txt",
			output: applyLoadInlineConcat(fixtureChangeName, []struct{ key, content string }{
				{key: "spec", content: fixtureSpecContent},
				{key: "design", content: fixtureDesignContent},
			}),
		},
		{
			name: "apply_full_three_sections.golden.txt",
			output: func() string {
				base := applyLoadInlineConcat(fixtureChangeName, []struct{ key, content string }{
					{key: "spec", content: fixtureSpecContent},
					{key: "design", content: fixtureDesignContent},
				})
				return applyRefreshInlineConcat(fixtureChangeName, base, fixtureProgressContent)
			}(),
		},
		{
			name: "apply_progress_refresh.golden.txt",
			// refreshApplyProgress with non-empty base and non-empty progress.
			output: func() string {
				base := applyLoadInlineConcat(fixtureChangeName, []struct{ key, content string }{
					{key: "spec", content: fixtureSpecContent},
				})
				return applyRefreshInlineConcat(fixtureChangeName, base, fixtureProgressContent)
			}(),
		},
		{
			name: "apply_progress_error_fail_soft.golden.txt",
			// refreshApplyProgress error path: progress "" → return base unchanged.
			output: func() string {
				base := applyLoadInlineConcat(fixtureChangeName, []struct{ key, content string }{
					{key: "spec", content: fixtureSpecContent},
					{key: "design", content: fixtureDesignContent},
				})
				return applyRefreshInlineConcat(fixtureChangeName, base, "" /*error → empty*/)
			}(),
		},
		{
			name: "apply_empty_returns_empty.golden.txt",
			// Both topics absent → ""
			output: applyLoadInlineConcat(fixtureChangeName, nil),
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if updateGolden() {
				writeGoldenSub(t, tc.name, tc.output)
				t.Logf("wrote golden: testdata/%s/%s", priorContextDir, tc.name)
				return
			}
			// Without GOLDEN_UPDATE: verify the file exists and matches.
			got := readGoldenSub(t, tc.name)
			if got != tc.output {
				t.Errorf("golden mismatch for %s:\n  want: %q\n   got: %q",
					tc.name, tc.output, got)
			}
		})
	}
}
