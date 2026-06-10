# Exploration — priorcontext-struct-refactor (M0.5)

**Strategy ref:** V4.1 §16 milestone M0.5 (Q-H2 resolution).
**Mode:** SDD explore. NO production code changes; investigation only.
**Scope:** Single-repo (sophia-orchestator). No cross-repo coupling.
**Engram artifact:** `sdd/priorcontext-struct-refactor/explore`.

---

## 1. Critical architectural finding

**Skills are NOT part of PriorContext today.** They live in a separate `# Skill` section rendered by `internal/application/discipline/prompt_builder.go:97-108`, BEFORE the `# Prior Context` section. V4.1's `[]RenderedSkill` field in the struct is a **future M3 slot** — M0.5 declares it empty and skipped by `Render()`.

This changes the M0.5 problem statement: we are NOT refactoring how skills get into prompts. We are refactoring how the **unstructured prior-context blob** that today is built inline gets assembled.

---

## 2. The 2 callsites that assemble PriorContext today

### File 1 — `internal/application/phase/service.go:960` `buildPriorContext`

```go
// Iterates memory.BuildContext(ctx, ContextRequest{
//     Scope: {ProjectID, TenantID}, MaxTokens: 4000
// }) bundle and concatenates all rec.Content + "\n\n" via strings.Builder.
// Returns "" on error.
//
// Output is RAW unstructured project-wide semantic memory (heuristics,
// decisions). NO section headers. NO source attribution.
```

- Called at `service.go:390`
- Result assigned to `priorCtx`
- Passed to `PromptInput.PriorContext` at `service.go:406`

### File 2 — `internal/application/apply/run.go:844` `loadPriorContext` + `run.go:807` `refreshApplyProgress`

`loadPriorContext` fetches via memory-engine by topic key:

```
sdd/{change_name}/spec
sdd/{change_name}/design
```

Assembles:

```
## spec (sdd/{name}/spec)

{spec content}

## design (sdd/{name}/design)

{design content}
```

`refreshApplyProgress` then appends fail-soft:

```
## Recent progress (sdd/{name}/apply-progress)

{progress content}
```

Returns base unchanged on error. Produces a **3-section string with `##` headers**.

### Important: the string propagates through 5 method signatures

The result string is plumbed through `runAllGroups`, `runTeamLead`, `runImplementWithRetry`, `dispatchImplement`, `dispatchImplementWithOverride`, and `runGroupBuildFeedbackLoop` in `build_feedback.go`. **M0.5 can leave this plumbing alone** by rendering-at-boundary: the callsite calls `pc.Render()` and the rest of the chain still receives a `string`.

---

## 3. The 2 callsite shapes are STRUCTURALLY DIFFERENT

- phase-service path: unstructured memory bundle, no headers
- apply path: 3 named sections with `##` headers (spec / design / progress)

This means the struct cannot have a single naive shape. Two options:

| Option | Pros | Cons |
|---|---|---|
| Single unified struct with multiple optional fields | One canonical type; matches V4.1 | Needs interim fields like `RawMemoryBlob` for memory bundle |
| Separate struct per path (PhasePriorContext, ApplyPriorContext) | Cleaner per-callsite shape | Diverges from V4.1; doubles the test surface |

**Recommendation**: single struct with `RawMemoryBlob string` interim field for the memory bundle. Document as M0.5-interim; M3 decomposes the blob into `Episodes` / `ChangeDigests` / `BusinessRules` proper layers.

---

## 4. StructuralContext import cycle — design blocker

V4.1 struct says:

```go
type PriorContext struct {
    ...
    StructuralCtx *StructuralContext
    ...
}
```

But `StructuralContext` lives in `internal/application/init/detector` (INIT-0 output).
`discipline` package cannot import `application/init` without an application-layer cycle.

**Options** (proposal must decide):

