# Tasks: priorcontext-struct-refactor (M0.5)

## Review Workload Forecast

| Field | Value |
|---|---|
| Estimated changed lines (code+tests) | 480–560 |
| Estimated golden fixture data (inert) | ~500 |
| Total LoC including fixtures | ~1 010 |
| 400-line budget risk (excluding fixtures) | Medium (over 400 if exec strict; acceptable per V4.1 refactor scope) |
| Chained PRs recommended | No — single repo, single milestone, hard internal dependency (baseline→struct→migrate→snapshot) |
| Suggested split | None |
| Delivery strategy | ask-on-risk |
| Decision needed before apply | No (forecast within tolerance + golden fixtures excluded per operator decision) |
| Chain strategy | stacked-to-main (cached; not used for single PR) |
| Notes | Single PR; render-at-boundary keeps downstream signatures unchanged; baseline-capture-FIRST is a process invariant tasks make explicit |

Decision needed before apply: No
Chained PRs recommended: No
Chain strategy: stacked-to-main
400-line budget risk: Medium

## Cross-repo PR strategy

None — single-repo change. No same-commit-pair.

## Locked design decisions absorbed

1. StructuralContextRef as empty struct (D-M05-2)
2. RawMemoryBlob interim field (D-M05-3)
3. Baseline-capture-FIRST as hard prerequisite (D-M05-5)
4. RenderOpts{} zero-value no-op tested explicitly (D-M05-6)
5. Render() deterministic — no time.Now() or rand (D-M05-7)
6. Stub types co-located in `prior_context.go` (D-M05-8)

## Spec requirements covered

| Req | Spec capability | Tasks |
|-----|----------------|-------|
| PriorContext struct declaration | priorcontext-struct | B.9 |
| Render method with no-op zero-value guarantee | priorcontext-struct | B.1–B.7, B.10 |
| Godoc coverage | priorcontext-struct | B.11 |
| TDD red-before-green | priorcontext-struct | B.1–B.8 → B.9–B.10 |
| Migrate buildPriorContext body | priorcontext-phase-service-migration | D.5 |
| Render-at-boundary no downstream change | priorcontext-phase-service-migration | E.7, G.1 |
| Snapshot tests red before migration | priorcontext-phase-service-migration | D.1–D.4 |
| Migrate loadPriorContext body | priorcontext-apply-migration | E.4 |
| Migrate refreshApplyProgress body | priorcontext-apply-migration | E.5 |
| Exhaustive consumer enumeration | priorcontext-apply-migration | G.1–G.2 |
| 12 golden fixture files | priorcontext-snapshot-golden | A.4, C.1–C.5 |
| GOLDEN_UPDATE env-var pattern | priorcontext-snapshot-golden | A.4, C.2 |
| Deterministic fixtures (frozen Clock/IDGenerator) | priorcontext-snapshot-golden | A.4 |
| Baseline captured before any production change | priorcontext-snapshot-golden | A.1–A.6 |
| 4 benchmarks at canonical path | priorcontext-benchmark | F.1 |
| Render() <= 2x baseline latency | priorcontext-benchmark | F.2–F.3 |
| Benchmark output in PR body | priorcontext-benchmark | F.3 |
| TDD — benchmark file red before Render() | priorcontext-benchmark | F.1 → B.9–B.10 |

---

## Task groups (strict dependency order)

### Group A — Baseline capture FIRST (hard prerequisite; gates ALL subsequent groups)

