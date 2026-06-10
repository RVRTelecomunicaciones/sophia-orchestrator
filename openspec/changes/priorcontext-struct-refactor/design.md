# Design: priorcontext-struct-refactor (M0.5)

## Approach

Introduce a single canonical `discipline.PriorContext` struct + deterministic `Render(RenderOpts) string` method in `internal/application/discipline/prior_context.go`. The two existing callsites (`phase/service.go:buildPriorContext` and `apply/run.go:loadPriorContext` + `refreshApplyProgress`) construct the struct, populate either `PhaseIdentity` (apply path with named sections) or `RawMemoryBlob` (phase-service path with unstructured bundle), then call `Render(RenderOpts{})` at the boundary. Downstream signatures stay `string` (render-at-boundary, operator decision #6). The 7 V4.1 forward-compat fields (`Skills`, `StructuralCtx`, `Episodes`, `ChangeDigests`, `BusinessRules`, `Routines`, `AuxiliaryMemory`) land as empty/nil with their stub types; `Render` skips them in M0.5. Twelve golden fixtures captured BEFORE the struct exists (TDD-RED baseline) prove byte-exact preservation. Four benchmarks prove `Render() <= 2x` inline-concat latency.

## Architecture Decisions

### D-M05-1: PriorContext field order follows V4.1 §16 with RawMemoryBlob appended

**Choice**: Field order matches V4.1 §16 M0.5 spec. `RawMemoryBlob string` appended LAST with godoc marking it M0.5-interim.

**Alternatives considered**:
- Drop `RawMemoryBlob`, force phase-service callsite to fake structure into `Episodes`/`ChangeDigests` → rejected: deviates from operator decision #5; M3 would still re-decompose.
- Put `RawMemoryBlob` in the middle of the struct → rejected: hides the M0.5-interim nature; V4.1 ordering becomes ambiguous for future readers.

**Rationale**: Reviewers reading `prior_context.go` see V4.1 §16 order first, then `RawMemoryBlob` clearly marked as interim. M3 removal becomes a single-field delete plus 1 callsite migration.

### D-M05-2: StructuralContextRef opaque marker (Option D from explore §4)

**Choice**: `type StructuralContextRef struct{}` — empty struct (zero-size, zero-cost). Field declared as `*StructuralContextRef`, always `nil` in M0.5. `Render` guards with `if pc.StructuralCtx != nil { ... }` — the body is unreachable in M0.5 but documents M3's wiring point.

**Alternatives considered**:
- Option A (move `StructuralContext` to `domain/structural`) → rejected: touches INIT-0 stable code; out of M0.5 scope.
- Option B (interface `StructuralCtxView`) → rejected: adds vocabulary nobody consumes in M0.5; over-engineered for a stub.
- Option C (`any` / `json.RawMessage`) → rejected: loses type safety; M3 has no compile-time signal where to wire.

**Rationale**: Empty struct is the cheapest forward-compat anchor. Avoids the import cycle entirely (no reference to `init/detector`). M3 redefines `StructuralContextRef` to a real shape OR replaces it with the proper type via a single rename. Zero blast radius today.

### D-M05-3: RawMemoryBlob interim field for phase-service path

**Choice**: `RawMemoryBlob string` populated by `phase/service.go:buildPriorContext` with the existing `strings.Builder` loop output (verbatim memory-engine bundle concatenation). Godoc inline: `// M0.5-interim: unstructured memory bundle from phase-service path. M3 will decompose into Episodes / ChangeDigests / BusinessRules and remove this field.`

**Alternatives considered**:
- Two structs (`PhasePriorContext`, `ApplyPriorContext`) → rejected: doubles test surface, diverges from V4.1 §16 single struct.
- Force memory-engine records into `Episodes` field today → rejected: `Episodes` semantics are M3-defined; mis-typing the field destroys forward-compat clarity.

**Rationale**: One canonical struct (operator decision #5). Single render path. M3 has a clear delete + remap target.

### D-M05-4: Render-at-boundary preserves downstream signatures

**Choice**: Only `buildPriorContext` (phase/service.go:960), `loadPriorContext` (apply/run.go:844), and `refreshApplyProgress` (apply/run.go:807) bodies change. They construct a `PriorContext`, call `pc.Render(RenderOpts{})`, and return the resulting `string`. The 6 downstream methods (`runAllGroups`, `runTeamLead`, `runImplementWithRetry`, `dispatchImplement`, `dispatchImplementWithOverride`, `runGroupBuildFeedbackLoop`) keep their `string` parameters untouched. `discipline.PromptInput.PriorContext` stays `string`.

**Alternatives considered**:
- Plumb `*PriorContext` through the chain → rejected: 6 signatures change; blast radius explodes; not testable in 1 PR.
- Render lazily inside `PromptInput.Build` → rejected: forces `PromptInput.PriorContext` field to change type; requires `prompt_builder.go` and `prompt_builder_test.go` edits; widens the diff for no semantic gain in M0.5.

**Rationale**: Minimum-blast-radius refactor. Matches operator decision #6. Each callsite owns construction; renders at its own boundary.

### D-M05-5: Baseline-capture-FIRST sequencing (strict TDD)

**Choice**: Within the single PR, sequence the work as: (a) write 12 golden fixtures by running the EXISTING inline-concat path with deterministic fixtures (frozen `Clock`, frozen `IDGenerator`) — these are the "before" snapshots; (b) write `TestPriorContext_Render_Goldens` referencing fixtures that exist but a `Render` method that does NOT — observe RED; (c) implement `PriorContext` + `Render`; (d) migrate the 2 callsites; (e) re-run snapshots → byte-exact match expected.

**Alternatives considered**:
- Codify the post-refactor output as the golden → rejected: a whitespace drift in the refactor would silently become "the new golden"; defeats byte-exact preservation.
- Capture goldens in a separate prior PR → rejected: 1-PR constraint (proposal scope); operator decision #11 keeps it strict TDD inside one PR.

**Rationale**: This is the critical defense against the highest-likelihood risk (byte drift). The goldens must reflect the PRE-refactor reality, not whatever the refactor produces. Operator decision #11 (strict TDD).

### D-M05-6: RenderOpts zero-value MUST be a no-op (operator decision #9)

**Choice**: `type RenderOpts struct { TokenBudget int; EnableAttribution bool }`. `Render` checks each field for zero-value and skips the hook (no truncation, no `## section` headers added). Test `TestRenderOpts_ZeroValue_IsNoOp` explicitly asserts `pc.Render(RenderOpts{})` equals a control inline-concat case for both callsite shapes.

**Alternatives considered**:
- Drop RenderOpts entirely in M0.5 → rejected: M3 needs the hook surface; declaring it now with no-op semantics avoids a signature change later.
- Use pointer receivers (`*RenderOpts`) with nil sentinel → rejected: more error-prone than zero-value checks; Go idiom prefers value semantics for option structs.

**Rationale**: V4.1 declares the hook surface. M0.5 ships the struct so M3 has a stable API to enrich. Zero-value-is-no-op is the safest contract for forward compatibility.

### D-M05-7: Render is deterministic — no clock, no random, no env

**Choice**: `Render` reads only from `pc` (receiver) fields and `opts`. No `time.Now()`, no `rand`, no `os.Getenv`, no maps with unstable iteration order. All inputs are in the struct.

**Alternatives considered**:
- Inject a `Clock` interface → rejected: M0.5 has no time-dependent rendering; adds vocabulary for no consumer.

**Rationale**: Determinism is the precondition for byte-exact golden testing. This also matches sophia-orchestator CLAUDE.md rule #5 (no direct `time.Now()` / `ulid.Make()` in domain/application).

### D-M05-8: Single file `prior_context.go` for struct + 8 stub types

**Choice**: All types (`PriorContext`, `RenderedSkill`, `StructuralContextRef`, `EpisodeRef`, `ChangeDigestRef`, `RuleRef`, `RoutineOutput`, `AuxiliaryBlock`, `RenderOpts`) + `Render` method live in `internal/application/discipline/prior_context.go` (~150 LoC total).

**Alternatives considered**:
- Split stub types into `prior_context_types.go` → rejected: adds organizational overhead at this size; readers must jump files to understand the struct.

**Rationale**: Co-location wins at ~150 LoC. A single grep reveals the entire M0.5 surface.

## Data Flow

```
                phase/service.go:buildPriorContext
                        |
                memory.BuildContext → bundle
                        |
                strings.Builder loop → rawBlob
                        |
                PriorContext{RawMemoryBlob: rawBlob}.Render(RenderOpts{})
                        |
                        v
                PromptInput.PriorContext (string) ──→ Build ──→ prompt ──→ dispatcher


                apply/run.go:loadPriorContext
                        |
                memory.GetByTopicKey × {spec, design} → sections
                        |
                inline assembly → "## spec ...\n\n## design ..."
                        |
                PriorContext{PhaseIdentity: assembled3SectionString}.Render(RenderOpts{})
                        |
                        v (returned to caller)
                refreshApplyProgress(c, base) ── optionally appends "\n\n## Recent progress …"
                        |
                        v
                PromptInput.PriorContext (string) ──→ Build ──→ prompt ──→ dispatcher
```

`refreshApplyProgress` may rebuild a new `PriorContext{PhaseIdentity: base + "\n\n## Recent progress (...)\n\n" + content}.Render(RenderOpts{})` OR keep the existing string concat — both produce byte-exact identical output because `RenderOpts{}` is a no-op pass-through. Recommendation: rebuild via struct to keep "zero callsites use inline concatenation" success criterion clean.

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/application/discipline/prior_context.go` | Create | `PriorContext` struct + 8 stub types + `RenderOpts` + `Render` (~150 LoC) |
| `internal/application/discipline/prior_context_test.go` | Create | 12 golden snapshot tests + `TestRenderOpts_ZeroValue_IsNoOp` + determinism unit test (~200 LoC) |
| `internal/application/discipline/prior_context_bench_test.go` | Create | 4 benchmarks: 2 `Render` paths + 2 inline-concat baselines (~80 LoC) |
| `internal/application/discipline/testdata/priorcontext/*.golden.txt` | Create | 12 golden fixtures (~500 LoC inert — excluded from 400-LoC budget per operator decision #3) |
| `internal/application/phase/service.go` | Modify | `buildPriorContext` body only: wrap existing `strings.Builder` output into `PriorContext.RawMemoryBlob`, call `Render(RenderOpts{})`. Signature unchanged (~30 LoC delta). |
| `internal/application/apply/run.go` | Modify | `loadPriorContext` + `refreshApplyProgress` bodies only: construct `PriorContext.PhaseIdentity`, call `Render(RenderOpts{})`. Signatures unchanged (~50 LoC delta). |
| `internal/application/discipline/prompt_builder.go` | NO CHANGE | Skills sibling section untouched (operator decision #4) |
| `internal/application/apply/teamlead.go` | NO CHANGE | Receives `string` PriorContext (render-at-boundary) |
| `internal/application/apply/build_feedback.go` | NO CHANGE | Receives `string` PriorContext (render-at-boundary) |
| `internal/application/init/detector` | NO CHANGE | Option D defers `StructuralContext` cycle to M3 |

## Interfaces / Contracts

### Types (exact Go shapes — `internal/application/discipline/prior_context.go`)

```go
package discipline

// PriorContext is the structured assembly of prior-context content fed to
// LLM phase prompts. M0.5 introduces this struct as a refactor of inline
// string concatenation; M3 enriches it with skills/episodes/digests/routines.
//
// Field order follows V4.1 §16 M0.5 milestone spec. RawMemoryBlob is an
// M0.5-interim field for the phase-service callsite; M3 will decompose it
// into Episodes / ChangeDigests / BusinessRules and remove the field.
type PriorContext struct {
    PhaseIdentity   string                // apply path: "## spec ...\n\n## design ..." (+ optional progress)
    Skills          []RenderedSkill       // M3: rendered skill summaries (empty in M0.5)
    StructuralCtx   *StructuralContextRef // M3 wires; nil in M0.5
    Episodes        []EpisodeRef          // M3: relevant episodic memories (empty in M0.5)
    ChangeDigests   []ChangeDigestRef     // M3: prior change digests (empty in M0.5)
    BusinessRules   []RuleRef             // M3: project rules (empty in M0.5)
    Routines        []RoutineOutput       // M3: deterministic routine outputs (empty in M0.5)
    AuxiliaryMemory *AuxiliaryBlock       // M3: aux memory provider block (nil in M0.5)
    RawMemoryBlob   string                // M0.5-interim: unstructured memory bundle from phase-service path
}

// RenderedSkill is a forward-compat stub for M3 skill rendering integration.
type RenderedSkill struct{}

// StructuralContextRef is an opaque marker for M3 StructuralContext wiring.
// Concrete shape chosen in M3 (interface vs domain type — see explore §4).
type StructuralContextRef struct{}

// EpisodeRef, ChangeDigestRef, RuleRef, RoutineOutput, AuxiliaryBlock are
// forward-compat stubs for M3 enrichment. Empty struct = zero-cost anchor.
type EpisodeRef     struct{}
type ChangeDigestRef struct{}
type RuleRef        struct{}
type RoutineOutput  struct{}
type AuxiliaryBlock struct{}

// RenderOpts configures Render. Zero-value MUST be a no-op for ALL hooks.
type RenderOpts struct {
    // TokenBudget caps total bytes emitted. 0 = unlimited (no-op in M0.5).
    TokenBudget int
    // EnableAttribution emits "## section (topic_key)" headers per layer.
    // false = no attribution added by Render (no-op in M0.5).
    EnableAttribution bool
}

// Render assembles PriorContext into the LLM-facing prompt string.
//
// Render is DETERMINISTIC — it reads only from pc fields and opts; no time,
// no random, no env access. Byte-exact snapshot testing depends on this.
//
// M0.5 emits exactly two layers: PhaseIdentity (apply path) and RawMemoryBlob
// (phase-service path). All other layers (Skills, StructuralCtx, Episodes,
// ChangeDigests, BusinessRules, Routines, AuxiliaryMemory) are empty/nil and
// skipped. M3 enrichment populates them and Render learns to emit them.
func (pc PriorContext) Render(opts RenderOpts) string {
    var b strings.Builder

    // Layer 1: PhaseIdentity — apply path's "## spec / ## design / ## progress" block.
    // Rendered verbatim; the section headers live INSIDE PhaseIdentity (assembled
    // by the callsite) so Render itself adds nothing — byte-exact preservation.
    if pc.PhaseIdentity != "" {
        b.WriteString(pc.PhaseIdentity)
    }

    // Layer 2: RawMemoryBlob — phase-service path's unstructured memory bundle.
    // Rendered verbatim; the callsite already assembled the strings.Builder loop
    // output. M0.5-interim — M3 decomposes into structured layers below.
    if pc.RawMemoryBlob != "" {
        b.WriteString(pc.RawMemoryBlob)
    }

    // M3 future layers — all empty/nil in M0.5, skipped.
    // if len(pc.Episodes) > 0 { ... }
    // if len(pc.ChangeDigests) > 0 { ... }
    // if len(pc.BusinessRules) > 0 { ... }
    // if len(pc.Routines) > 0 { ... }
    // if pc.AuxiliaryMemory != nil { ... }
    // if pc.StructuralCtx != nil { ... }
    // if len(pc.Skills) > 0 { ... }  // operator decision #4: skills stay in sibling section in M0.5

    out := b.String()

    // RenderOpts.TokenBudget — zero-value no-op (operator decision #9).
    if opts.TokenBudget > 0 && len(out) > opts.TokenBudget {
        out = out[:opts.TokenBudget]
    }
    // RenderOpts.EnableAttribution — zero-value no-op (operator decision #9).
    // M3 will emit "## {layer} ({topic_key})" headers when true.
    _ = opts.EnableAttribution // referenced for vet; no-op in M0.5

    return out
}
```

### Callsite migration shapes

`phase/service.go:buildPriorContext` (lines 960-992 today):

```go
func (s *Service) buildPriorContext(ctx context.Context, c *change.Change) string {
    bundle, err := s.d.Memory.BuildContext(ctx, outbound.ContextRequest{ /* unchanged */ })
    if err != nil || bundle == nil {
        return ""
    }
    var sb strings.Builder
    for _, sec := range bundle.Sections {
        for _, rec := range sec.Records {
            sb.WriteString(rec.Content)
            sb.WriteString("\n\n")
        }
    }
    pc := discipline.PriorContext{RawMemoryBlob: sb.String()}
    return pc.Render(discipline.RenderOpts{})
}
```

`apply/run.go:loadPriorContext` (lines 844-880 today):

```go
func (s *RunService) loadPriorContext(ctx context.Context, c *change.Change) (string, error) {
    // memory-engine fetch loop UNCHANGED (lines 845-869)
    // ...
    if len(sections) == 0 {
        return "", nil
    }
    out := sections[0]
    for _, s := range sections[1:] {
        out += "\n\n" + s
    }
    pc := discipline.PriorContext{PhaseIdentity: out}
    return pc.Render(discipline.RenderOpts{}), nil
}
```

`apply/run.go:refreshApplyProgress` (lines 807-827 today):

```go
func (s *RunService) refreshApplyProgress(ctx context.Context, c *change.Change, base string) string {
    // memory-engine fetch UNCHANGED (lines 808-820)
    // ...
    section := fmt.Sprintf("## Recent progress (sdd/%s/apply-progress)\n\n%s", c.Name(), rec.Content)
    var assembled string
    if base == "" {
        assembled = section
    } else {
        assembled = base + "\n\n" + section
    }
    pc := discipline.PriorContext{PhaseIdentity: assembled}
    return pc.Render(discipline.RenderOpts{})
}
```

Note on `refreshApplyProgress`: the input `base` arrives ALREADY rendered (it's the `string` returned by `loadPriorContext` → `Render`). M0.5 keeps the simple concat-then-rewrap approach to satisfy "zero callsites use inline concatenation" without changing the function signature.

### Golden fixture file naming — `internal/application/discipline/testdata/priorcontext/`

Phase-service path (5):
- `phase_empty_memory_bundle.golden.txt`
- `phase_single_memory_record.golden.txt`
- `phase_multi_memory_record.golden.txt`
- `phase_memory_with_unicode.golden.txt`
- `phase_memory_error_returns_empty.golden.txt`

Apply path (7):
- `apply_spec_only.golden.txt`
- `apply_design_only.golden.txt`
- `apply_both_no_progress.golden.txt`
- `apply_full_three_sections.golden.txt`
- `apply_progress_refresh.golden.txt`
- `apply_progress_error_fail_soft.golden.txt`
- `apply_empty_returns_empty.golden.txt`

### Snapshot test driver shape

```go
func TestPriorContext_Render_Goldens(t *testing.T) {
    cases := []struct {
        name string
        pc   discipline.PriorContext
        opts discipline.RenderOpts
    }{
        {name: "phase_empty_memory_bundle",      pc: discipline.PriorContext{}, opts: discipline.RenderOpts{}},
        {name: "phase_single_memory_record",     pc: discipline.PriorContext{RawMemoryBlob: "fix flaky test\n\n"}, opts: discipline.RenderOpts{}},
        {name: "phase_multi_memory_record",      pc: discipline.PriorContext{RawMemoryBlob: multiRecordFixture}, opts: discipline.RenderOpts{}},
        {name: "phase_memory_with_unicode",      pc: discipline.PriorContext{RawMemoryBlob: "café ☕ 日本語\n\n"}, opts: discipline.RenderOpts{}},
        {name: "phase_memory_error_returns_empty", pc: discipline.PriorContext{}, opts: discipline.RenderOpts{}}, // empty struct == error path's "" return
        {name: "apply_spec_only",                pc: discipline.PriorContext{PhaseIdentity: applySpecOnlyFixture}, opts: discipline.RenderOpts{}},
        {name: "apply_design_only",              pc: discipline.PriorContext{PhaseIdentity: applyDesignOnlyFixture}, opts: discipline.RenderOpts{}},
        {name: "apply_both_no_progress",         pc: discipline.PriorContext{PhaseIdentity: applyBothFixture}, opts: discipline.RenderOpts{}},
        {name: "apply_full_three_sections",      pc: discipline.PriorContext{PhaseIdentity: applyFullFixture}, opts: discipline.RenderOpts{}},
        {name: "apply_progress_refresh",         pc: discipline.PriorContext{PhaseIdentity: applyProgressRefreshFixture}, opts: discipline.RenderOpts{}},
        {name: "apply_progress_error_fail_soft", pc: discipline.PriorContext{PhaseIdentity: applyBothFixture}, opts: discipline.RenderOpts{}},
        {name: "apply_empty_returns_empty",      pc: discipline.PriorContext{}, opts: discipline.RenderOpts{}},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            got := tc.pc.Render(tc.opts)
            if updateGolden() {
                writeGolden(t, filepath.Join("priorcontext", tc.name+".golden.txt"), got)
                return
            }
            want := readGolden(t, filepath.Join("priorcontext", tc.name+".golden.txt"))
            require.Equal(t, want, got)
        })
    }
}