| Option | Effort | Notes |
|---|---|---|
| A. Move `StructuralContext` to `internal/domain/structural` | Medium | Cleanest; ports the type to where it belongs. Affects INIT-0 callers in main. |
| B. Define a `discipline.StructuralCtxView` interface that `detector.StructuralContext` implements | Low | Lowest risk; the struct field stores the interface |
| C. Use `any` / `json.RawMessage` for the field | Low | Cheap but loses type safety |
| D. Defer the field entirely; nil-only in M0.5; M3 wires it | Lowest | Minimal scope; matches "wired but unused" semantics |

**Recommendation**: D for M0.5 (field declared as `*StructuralContextRef` opaque marker; always nil; `Render()` skips). M3 decomposes the field into the consumed shape. This avoids touching INIT-0's stable code.

---

## 5. RenderedSkill type — does it exist?

No. Skills are rendered today by `renderSkillSection()` in `prompt_builder.go:263-282`. The struct returns no value with the name `RenderedSkill`.

V4.1's `[]RenderedSkill` field is a **future M3 slot**. M0.5 declares `Skills []RenderedSkill` where `RenderedSkill` is a stub type (zero fields, just the type declared for forward-compat) so the V4.1 field name lands now.

---

## 6. Existing snapshot test infrastructure

The discipline package ALREADY has:

- `GOLDEN_UPDATE=1` env var convention
- `readGolden`/`writeGolden` helpers in `prompt_builder_test.go:371-394`
- 2 existing `.golden` files in `internal/application/discipline/testdata/`

Reuse pattern for M0.5: new golden files at `internal/application/discipline/testdata/priorcontext/*.golden.txt`.

---

## 7. Snapshot fixtures plan (≥10 per V4.1)

To hit byte-exact preservation across BOTH callsite shapes, 12 candidate fixtures:

1. `empty_memory_bundle.golden.txt` — phase-service path with 0 records → empty string
2. `single_memory_record.golden.txt` — phase-service path with 1 record
3. `multi_memory_record.golden.txt` — phase-service path with 5 records
4. `memory_with_unicode.golden.txt` — non-ASCII content preserved
5. `memory_error_returns_empty.golden.txt` — error path returns ""
6. `apply_spec_only.golden.txt` — apply path with spec, no design
7. `apply_design_only.golden.txt` — apply path with design, no spec
8. `apply_both_no_progress.golden.txt` — spec + design, no apply-progress
9. `apply_full_three_sections.golden.txt` — spec + design + apply-progress
10. `apply_progress_refresh.golden.txt` — refreshApplyProgress appends fail-soft
11. `apply_progress_error_fail_soft.golden.txt` — refresh failure returns base
12. `apply_empty_returns_empty.golden.txt` — both topics absent → ""

---

## 8. Benchmark approach

`internal/application/discipline/prior_context_bench_test.go` with:

```go
func BenchmarkPriorContext_Render_PhaseService(b *testing.B) { ... }
func BenchmarkPriorContext_Render_ApplyThreeSections(b *testing.B) { ... }
func BenchmarkInlineConcat_PhaseService(b *testing.B) { ... }   // baseline
func BenchmarkInlineConcat_ApplyThreeSections(b *testing.B) { ... }  // baseline
```

Target per V4.1: `Render() <= 2x latencia de concatenación inline`. Run via `go test -bench`.

---

## 9. Affected areas

| File | Action | LoC est. |
|---|---|---|
| `internal/application/discipline/prior_context.go` | NEW: struct + Render + stub types | ~150 |
| `internal/application/discipline/prior_context_test.go` | NEW: 12 golden tests + unit tests | ~200 |
| `internal/application/discipline/prior_context_bench_test.go` | NEW: 4 benchmarks | ~80 |
| `internal/application/discipline/testdata/priorcontext/*.golden.txt` | NEW: 12 fixture files | ~500 (inert) |
| `internal/application/phase/service.go` | MODIFIED: `buildPriorContext` body | ~30 |
| `internal/application/apply/run.go` | MODIFIED: `loadPriorContext` + `refreshApplyProgress` | ~50 |
| `internal/application/discipline/prompt_builder.go` | NO CHANGE | 0 |
| `internal/application/apply/teamlead.go` | NO CHANGE (render-at-boundary) | 0 |
| `internal/application/apply/build_feedback.go` | NO CHANGE (render-at-boundary) | 0 |