- [x] A.1 Confirm `git status` is clean (no unstaged changes, no new files) — STOP if dirty; resolve before continuing
- [x] A.2 Read and verify the 3 construction callsites are pristine pre-refactor: `internal/application/phase/service.go` around line 960 (`buildPriorContext`), `internal/application/apply/run.go` around line 807 (`refreshApplyProgress`) and line 844 (`loadPriorContext`)
- [x] A.3 Create directory `internal/application/discipline/testdata/priorcontext/` (add `.gitkeep` placeholder if tooling requires non-empty dir)
- [x] A.4 Write golden-capture test driver: file `internal/application/discipline/prior_context_golden_capture_test.go` — exercises the CURRENT inline paths for all 12 scenarios using frozen `Clock` and fixed `IDGenerator`; uses `writeGolden` from `prompt_builder_test.go:371-394` when `GOLDEN_UPDATE=1`; run `GOLDEN_UPDATE=1 go test ./internal/application/discipline/...` to capture all 12 `.golden.txt` baselines into `testdata/priorcontext/`
- [x] A.5 Verify all 12 golden files exist and are non-zero: `phase_empty_memory_bundle.golden.txt`, `phase_single_memory_record.golden.txt`, `phase_multi_memory_record.golden.txt`, `phase_memory_with_unicode.golden.txt`, `phase_memory_error_returns_empty.golden.txt`, `apply_spec_only.golden.txt`, `apply_design_only.golden.txt`, `apply_both_no_progress.golden.txt`, `apply_full_three_sections.golden.txt`, `apply_progress_refresh.golden.txt`, `apply_progress_error_fail_soft.golden.txt`, `apply_empty_returns_empty.golden.txt`
- [x] A.6 Commit: `test(discipline): capture PriorContext baseline goldens (M0.5 pre-refactor)` — standalone commit so review can confirm bytes were captured from pre-refactor code
- [x] A.7 CHECKPOINT — operator confirms baseline commit is present and clean before any Group B work begins

### Group B — PriorContext struct + 8 stub types + RenderOpts + Render (depends on A)

- [x] B.1 (RED) Write `internal/application/discipline/prior_context_test.go` — test `TestRenderOpts_ZeroValue_IsNoOp`: `PriorContext{}.Render(RenderOpts{}) == ""`; fails because `prior_context.go` does not exist yet
- [x] B.2 (RED) Add test: `PriorContext{RawMemoryBlob: "x"}.Render(RenderOpts{}) == "x"` (blob-only path byte-exact)
- [x] B.3 (RED) Add test: `PriorContext{PhaseIdentity: "y"}.Render(RenderOpts{}) == "y"` (identity-only path byte-exact)
- [x] B.4 (RED) Add test: `PriorContext{PhaseIdentity: "y", RawMemoryBlob: "x"}.Render(RenderOpts{})` emits PhaseIdentity first then RawMemoryBlob — asserts deterministic order explicitly
- [x] B.5 (RED) Add test: `Render(RenderOpts{TokenBudget: 0})` is no-op — output equals control string (unlimited)
- [x] B.6 (RED) Add test: `Render(RenderOpts{TokenBudget: 5})` truncates output to 5 bytes
- [x] B.7 (RED) Add test: `Render(RenderOpts{EnableAttribution: false})` emits no `##` headers injected by Render (Render is pass-through; section headers live inside the field values, not added by Render)
- [x] B.8 (RED) Add test: all 9 struct fields serialize round-trip via `json.Marshal`/`json.Unmarshal` — 8 stub types produce `{}` in JSON
- [x] B.9 (GREEN) Create `internal/application/discipline/prior_context.go`: `PriorContext` struct with 9 fields in V4.1 §16 order + 8 exported stub types (`RenderedSkill`, `StructuralContextRef`, `EpisodeRef`, `ChangeDigestRef`, `RuleRef`, `RoutineOutput`, `AuxiliaryBlock`) + `RenderOpts` struct — single file per D-M05-8
- [x] B.10 (GREEN) Implement `(pc PriorContext) Render(opts RenderOpts) string` per D-M05-7 determinism contract: writes PhaseIdentity then RawMemoryBlob verbatim; guards each M3 layer with empty/nil check; applies `TokenBudget` truncation only when `> 0`; no time, no rand, no env
- [x] B.11 Add godoc to `PriorContext` (field-level + type-level), `Render`, `RenderOpts`, `RawMemoryBlob` (explicitly marks M0.5-interim with M3 decomposition path per D-M05-3), `StructuralContextRef` (opaque marker per D-M05-2)
- [x] B.12 Verify: `go test ./internal/application/discipline/...` green; `golangci-lint run ./internal/application/discipline/...` clean