func TestRenderOpts_ZeroValue_IsNoOp(t *testing.T) {
    // Control: inline-concat output for a representative apply 3-section case.
    inline := "## spec (sdd/x/spec)\n\nFOO\n\n## design (sdd/x/design)\n\nBAR"
    pc := discipline.PriorContext{PhaseIdentity: inline}
    require.Equal(t, inline, pc.Render(discipline.RenderOpts{}),
        "RenderOpts{} MUST be a no-op (operator decision #9)")
}

func TestRender_Deterministic(t *testing.T) {
    pc := discipline.PriorContext{PhaseIdentity: "X", RawMemoryBlob: "Y"}
    first := pc.Render(discipline.RenderOpts{})
    for i := 0; i < 100; i++ {
        require.Equal(t, first, pc.Render(discipline.RenderOpts{}))
    }
}
```

Reuses `readGolden` / `writeGolden` / `updateGolden` helpers from `prompt_builder_test.go:371-394`.

### Benchmarks shape

```go
func BenchmarkPriorContext_Render_PhaseService(b *testing.B) {
    pc := discipline.PriorContext{RawMemoryBlob: largePhaseFixture}
    opts := discipline.RenderOpts{}
    b.ResetTimer()
    for i := 0; i < b.N; i++ { _ = pc.Render(opts) }
}