**Production+test code**: ~510 LoC. **Including golden fixtures (inert)**: ~1010 LoC.

Recommend excluding golden fixtures from 400-line PR budget. Operator decides in proposal.

---

## 10. Cross-cutting risks

1. **HIGH — Byte-exact preservation**: any whitespace, ordering, or separator change breaks tests. Mitigation: 12 golden fixtures, run baseline capture FIRST before any code change.
2. **HIGH — StructuralContext import cycle**: design blocker. Recommended resolution: Option D (nil-only field) for M0.5.
3. **HIGH — RenderOpts zero-value must be no-op**: token budget hooks and source attribution hooks declared on RenderOpts but always disabled when zero. Test explicitly.
4. **MEDIUM — `RawMemoryBlob` interim field deviates from V4.1 spec**: document inline that M3 decomposes the blob into proper layers.
5. **MEDIUM — golangci-lint v2.12 stricter than local** (INIT-0 lesson): run `make lint` before push.
6. **LOW — Empty fixture dirs need `.gitkeep`** (INIT-0 lesson — but unlikely here since golden files are non-empty by definition).
7. **LOW — Benchmark variance** on CI runner. Tolerance band recommended in spec.

---

## 11. Migration mechanics (render-at-boundary)

For each callsite:

**phase/service.go:960**:
```go
// BEFORE
priorCtx := buildPriorContext(ctx, change)  // returns string

// AFTER
pc := buildPriorContextStruct(ctx, change)  // returns *discipline.PriorContext
priorCtx := pc.Render(discipline.RenderOpts{})
```

**apply/run.go:844 + 807**:
```go
// BEFORE
priorCtx := loadPriorContext(ctx, changeName)
priorCtx = refreshApplyProgress(ctx, changeName, priorCtx)

// AFTER
pc := loadPriorContextStruct(ctx, changeName)
pc = refreshApplyProgressStruct(ctx, changeName, pc)
priorCtx := pc.Render(discipline.RenderOpts{})
```

Downstream chain (`runAllGroups`, `runTeamLead`, etc.) remains UNCHANGED — still receives `string`. Render-at-boundary keeps blast radius minimal.

---

## 12. Approaches considered (real forks)

| # | Approach | Recommendation |
|---|---|---|
| A | Concrete struct in `discipline` package, render-at-boundary | **RECOMMENDED** — matches V4.1, minimal blast radius |
| B | PriorContext as interface with multiple impls | REJECTED — over-engineered for refactor |
| C | Two-step: extract helpers first, then struct (2 PRs) | FALLBACK — only if A reveals impossible coupling |

---

## 13. PR scope forecast

```
Production + test code:       ~510 LoC
Golden fixture data (inert):  ~500 LoC
Total:                        ~1010 LoC
```

**Open decision**: do golden fixtures count toward 400-line budget?

- If YES: size:exception declared (like INIT-0 PR2)
- If NO: under budget, no exception

Recommendation: do NOT count inert golden data. Document operator decision in proposal.

---

## 14. Recommendation

**Proceed to sdd-propose** with these locked recommendations:

- Approach A (concrete struct in `discipline`)
- Render-at-boundary (no downstream signature changes)
- 12 golden fixtures covering both callsite shapes
- `StructuralCtx` field nil-only in M0.5 (Option D — defer cycle resolution)
- `RawMemoryBlob` interim field for the memory bundle path
- `RenderedSkill` declared as stub type (Skills still rendered by existing `# Skill` section)
- Benchmark target `Render() <= 2x` baseline
- Golden fixtures excluded from 400-LoC budget (operator confirms)

Open question for proposal/design:
- **Q-M05-1**: golden fixtures in/out of LoC budget?
- **Q-M05-2**: confirm Option D (defer StructuralCtx cycle resolution) vs Option B (interface)?

---

## 15. Skill resolution

No project-specific skill registry. Standard SDD phase skills apply per `sdd-init/2026`. Apply phase will need:
- go-testing (table-driven + golden files)
- testing-quality (benchmark + tolerance)

`skill_resolution: none` — standard SDD skills apply.
