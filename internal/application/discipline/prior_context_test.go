package discipline_test

// prior_context_test.go — Group B (RED tests) and Group C (snapshot tests)
//
// TDD cycle: tests written FIRST against discipline.PriorContext which does
// not exist yet (prior_context.go absent). All tests in this file fail RED
// until B.9-B.10 implement the struct and Render method.

import (
	"encoding/json"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// B.1 — RenderOpts zero-value is a no-op (empty struct returns "")
// ---------------------------------------------------------------------------

func TestRenderOpts_ZeroValue_IsNoOp(t *testing.T) {
	got := discipline.PriorContext{}.Render(discipline.RenderOpts{})
	require.Equal(t, "", got,
		"RenderOpts{} MUST be a no-op: empty PriorContext must return empty string")
}

// ---------------------------------------------------------------------------
// B.2 — RawMemoryBlob-only path returns blob verbatim
// ---------------------------------------------------------------------------

func TestRender_RawMemoryBlob_Only(t *testing.T) {
	pc := discipline.PriorContext{RawMemoryBlob: "x"}
	got := pc.Render(discipline.RenderOpts{})
	require.Equal(t, "x", got,
		"RawMemoryBlob-only path must return the blob string byte-exact")
}

// ---------------------------------------------------------------------------
// B.3 — PhaseIdentity-only path returns identity verbatim
// ---------------------------------------------------------------------------

func TestRender_PhaseIdentity_Only(t *testing.T) {
	pc := discipline.PriorContext{PhaseIdentity: "y"}
	got := pc.Render(discipline.RenderOpts{})
	require.Equal(t, "y", got,
		"PhaseIdentity-only path must return the identity string byte-exact")
}

// ---------------------------------------------------------------------------
// B.4 — Deterministic field order: PhaseIdentity first, then RawMemoryBlob
// ---------------------------------------------------------------------------

func TestRender_DeterministicOrder_PhaseIdentityBeforeRawBlob(t *testing.T) {
	pc := discipline.PriorContext{
		PhaseIdentity: "y",
		RawMemoryBlob: "x",
	}
	got := pc.Render(discipline.RenderOpts{})
	require.Equal(t, "yx", got,
		"Render MUST emit PhaseIdentity first, then RawMemoryBlob (field order deterministic)")
}

// ---------------------------------------------------------------------------
// B.5 — TokenBudget=0 is a no-op (same output as unlimited)
// ---------------------------------------------------------------------------

func TestRender_TokenBudget_Zero_IsNoOp(t *testing.T) {
	control := "hello world"
	pc := discipline.PriorContext{RawMemoryBlob: control}
	withZero := pc.Render(discipline.RenderOpts{TokenBudget: 0})
	withUnlimited := pc.Render(discipline.RenderOpts{})
	require.Equal(t, withUnlimited, withZero,
		"TokenBudget=0 must be a no-op identical to unlimited budget")
}

// ---------------------------------------------------------------------------
// B.6 — TokenBudget=5 truncates output to 5 bytes
// ---------------------------------------------------------------------------

func TestRender_TokenBudget_Truncates(t *testing.T) {
	pc := discipline.PriorContext{RawMemoryBlob: "hello world"}
	got := pc.Render(discipline.RenderOpts{TokenBudget: 5})
	require.Equal(t, "hello", got,
		"TokenBudget=5 must truncate output to the first 5 bytes")
}

// ---------------------------------------------------------------------------
// B.7 — EnableAttribution=false is a no-op (Render is pass-through in M0.5)
// ---------------------------------------------------------------------------

func TestRender_EnableAttribution_False_IsNoOp(t *testing.T) {
	// Section headers live INSIDE the field values, not added by Render.
	// Render is pass-through; EnableAttribution=false must not inject ## headers.
	content := "## spec (sdd/x/spec)\n\nFOO"
	pc := discipline.PriorContext{PhaseIdentity: content}
	got := pc.Render(discipline.RenderOpts{EnableAttribution: false})
	require.Equal(t, content, got,
		"EnableAttribution=false must not inject any additional ## headers — Render is pass-through in M0.5")
}

// ---------------------------------------------------------------------------
// B.8 — All 9 struct fields JSON round-trip; 8 stub types produce {} in JSON
// ---------------------------------------------------------------------------

func TestPriorContext_JSON_RoundTrip(t *testing.T) {
	// Construct a PriorContext with non-zero values for all reachable fields.
	original := discipline.PriorContext{
		PhaseIdentity:   "phase-id",
		Skills:          []discipline.RenderedSkill{{}},
		StructuralCtx:   &discipline.StructuralContextRef{},
		Episodes:        []discipline.EpisodeRef{{}},
		ChangeDigests:   []discipline.ChangeDigestRef{{}},
		BusinessRules:   []discipline.RuleRef{{}},
		Routines:        []discipline.RoutineOutput{{}},
		AuxiliaryMemory: &discipline.AuxiliaryBlock{},
		RawMemoryBlob:   "raw-blob",
	}

	data, err := json.Marshal(original)
	require.NoError(t, err, "PriorContext must be JSON-serializable")

	var decoded discipline.PriorContext
	require.NoError(t, json.Unmarshal(data, &decoded), "PriorContext must be JSON-deserializable")

	require.Equal(t, original.PhaseIdentity, decoded.PhaseIdentity)
	require.Equal(t, original.RawMemoryBlob, decoded.RawMemoryBlob)
	require.Equal(t, original.Skills, decoded.Skills)
	require.Equal(t, original.Episodes, decoded.Episodes)
	require.Equal(t, original.ChangeDigests, decoded.ChangeDigests)
	require.Equal(t, original.BusinessRules, decoded.BusinessRules)
	require.Equal(t, original.Routines, decoded.Routines)

	// Stub types must round-trip as {} in JSON.
	renderedSkillJSON, err := json.Marshal(discipline.RenderedSkill{})
	require.NoError(t, err)
	require.Equal(t, "{}", string(renderedSkillJSON), "RenderedSkill{} must marshal to {}")

	structuralCtxJSON, err := json.Marshal(discipline.StructuralContextRef{})
	require.NoError(t, err)
	require.Equal(t, "{}", string(structuralCtxJSON), "StructuralContextRef{} must marshal to {}")

	episodeJSON, err := json.Marshal(discipline.EpisodeRef{})
	require.NoError(t, err)
	require.Equal(t, "{}", string(episodeJSON), "EpisodeRef{} must marshal to {}")

	changeDigestJSON, err := json.Marshal(discipline.ChangeDigestRef{})
	require.NoError(t, err)
	require.Equal(t, "{}", string(changeDigestJSON), "ChangeDigestRef{} must marshal to {}")

	ruleRefJSON, err := json.Marshal(discipline.RuleRef{})
	require.NoError(t, err)
	require.Equal(t, "{}", string(ruleRefJSON), "RuleRef{} must marshal to {}")

	routineJSON, err := json.Marshal(discipline.RoutineOutput{})
	require.NoError(t, err)
	require.Equal(t, "{}", string(routineJSON), "RoutineOutput{} must marshal to {}")

	auxBlockJSON, err := json.Marshal(discipline.AuxiliaryBlock{})
	require.NoError(t, err)
	require.Equal(t, "{}", string(auxBlockJSON), "AuxiliaryBlock{} must marshal to {}")
}

// ---------------------------------------------------------------------------
// Group C — Snapshot tests with goldens
// C.1-C.5: 12 table-driven cases, byte-exact match against baselines from A
// ---------------------------------------------------------------------------

// Fixture data mirroring prior_context_golden_capture_test.go constants.
// These must stay byte-exact to what was captured in Group A.
const (
	snapshotChangeName      = "feat-prior-context-fixture"
	snapshotSpecContent     = "Spec content: introduce PriorContext struct for structured assembly of prior-context content."
	snapshotDesignContent   = "Design content: single file prior_context.go with Render method and 8 stub types."
	snapshotProgressContent = "Progress: Group A baseline capture complete. Group B struct implementation in progress."

	snapshotRecord1 = "fix flaky test in apply phase — root cause: race condition in goroutine fan-out"
	snapshotRecord2 = "heuristic: always freeze Clock and IDGenerator in golden tests to ensure byte-exact reproducibility"
	snapshotRecord3 = "decision: PriorContext struct in discipline package, render-at-boundary preserves downstream signatures"
	snapshotRecord4 = "pattern: use strings.Builder for inline-concat paths to minimize allocations"
	snapshotRecord5 = "discovery: golangci-lint v2.12 enforces wrapcheck on all error returns from outbound ports"
	snapshotUnicode = "café ☕ 日本語テスト — unicode memory record preserved byte-exact through assembly"
)

// snapshotMultiRecord builds the multi-record fixture blob exactly as the
// golden-capture helper phaseServiceInlineConcat does (verbatim).
func snapshotMultiRecord() string {
	records := []string{snapshotRecord1, snapshotRecord2, snapshotRecord3, snapshotRecord4, snapshotRecord5}
	var b []byte
	for _, r := range records {
		b = append(b, []byte(r)...)
		b = append(b, []byte("\n\n")...)
	}
	return string(b)
}

// snapshotApplyIdentity assembles the PhaseIdentity string for apply cases,
// matching applyLoadInlineConcat from the golden capture helper.
func snapshotApplyIdentity(sections []struct{ key, content string }) string {
	if len(sections) == 0 {
		return ""
	}
	formatted := make([]string, 0, len(sections))
	for _, s := range sections {
		formatted = append(formatted,
			"## "+s.key+" (sdd/"+snapshotChangeName+"/"+s.key+")\n\n"+s.content)
	}
	out := formatted[0]
	for _, s := range formatted[1:] {
		out += "\n\n" + s
	}
	return out
}

// snapshotApplyRefresh mirrors applyRefreshInlineConcat.
func snapshotApplyRefresh(base, progressContent string) string {
	if progressContent == "" {
		return base
	}
	section := "## Recent progress (sdd/" + snapshotChangeName + "/apply-progress)\n\n" + progressContent
	if base == "" {
		return section
	}
	return base + "\n\n" + section
}

// TestPriorContext_Render_Goldens is the Group C snapshot test.
// All 12 cases must produce byte-exact output matching the pre-refactor baselines.
//
// C.3: 5 phase-service cases
// C.4: 7 apply cases
// C.5: All 12 must be green — STOP and report on any mismatch.
func TestPriorContext_Render_Goldens(t *testing.T) {
	specOnly := snapshotApplyIdentity([]struct{ key, content string }{
		{key: "spec", content: snapshotSpecContent},
	})
	designOnly := snapshotApplyIdentity([]struct{ key, content string }{
		{key: "design", content: snapshotDesignContent},
	})
	bothNoProgress := snapshotApplyIdentity([]struct{ key, content string }{
		{key: "spec", content: snapshotSpecContent},
		{key: "design", content: snapshotDesignContent},
	})
	fullThreeSections := snapshotApplyRefresh(bothNoProgress, snapshotProgressContent)
	progressRefresh := snapshotApplyRefresh(specOnly, snapshotProgressContent)
	errorFailSoft := bothNoProgress // error → keep base unchanged

	cases := []struct {
		name string
		pc   discipline.PriorContext
		opts discipline.RenderOpts
	}{
		// --- C.3: 5 phase-service cases ---
		{
			name: "phase_empty_memory_bundle",
			pc:   discipline.PriorContext{},
			opts: discipline.RenderOpts{},
		},
		{
			name: "phase_single_memory_record",
			pc:   discipline.PriorContext{RawMemoryBlob: snapshotRecord1 + "\n\n"},
			opts: discipline.RenderOpts{},
		},
		{
			name: "phase_multi_memory_record",
			pc:   discipline.PriorContext{RawMemoryBlob: snapshotMultiRecord()},
			opts: discipline.RenderOpts{},
		},
		{
			name: "phase_memory_with_unicode",
			pc:   discipline.PriorContext{RawMemoryBlob: snapshotUnicode + "\n\n"},
			opts: discipline.RenderOpts{},
		},
		{
			name: "phase_memory_error_returns_empty",
			pc:   discipline.PriorContext{},
			opts: discipline.RenderOpts{},
		},

		// --- C.4: 7 apply cases ---
		{
			name: "apply_spec_only",
			pc:   discipline.PriorContext{PhaseIdentity: specOnly},
			opts: discipline.RenderOpts{},
		},
		{
			name: "apply_design_only",
			pc:   discipline.PriorContext{PhaseIdentity: designOnly},
			opts: discipline.RenderOpts{},
		},
		{
			name: "apply_both_no_progress",
			pc:   discipline.PriorContext{PhaseIdentity: bothNoProgress},
			opts: discipline.RenderOpts{},
		},
		{
			name: "apply_full_three_sections",
			pc:   discipline.PriorContext{PhaseIdentity: fullThreeSections},
			opts: discipline.RenderOpts{},
		},
		{
			name: "apply_progress_refresh",
			pc:   discipline.PriorContext{PhaseIdentity: progressRefresh},
			opts: discipline.RenderOpts{},
		},
		{
			name: "apply_progress_error_fail_soft",
			pc:   discipline.PriorContext{PhaseIdentity: errorFailSoft},
			opts: discipline.RenderOpts{},
		},
		{
			name: "apply_empty_returns_empty",
			pc:   discipline.PriorContext{},
			opts: discipline.RenderOpts{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.pc.Render(tc.opts)
			if updateGolden() {
				writeGolden(t, "priorcontext/"+tc.name+".golden.txt", got)
				return
			}
			want := readGolden(t, "priorcontext/"+tc.name+".golden.txt")
			require.Equal(t, want, got,
				"BYTE-EXACT GOLDEN MISMATCH for %s — Render output does not match pre-refactor baseline. Do NOT regenerate goldens.", tc.name)
		})
	}
}

// ---------------------------------------------------------------------------
// Additional determinism test (design spec requirement D-M05-7)
// ---------------------------------------------------------------------------

func TestRender_Deterministic(t *testing.T) {
	pc := discipline.PriorContext{PhaseIdentity: "X", RawMemoryBlob: "Y"}
	first := pc.Render(discipline.RenderOpts{})
	for i := 0; i < 100; i++ {
		require.Equal(t, first, pc.Render(discipline.RenderOpts{}),
			"Render must be deterministic — same output on every call (iteration %d)", i)
	}
}

// ---------------------------------------------------------------------------
// Operator decision #9: RenderOpts{} zero-value no-op for apply 3-section case
// ---------------------------------------------------------------------------

func TestRenderOpts_ZeroValue_IsNoOp_ApplyCase(t *testing.T) {
	// Control: inline-concat output for a representative apply 3-section case.
	inline := "## spec (sdd/x/spec)\n\nFOO\n\n## design (sdd/x/design)\n\nBAR"
	pc := discipline.PriorContext{PhaseIdentity: inline}
	require.Equal(t, inline, pc.Render(discipline.RenderOpts{}),
		"RenderOpts{} MUST be a no-op (operator decision #9)")
}
