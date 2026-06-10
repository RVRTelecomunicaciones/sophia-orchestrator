package discipline_test

// prior_context_test.go — Group B (RED tests) and Group C (snapshot tests)
// Extended for M3 PR3a Groups I and J.
//
// TDD cycle: tests written FIRST against discipline.PriorContext which does
// not exist yet (prior_context.go absent). All tests in this file fail RED
// until B.9-B.10 implement the struct and Render method.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/structural"
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
// B.3 — PhaseIdentity-only path returns identity verbatim
// ---------------------------------------------------------------------------

func TestRender_PhaseIdentity_Only(t *testing.T) {
	pc := discipline.PriorContext{PhaseIdentity: "y"}
	got := pc.Render(discipline.RenderOpts{})
	require.Equal(t, "y", got,
		"PhaseIdentity-only path must return the identity string byte-exact")
}

// ---------------------------------------------------------------------------
// B.5 — TokenBudget=0 is a no-op (same output as unlimited)
// ---------------------------------------------------------------------------

func TestRender_TokenBudget_Zero_IsNoOp(t *testing.T) {
	control := "hello world"
	pc := discipline.PriorContext{PhaseIdentity: control}
	withZero := pc.Render(discipline.RenderOpts{TokenBudget: 0})
	withUnlimited := pc.Render(discipline.RenderOpts{})
	require.Equal(t, withUnlimited, withZero,
		"TokenBudget=0 must be a no-op identical to unlimited budget")
}

// ---------------------------------------------------------------------------
// B.6 — TokenBudget truncates output and appends truncation marker (M3)
// ---------------------------------------------------------------------------