### Group C — Snapshot tests with goldens (depends on B; goldens from A must exist)

- [x] C.1 (RED) Extend `prior_context_test.go` with `TestPriorContext_Render_Goldens` table-driven test: 12 cases per design §Snapshot test driver shape; reads each `.golden.txt` via `readGolden` helper (reused from `prompt_builder_test.go:371-394` — DO NOT duplicate); `require.Equal(t, want, got)` byte-exact — initially RED because goldens were captured from inline path and struct is new
- [x] C.2 Confirm `readGolden`/`writeGolden`/`updateGolden` are NOT duplicated — import or call from `prompt_builder_test.go:371-394`; if helpers are unexported, extract them to a shared `testhelper_golden_test.go` file in the `discipline` package only
- [x] C.3 Verify 5 phase-service cases in the table: `phase_empty_memory_bundle`, `phase_single_memory_record`, `phase_multi_memory_record`, `phase_memory_with_unicode`, `phase_memory_error_returns_empty`
- [x] C.4 Verify 7 apply cases in the table: `apply_spec_only`, `apply_design_only`, `apply_both_no_progress`, `apply_full_three_sections`, `apply_progress_refresh`, `apply_progress_error_fail_soft`, `apply_empty_returns_empty`
- [x] C.5 (GREEN) All 12 snapshot tests green — `Render()` output must be byte-exact match of pre-refactor baselines captured in Group A

### Group D — phase-service callsite migration (depends on B + C green)

- [x] D.1 (RED) Write unit test in `internal/application/phase/` (or `service_test.go` extension): `buildPriorContext` on empty bundle returns `""`
- [x] D.2 (RED) Add test: `buildPriorContext` on N records produces N×(`content` + `"\n\n"`) concatenated — same byte sequence as pre-refactor
- [x] D.3 (RED) Add test: `buildPriorContext` on memory error returns `""`
- [x] D.4 Re-run 5 phase-service golden tests from Group C — must still pass (no code changed yet; confirms test isolation)
- [x] D.5 (GREEN) Migrate `internal/application/phase/service.go:buildPriorContext` body: replace `strings.Builder` loop with `pc := discipline.PriorContext{RawMemoryBlob: sb.String()}; return pc.Render(discipline.RenderOpts{})` — function signature `func (s *Service) buildPriorContext(...) string` unchanged
- [x] D.6 Verify: `go test ./internal/application/phase/...` green + 5 phase-service golden tests still green; `golangci-lint run ./internal/application/phase/...` clean

### Group E — apply callsite migration (depends on B + C green)

- [x] E.1 (RED) Write unit tests in `internal/application/apply/` (or `run_test.go` extension): `loadPriorContext` spec-only, design-only, both-no-progress, neither-returns-empty
- [x] E.2 (RED) Add tests: `refreshApplyProgress` success path (appends section); fail-soft path (returns base unchanged on memory error)
- [x] E.3 Re-run 7 apply golden tests from Group C — must still pass (no code changed yet)
- [x] E.4 (GREEN) Migrate `internal/application/apply/run.go:loadPriorContext` body (around line 844): replace inline section-assembly with `pc := discipline.PriorContext{PhaseIdentity: assembled}; return pc.Render(discipline.RenderOpts{}), nil` — return type `(string, error)` unchanged
- [x] E.5 (GREEN) Migrate `internal/application/apply/run.go:refreshApplyProgress` body (around line 807): construct `discipline.PriorContext{PhaseIdentity: assembled}` with `base + "\n\n" + section`; call `pc.Render(discipline.RenderOpts{})` — signature `func (..., base string) string` unchanged
- [x] E.6 Verify: `go test ./internal/application/apply/...` green + 7 apply golden tests still green; `golangci-lint run ./internal/application/apply/...` clean
- [x] E.7 Confirm downstream chain compiles without signature changes: verify `runAllGroups`, `runTeamLead`, `runImplementWithRetry`, `dispatchImplement`, `dispatchImplementWithOverride`, `runGroupBuildFeedbackLoop` — all still receive `string`; `go build ./internal/application/apply/...` green