func BenchmarkInlineConcat_PhaseService(b *testing.B) {
    bundle := largePhaseRecordSlice // pre-built
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        var sb strings.Builder
        for _, rec := range bundle { sb.WriteString(rec); sb.WriteString("\n\n") }
        _ = sb.String()
    }
}

func BenchmarkPriorContext_Render_ApplyThreeSections(b *testing.B) { /* analogous */ }
func BenchmarkInlineConcat_ApplyThreeSections(b *testing.B)        { /* analogous */ }
```

Run via `go test -bench=. -benchmem ./internal/application/discipline/...`. Apply phase records the ratio in the PR body (not a CI gate — benchmarks are flaky on shared runners; tasks phase enumerates this).

## Testing Strategy

| Layer | What to Test | Approach |
|-------|--------------|----------|
| Unit | `RenderOpts{}` zero-value no-op | Explicit test asserts identity output for both PhaseIdentity-only and RawMemoryBlob-only inputs |
| Unit | `Render` determinism | Loop calling `Render` 100× and assert identical output |
| Unit | Stub type compile-checks | `var _ = discipline.RenderedSkill{}` etc. — type assertions ensure forward-compat fields exist |
| Snapshot | 12 golden fixtures | `TestPriorContext_Render_Goldens` table-driven; `GOLDEN_UPDATE=1` regenerates; byte-exact `require.Equal` |
| Integration | Existing `phase/service_test.go` + `apply/run_test.go` | MUST still pass unchanged — render-at-boundary preserves output |
| Integration | `prompt_builder_test.go` skills sibling section | NO CHANGE — Skills field stays empty in M0.5 (operator decision #4) |
| Benchmark | 4 benchmarks (2 Render + 2 inline baselines) | `go test -bench`; record ratio in PR body; target `<= 2x` |

Sequence (strict TDD per operator decision #11):
1. **RED-baseline**: capture goldens by running the EXISTING inline-concat path with frozen `Clock`/`IDGenerator`. Write `TestPriorContext_Render_Goldens` referencing them → fails because `discipline.PriorContext` does not exist.
2. **GREEN-struct**: implement `prior_context.go`. Tests turn green against captured baselines.
3. **GREEN-callsite-migration**: migrate `phase/service.go` + `apply/run.go`. Existing integration tests stay green. Re-run snapshots — byte-exact match (this is the second green pass that proves the refactor preserves output).
4. **GREEN-benchmarks**: add 4 benchmarks; assert `<= 2x` in PR body.

## Migration / Rollout

No data migration. No schema change. No API contract change. Revert the single PR to return both callsites to inline concatenation; no orchestrator restart sequencing, no memory-engine coordination, no feature flag. Golden fixture files become orphan and can be left for M3 reuse or deleted in the same revert commit.

`golangci-lint v2.12` must pass (INIT-0 lesson — explore.md §10.5; run `make lint` before push).

## Open Questions

- None blocking design. All 11 operator decisions from proposal are locked.

## Risks Revisited (concrete mitigations)

| Risk (from proposal) | Concrete design mitigation |
|----------------------|---------------------------|
| Byte-exact preservation breaks on whitespace drift | D-M05-5 baseline-capture-FIRST sequencing; `Render` writes verbatim from `PhaseIdentity` and `RawMemoryBlob` (no header injection in M0.5); 12 golden fixtures |
| `StructuralContext` import cycle | D-M05-2 opaque `StructuralContextRef struct{}` — no reference to `init/detector`; cycle never forms |
| `RenderOpts{}` zero-value silently activates a hook | D-M05-6 explicit `TestRenderOpts_ZeroValue_IsNoOp`; `Render` body guards each opts field with `> 0` / `if !` checks |
| `RawMemoryBlob` deviates from V4.1 final shape | D-M05-3 godoc inline marks field M0.5-interim with M3 removal path; field order keeps V4.1 §16 ordering first, RawMemoryBlob last |
| Render-at-boundary misses a 3rd callsite | Tasks phase MUST run `rg -n 'PriorContext' internal/application/{phase,apply,discipline}` as an enumeration step; verified consumers today: `phase/service.go` (1), `apply/run.go` (2), `apply/teamlead.go` (2 reads only, no construction), `apply/build_feedback.go` (downstream), `discipline/prompt_builder.go` (1 field read) — all surveyed in explore §2 and re-confirmed in design verification |
| `golangci-lint v2.12` stricter than local | Tasks phase includes `make lint` step before commit |
| Benchmark variance on CI | 2x band absorbs runner noise; record in PR body, not CI gate |

## Out of Scope (reaffirmed from proposal)

- StructuralCtx wiring (M3)
- Skills migration from sibling `# Skill` section into `PriorContext.Skills` (operator decision #4)
- Token-budget enforcement implementation (M3 — field declared, zero-value no-op in M0.5)
- Source attribution implementation (M3 — field declared, zero-value no-op in M0.5)
- Episode / change-digest / business-rule / routine population (M3)
- Downstream chain signature changes (operator decision #6 — render-at-boundary)
- Memory-engine API changes
- `RawMemoryBlob` decomposition into structured layers (M3)
- Moving `StructuralContext` to `domain/structural` (M3 if Option A chosen then)
- Promoting Skills empty struct to a populated type (M3)