func TestRender_TokenBudget_Truncates(t *testing.T) {
	// M3: enforceBudget truncates at the budget boundary and appends a
	// truncation marker (deterministic text, no clock/random).
	// skills layer has share 0.40 → with budget=25, alloc=10 bytes.
	// content "hello world" (11 bytes) → first 10 kept, marker appended.
	pc := discipline.PriorContext{
		Skills: []discipline.RenderedSkill{
			{Name: "s", Version: "v1", Status: "active", Source: "manual", Content: "hello world"},
		},
	}
	got := pc.Render(discipline.RenderOpts{TokenBudget: 25})
	require.Contains(t, got, "hello",
		"truncated output must retain the start of skill content")
	require.Contains(t, got, "truncated",
		"TokenBudget truncation must append a truncation marker")
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
	// StructuralCtx is now *structural.StructuralContext (E.3 — domain move).
	sc := &structural.StructuralContext{
		SchemaVersion: structural.SchemaV1,
		ProjectID:     "proj-1",
		DetectedAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	original := discipline.PriorContext{
		PhaseIdentity:   "phase-id",
		Skills:          []discipline.RenderedSkill{{}},
		StructuralCtx:   sc,
		Episodes:        []discipline.EpisodeRef{{}},
		ChangeDigests:   []discipline.ChangeDigestRef{{}},
		BusinessRules:   []discipline.RuleRef{{}},
		Routines:        []discipline.RoutineOutput{{}},
		AuxiliaryMemory: &discipline.AuxiliaryBlock{},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err, "PriorContext must be JSON-serializable")

	var decoded discipline.PriorContext
	require.NoError(t, json.Unmarshal(data, &decoded), "PriorContext must be JSON-deserializable")

	require.Equal(t, original.PhaseIdentity, decoded.PhaseIdentity)
	require.Equal(t, original.Skills, decoded.Skills)
	require.Equal(t, original.Episodes, decoded.Episodes)
	require.Equal(t, original.ChangeDigests, decoded.ChangeDigests)
	require.Equal(t, original.BusinessRules, decoded.BusinessRules)
	require.Equal(t, original.Routines, decoded.Routines)
	require.NotNil(t, decoded.StructuralCtx, "StructuralCtx must round-trip non-nil")
	require.Equal(t, sc.ProjectID, decoded.StructuralCtx.ProjectID, "StructuralCtx.ProjectID must survive JSON round-trip")

	// M0.5 contract updated for M3: stub types now have real fields; zero-value
	// structs marshal to objects (empty string/slice fields may be omitempty).
	// The constraint is that JSON round-trip is lossless (tested above for each type).
	// RenderedSkill, EpisodeRef, ChangeDigestRef, RuleRef must all be JSON-serializable.
	renderedSkillJSON, err := json.Marshal(discipline.RenderedSkill{})
	require.NoError(t, err)
	require.NotEmpty(t, renderedSkillJSON, "RenderedSkill must be JSON-serializable")

	episodeJSON, err := json.Marshal(discipline.EpisodeRef{})
	require.NoError(t, err)
	require.NotEmpty(t, episodeJSON, "EpisodeRef must be JSON-serializable")

	changeDigestJSON, err := json.Marshal(discipline.ChangeDigestRef{})
	require.NoError(t, err)
	require.NotEmpty(t, changeDigestJSON, "ChangeDigestRef must be JSON-serializable")

	ruleRefJSON, err := json.Marshal(discipline.RuleRef{})
	require.NoError(t, err)
	require.NotEmpty(t, ruleRefJSON, "RuleRef must be JSON-serializable")

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
		// --- C.3: 5 phase-service cases (RawMemoryBlob retired in M3 PR3b;
		//          memory content now lives in typed layers Episodes/ChangeDigests/
		//          BusinessRules. Pass-through equivalence tested via PhaseIdentity.) ---
		{
			name: "phase_empty_memory_bundle",
			pc:   discipline.PriorContext{},
			opts: discipline.RenderOpts{},
		},
		{
			name: "phase_single_memory_record",
			pc:   discipline.PriorContext{PhaseIdentity: snapshotRecord1 + "\n\n"},
			opts: discipline.RenderOpts{},
		},
		{
			name: "phase_multi_memory_record",
			pc:   discipline.PriorContext{PhaseIdentity: snapshotMultiRecord()},
			opts: discipline.RenderOpts{},
		},
		{
			name: "phase_memory_with_unicode",
			pc:   discipline.PriorContext{PhaseIdentity: snapshotUnicode + "\n\n"},
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

		// --- Group L (M3 PR3a): enriched-layer golden fixtures ---
		// Structural assertions are co-located with each case below.
		{
			name: "phase_with_skills",
			pc: discipline.PriorContext{
				Skills: []discipline.RenderedSkill{
					{Name: "clean-arch", Version: "v2", Status: "active", Source: "manual",
						Techniques: []string{"step-back"},
						Content:    "Apply hexagonal architecture: ports+adapters boundary."},
				},
			},
			opts: discipline.RenderOpts{},
		},
		{
			name: "phase_with_episodes",
			pc: discipline.PriorContext{
				Episodes: []discipline.EpisodeRef{
					{ID: "ep-001", Content: "Fixed N+1 query in phase service."},
					{ID: "ep-002", Content: "Discovered advisory lock race on concurrent apply."},
				},
			},
			opts: discipline.RenderOpts{},
		},
		{
			name: "phase_with_digests",
			pc: discipline.PriorContext{
				ChangeDigests: []discipline.ChangeDigestRef{
					{ChangeID: "skills-lifecycle-matcher", Content: "Added lifecycle fields to Skill aggregate."},
				},
			},
			opts: discipline.RenderOpts{},
		},
		{
			name: "phase_all_layers",
			pc: discipline.PriorContext{
				Skills: []discipline.RenderedSkill{
					{Name: "tdd-always", Version: "v1", Status: "active", Source: "manual",
						Content: "Write failing test first."},
				},
				Episodes: []discipline.EpisodeRef{
					{ID: "ep-01", Content: "Found that testcontainers adds 4s cold-start."},
				},
				ChangeDigests: []discipline.ChangeDigestRef{
					{ChangeID: "prior-context", Content: "Introduced PriorContext Render."},
				},
				BusinessRules: []discipline.RuleRef{
					{ID: "rule-01", Kind: "decision", Content: "Use pgx/v5."},
				},
				PhaseIdentity: "spec: defined Render contract\ndesign: layer-order canonical",
			},
			opts: discipline.RenderOpts{},
		},
		{
			name: "render_attribution_on",
			pc: discipline.PriorContext{
				Skills: []discipline.RenderedSkill{
					{Name: "attr-skill", Version: "v3", Status: "active", Source: "consolidation_worker",
						Techniques: []string{"inline-why"},
						Content:    "Attribution header content here."},
				},
			},
			opts: discipline.RenderOpts{EnableAttribution: true},
		},
		{
			name: "render_attribution_off",
			pc: discipline.PriorContext{
				Skills: []discipline.RenderedSkill{
					{Name: "attr-skill", Version: "v3", Status: "active", Source: "consolidation_worker",
						Techniques: []string{"inline-why"},
						Content:    "Attribution header content here."},
				},
			},
			opts: discipline.RenderOpts{EnableAttribution: false},
		},
		{
			// render_budget_truncated: budget small enough to truncate skill content.
			// TokenBudget:75 → skills alloc=30 bytes → first skill fits, second is truncated.
			name: "render_budget_truncated",
			pc: discipline.PriorContext{
				Skills: []discipline.RenderedSkill{
					{Name: "s1", Version: "v1", Status: "active", Source: "manual",
						Content: "skill-one-content-here"},
					{Name: "s2", Version: "v1", Status: "active", Source: "manual",
						Content: "skill-two-content-here"},
				},
			},
			opts: discipline.RenderOpts{TokenBudget: 75},
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
// Group L (M3 PR3a): structural assertions on enriched golden cases.
// These complement the byte-exact capture above with semantic checks that
// survive reformatting (D-M3-10: byte-exact contract retired for M3 goldens).
// ---------------------------------------------------------------------------

// TestRender_GroupL_StructuralAssertions verifies semantic properties of each
// M3 enriched-layer golden case without relying on byte-exact matching.
func TestRender_GroupL_StructuralAssertions(t *testing.T) {
	t.Run("phase_with_skills", func(t *testing.T) {
		pc := discipline.PriorContext{
			Skills: []discipline.RenderedSkill{
				{Name: "clean-arch", Version: "v2", Status: "active", Source: "manual",
					Techniques: []string{"step-back"},
					Content:    "Apply hexagonal architecture: ports+adapters boundary."},
			},
		}
		out := pc.Render(discipline.RenderOpts{})
		require.Contains(t, out, "clean-arch", "skill name must appear")
		require.Contains(t, out, "Apply hexagonal architecture", "skill content must appear verbatim")
		require.Contains(t, out, "step-back", "technique must appear")
		require.NotContains(t, out, "## Skill: ", "no attribution header without EnableAttribution")
	})

	t.Run("phase_with_episodes", func(t *testing.T) {
		pc := discipline.PriorContext{
			Episodes: []discipline.EpisodeRef{
				{ID: "ep-001", Content: "Fixed N+1 query in phase service."},
				{ID: "ep-002", Content: "Discovered advisory lock race on concurrent apply."},
			},
		}
		out := pc.Render(discipline.RenderOpts{})
		require.Contains(t, out, "Fixed N+1 query", "first episode content must appear")
		require.Contains(t, out, "advisory lock race", "second episode content must appear")
	})

	t.Run("phase_with_digests", func(t *testing.T) {
		pc := discipline.PriorContext{
			ChangeDigests: []discipline.ChangeDigestRef{
				{ChangeID: "skills-lifecycle-matcher", Content: "Added lifecycle fields to Skill aggregate."},
			},
		}
		out := pc.Render(discipline.RenderOpts{})
		require.Contains(t, out, "lifecycle fields", "digest content must appear")
	})

	t.Run("phase_all_layers_ordering", func(t *testing.T) {
		pc := discipline.PriorContext{
			Skills: []discipline.RenderedSkill{
				{Name: "tdd-always", Version: "v1", Status: "active", Source: "manual",
					Content: "Write failing test first."},
			},
			Episodes: []discipline.EpisodeRef{
				{ID: "ep-01", Content: "Found that testcontainers adds 4s cold-start."},
			},
			ChangeDigests: []discipline.ChangeDigestRef{
				{ChangeID: "prior-context", Content: "Introduced PriorContext Render."},
			},
			BusinessRules: []discipline.RuleRef{
				{ID: "rule-01", Kind: "decision", Content: "Use pgx/v5."},
			},
			PhaseIdentity: "spec: defined Render contract",
		}
		out := pc.Render(discipline.RenderOpts{})
		skillIdx := strings.Index(out, "Write failing test first.")
		epIdx := strings.Index(out, "testcontainers adds 4s")
		digestIdx := strings.Index(out, "Introduced PriorContext")
		ruleIdx := strings.Index(out, "Use pgx/v5")
		identityIdx := strings.Index(out, "spec: defined Render contract")
		require.Greater(t, skillIdx, -1, "skills must appear")
		require.Greater(t, epIdx, -1, "episodes must appear")
		require.Greater(t, digestIdx, -1, "digests must appear")
		require.Greater(t, ruleIdx, -1, "rules must appear")
		require.Greater(t, identityIdx, -1, "phase identity must appear")
		// Canonical layer order (D-M3-11): Skills → Episodes → ChangeDigests → BusinessRules → PhaseIdentity
		require.Less(t, skillIdx, epIdx, "skills before episodes")
		require.Less(t, epIdx, digestIdx, "episodes before digests")
		require.Less(t, digestIdx, ruleIdx, "digests before rules")
		require.Less(t, ruleIdx, identityIdx, "rules before phase identity")
	})

	t.Run("render_attribution_on", func(t *testing.T) {
		pc := discipline.PriorContext{
			Skills: []discipline.RenderedSkill{
				{Name: "attr-skill", Version: "v3", Status: "active", Source: "consolidation_worker",
					Content: "Attribution header content here."},
			},
		}
		out := pc.Render(discipline.RenderOpts{EnableAttribution: true})
		require.Contains(t, out, "## Skill: attr-skill v3 (active, source=consolidation_worker)",
			"attribution header must match D-M3-8 format")
		require.Contains(t, out, "Attribution header content here.", "skill content must appear")
	})

	t.Run("render_attribution_off", func(t *testing.T) {
		pc := discipline.PriorContext{
			Skills: []discipline.RenderedSkill{
				{Name: "attr-skill", Version: "v3", Status: "active", Source: "consolidation_worker",
					Content: "Attribution header content here."},
			},
		}
		out := pc.Render(discipline.RenderOpts{EnableAttribution: false})
		require.NotContains(t, out, "## Skill: attr-skill", "no attribution header when disabled")
		require.Contains(t, out, "## attr-skill", "simple header must appear")
		require.Contains(t, out, "Attribution header content here.", "skill content must appear")
	})
}

// ---------------------------------------------------------------------------
// Additional determinism test (design spec requirement D-M05-7)
// ---------------------------------------------------------------------------

func TestRender_Deterministic(t *testing.T) {
	pc := discipline.PriorContext{PhaseIdentity: "X"}
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

// ---------------------------------------------------------------------------
// Group I — Stub types become real (M3 PR3a)
// I.1 — RenderedSkill fields populated
// I.2 — EpisodeRef, RuleRef, ChangeDigestRef real fields
// ---------------------------------------------------------------------------

// TestRenderedSkill_FieldsPopulated asserts that RenderedSkill has the 6
// concrete fields declared in design D-M3-5. The test constructs a value via
// struct literal and confirms all fields survive a JSON round-trip (non-empty
// when set). Tests MUST fail RED until I.3 adds real fields to the struct.
func TestRenderedSkill_FieldsPopulated(t *testing.T) {
	rs := discipline.RenderedSkill{
		Name:       "clean-arch",
		Version:    "v3",
		Status:     "active",
		Source:     "consolidation_worker",
		Techniques: []string{"step-back", "chain-of-verification"},
		Content:    "Apply hexagonal architecture: ports+adapters boundary.",
	}
	require.Equal(t, "clean-arch", rs.Name, "Name must be set")
	require.Equal(t, "v3", rs.Version, "Version must be set")
	require.Equal(t, "active", rs.Status, "Status must be set")
	require.Equal(t, "consolidation_worker", rs.Source, "Source must be set")
	require.Equal(t, []string{"step-back", "chain-of-verification"}, rs.Techniques, "Techniques must be set")
	require.Equal(t, "Apply hexagonal architecture: ports+adapters boundary.", rs.Content, "Content must be set")

	// JSON round-trip: real fields must survive marshal/unmarshal.
	data, err := json.Marshal(rs)
	require.NoError(t, err)
	var decoded discipline.RenderedSkill
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, rs, decoded, "RenderedSkill must JSON round-trip")
}

// TestEpisodeRef_FromRecentEpisodic asserts EpisodeRef has real ID + Content fields.
func TestEpisodeRef_FromRecentEpisodic(t *testing.T) {
	ep := discipline.EpisodeRef{
		ID:      "mem-01ABC",
		Content: "Fixed N+1 query in phase service — root cause: eager join missing.",
	}
	require.Equal(t, "mem-01ABC", ep.ID, "EpisodeRef.ID must be set")
	require.Equal(t, "Fixed N+1 query in phase service — root cause: eager join missing.", ep.Content, "EpisodeRef.Content must be set")

	data, err := json.Marshal(ep)
	require.NoError(t, err)
	var decoded discipline.EpisodeRef
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, ep, decoded, "EpisodeRef must JSON round-trip")
}

// TestRuleRef_FromDecisions asserts RuleRef has real ID, Kind, Content fields and
// Kind="decision" for rules sourced from the decisions section.
func TestRuleRef_FromDecisions(t *testing.T) {
	r := discipline.RuleRef{
		ID:      "mem-D001",
		Kind:    "decision",
		Content: "Use pgx/v5 for all database access; no gorm.",
	}
	require.Equal(t, "mem-D001", r.ID, "RuleRef.ID must be set")
	require.Equal(t, "decision", r.Kind, "RuleRef.Kind must be 'decision'")
	require.Equal(t, "Use pgx/v5 for all database access; no gorm.", r.Content, "RuleRef.Content must be set")

	data, err := json.Marshal(r)
	require.NoError(t, err)
	var decoded discipline.RuleRef
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, r, decoded, "RuleRef must JSON round-trip")
}

// TestRuleRef_FromHeuristics asserts RuleRef.Kind="heuristic" for rules from heuristics section.
func TestRuleRef_FromHeuristics(t *testing.T) {
	r := discipline.RuleRef{
		ID:      "mem-H007",
		Kind:    "heuristic",
		Content: "Always freeze Clock in golden tests for byte-exact reproducibility.",
	}
	require.Equal(t, "heuristic", r.Kind, "RuleRef.Kind must be 'heuristic'")
	require.NotEmpty(t, r.Content, "RuleRef.Content must be non-empty")
}

// TestChangeDigestRef_Populated asserts ChangeDigestRef has real ChangeID + Content.
func TestChangeDigestRef_Populated(t *testing.T) {
	cd := discipline.ChangeDigestRef{
		ChangeID: "feat-prior-context-fixture",
		Content:  "Digest: introduced PriorContext struct, Render method, 8 stub types.",
	}
	require.Equal(t, "feat-prior-context-fixture", cd.ChangeID, "ChangeDigestRef.ChangeID must be set")
	require.Equal(t, "Digest: introduced PriorContext struct, Render method, 8 stub types.", cd.Content, "ChangeDigestRef.Content must be set")

	data, err := json.Marshal(cd)
	require.NoError(t, err)
	var decoded discipline.ChangeDigestRef
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, cd, decoded, "ChangeDigestRef must JSON round-trip")
}

// ---------------------------------------------------------------------------
// Group J — Render() layers + budget + attribution (M3 PR3a)
// J.1 — layer ordering (skills before episodes)
// J.2 — no blocked/deprecated skill rendered
// J.3 — budget respected (truncation marker present)
// J.4 — attribution headers when enabled
// J.5 — zero-value RenderOpts is still no-op
// ---------------------------------------------------------------------------

// TestRender_LayerOrdering verifies that the skills layer appears before
// the episodes layer in Render() output (D-M3-11 canonical order).
// MUST fail RED until J.6/J.9 implement collectLayers.
func TestRender_LayerOrdering(t *testing.T) {
	pc := discipline.PriorContext{
		Skills: []discipline.RenderedSkill{
			{Name: "clean-arch", Version: "v1", Status: "active", Source: "manual", Content: "skill content here"},
		},
		Episodes: []discipline.EpisodeRef{
			{ID: "ep-01", Content: "episode content here"},
		},
	}
	out := pc.Render(discipline.RenderOpts{})
	skillIdx := strings.Index(out, "skill content here")
	epIdx := strings.Index(out, "episode content here")
	require.Greater(t, skillIdx, -1, "skills content must appear in Render output")
	require.Greater(t, epIdx, -1, "episodes content must appear in Render output")
	require.Less(t, skillIdx, epIdx, "skills MUST appear before episodes in Render output (D-M3-11)")
}

// TestRender_NoBlockedSkillRendered verifies that deprecated/blocked/archived
// skills set in PriorContext.Skills do NOT appear in Render() output.
// The SkillMatcher gate prevents non-active skills from entering PriorContext.Skills,
// but Render itself must also skip any non-active skill by checking Status.
// MUST fail RED until J.6 implements the guard.
func TestRender_NoBlockedSkillRendered(t *testing.T) {
	pc := discipline.PriorContext{
		Skills: []discipline.RenderedSkill{
			{Name: "blocked-skill", Version: "v1", Status: "blocked", Source: "manual", Content: "BLOCKED CONTENT"},
			{Name: "deprecated-skill", Version: "v1", Status: "deprecated", Source: "manual", Content: "DEPRECATED CONTENT"},
			{Name: "archived-skill", Version: "v1", Status: "archived", Source: "manual", Content: "ARCHIVED CONTENT"},
			{Name: "active-skill", Version: "v2", Status: "active", Source: "manual", Content: "ACTIVE CONTENT"},
		},
	}
	out := pc.Render(discipline.RenderOpts{})
	require.NotContains(t, out, "BLOCKED CONTENT", "blocked skill must not render")
	require.NotContains(t, out, "DEPRECATED CONTENT", "deprecated skill must not render")
	require.NotContains(t, out, "ARCHIVED CONTENT", "archived skill must not render")
	require.Contains(t, out, "ACTIVE CONTENT", "active skill MUST render")
}

// TestRender_BudgetRespected verifies that when TokenBudget is set, only the
// allowed amount of skill content appears and a truncation marker is emitted.
// MUST fail RED until J.7 implements enforceBudget.
func TestRender_BudgetRespected(t *testing.T) {
	// Three skills; set budget to allow only the first skill.
	// Skill 1 body (no-attribution mode):
	//   "## s1\n" (6) + "skill-one-content-here" (22) + "\n\n" (2) = 30 bytes
	//   Budget share for skills = 40% of total budget.
	//   So total budget = 75 → skills share = 30 bytes → first skill fits exactly.
	//   Skill 2+3 get cut.
	pc := discipline.PriorContext{
		Skills: []discipline.RenderedSkill{
			{Name: "s1", Version: "v1", Status: "active", Source: "manual", Content: "skill-one-content-here"},
			{Name: "s2", Version: "v1", Status: "active", Source: "manual", Content: "skill-two-content-here"},
			{Name: "s3", Version: "v1", Status: "active", Source: "manual", Content: "skill-three-content-here"},
		},
	}
	// Total budget 75 → skills alloc = 40% of 75 = 30 bytes.
	// First skill is 30 bytes exactly; second would need 30+ more → truncated.
	out := pc.Render(discipline.RenderOpts{TokenBudget: 75})
	require.Contains(t, out, "truncated", "truncation marker must be present when budget exceeded")
	// The first skill should fit; the others should not.
	require.Contains(t, out, "skill-one-content-here", "first skill must be included within budget")
	require.NotContains(t, out, "skill-two-content-here", "second skill must be cut by budget")
	require.NotContains(t, out, "skill-three-content-here", "third skill must be cut by budget")
}

// TestRender_AttributionHeaders verifies that when EnableAttribution=true,
// each rendered skill is prefixed with the canonical attribution header.
// MUST fail RED until J.8 implements attribution header emission.
func TestRender_AttributionHeaders(t *testing.T) {
	pc := discipline.PriorContext{
		Skills: []discipline.RenderedSkill{
			{Name: "clean-arch", Version: "v3", Status: "active", Source: "consolidation_worker", Content: "content here"},
		},
	}
	out := pc.Render(discipline.RenderOpts{EnableAttribution: true})
	require.Contains(t, out, "## Skill: clean-arch v3 (active, source=consolidation_worker)",
		"attribution header must match D-M3-8 format")
}

// TestRenderOpts_ZeroValue_IsNoOp_M3 confirms the M0.5 no-op contract holds
// after M3 enrichment: zero-value RenderOpts with skills and episodes includes
// all items without attribution headers.
// MUST fail RED when Render starts filtering non-active skills (J.2 changes behavior).
func TestRenderOpts_ZeroValue_IsNoOp_M3(t *testing.T) {
	pc := discipline.PriorContext{
		Skills: []discipline.RenderedSkill{
			{Name: "s1", Version: "v1", Status: "active", Source: "manual", Content: "skill content"},
		},
		Episodes: []discipline.EpisodeRef{
			{ID: "ep-01", Content: "episode content"},
		},
	}
	out := pc.Render(discipline.RenderOpts{})
	// All active items included.
	require.Contains(t, out, "skill content", "active skill must be included with zero-value opts")
	require.Contains(t, out, "episode content", "episode must be included with zero-value opts")
	// No attribution headers.
	require.NotContains(t, out, "## Skill: ", "no attribution headers with zero EnableAttribution")
}