### Group F — Benchmarks (depends on B.9–B.10 green)

- [x] F.1 (RED then GREEN) Write `internal/application/discipline/prior_context_bench_test.go` with exactly 4 benchmarks: `BenchmarkPriorContext_Render_PhaseService`, `BenchmarkPriorContext_Render_ApplyThreeSections`, `BenchmarkInlineConcat_PhaseService`, `BenchmarkInlineConcat_ApplyThreeSections` — file is RED when `prior_context.go` absent, GREEN after B.9–B.10
- [x] F.2 Run `go test -bench=. -benchmem -count=10 ./internal/application/discipline/...` and capture raw output
- [x] F.3 Manually verify each `BenchmarkPriorContext_Render_*` median ns/op is ≤ 2× its `BenchmarkInlineConcat_*` counterpart; record both numbers and the computed ratio in the PR body as a fenced code block
- [x] F.4 Confirm NO CI gate is added for benchmarks — flaky on shared runners per design decision

### Group G — Cross-cutting verification (depends on D + E complete)

- [x] G.1 Run `rg -n 'PriorContext' internal/application/{phase,apply,discipline}` — confirm only the 3 construction callsites (`service.go:buildPriorContext`, `run.go:loadPriorContext`, `run.go:refreshApplyProgress`) construct the struct; `teamlead.go:389,488` are read-only `string` assignments (not migration targets)
- [x] G.2 Confirm 0 callsites use inline string concatenation to build prior-context content; every path passes through `PriorContext.Render()`
- [x] G.3 Confirm godoc present on `PriorContext`, `Render`, `RenderOpts`, `RawMemoryBlob` — run `go doc ./internal/application/discipline/` and verify
- [x] G.4 Run `go test ./...` from repo root — full suite green
- [x] G.5 Run `golangci-lint run` (equivalent: `make lint`) — zero warnings; `forbidigo`, `wrapcheck`, `errorlint` clean
- [x] G.6 Inspect commits: no `// removed` orphan comments; no `Co-Authored-By`; no AI attribution; no `time.Now()` or `ulid.Make()` in production code
- [x] G.7 Verify commit message format: `feat(discipline): introduce PriorContext struct and Render method (M0.5)` and `refactor(phase,apply): migrate buildPriorContext and loadPriorContext to PriorContext.Render (M0.5)` — conventional commits only
- [ ] G.8 FINAL CHECKPOINT — operator approves PR; mark M0.5 done

---

## Strict TDD discipline

Every GREEN task preceded by a RED task. Tests-first is non-negotiable. Group A (baseline capture) is the OUTER prerequisite that gates all subsequent work — no code may be written before Group A commit is in.

Sequence enforced by group ordering:
```
A (baseline capture + commit) →
  B (RED tests → struct GREEN) →
    C (snapshot tests GREEN) →
      D (RED unit tests → phase migration GREEN) →
      E (RED unit tests → apply migration GREEN) →
    F (benchmarks RED→GREEN) →
  G (cross-cutting verification + final checkpoint)
```

D and E can proceed in parallel once B and C are green.
F can proceed as soon as B.9–B.10 are green (does not require D or E).

---

## Out of scope reminders

- StructuralCtx wiring (M3)
- Skills moving from sibling `# Skill` section into `PriorContext` (M3)
- Token-budget enforcement beyond byte truncation (M3)
- Source attribution rendering (M3)
- Episodes / ChangeDigests / BusinessRules / Routines population (M3)
- Memory-engine API changes (consumer-side only)
- Downstream chain signature changes (render-at-boundary preserves)
- `RawMemoryBlob` decomposition into structured layers (M3)
