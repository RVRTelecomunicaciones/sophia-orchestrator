# Proposal: priorcontext-struct-refactor (M0.5)

## Intent

Pure refactor: replace the two inline string-concatenation callsites that today assemble the prior-context blob with a single canonical `discipline.PriorContext` struct + `Render(RenderOpts) string` method. Render-at-boundary preserves all downstream signatures and produces byte-exact output against pre-refactor goldens. References V4.1 §16 milestone M0.5 acceptance criteria. The "wired but unused" forward-compat fields (`Skills`, `StructuralCtx`, `Episodes`, `ChangeDigests`, `BusinessRules`, `Routines`, `Auxiliary`) land now so M3 enrichment has somewhere to write. Without this change, M3 (PriorContext enrichment with skills/episodes/digests/routines) has no struct to enrich; M1 SkillMatcher has no formalized integration point for its output.

## Scope

### In Scope (1 PR — production+test under 400-LoC budget)
- NEW `internal/application/discipline/prior_context.go` (~150 LoC): `PriorContext` struct + 8 supporting types (`RenderedSkill`, `StructuralContextRef`, `EpisodeRef`, `ChangeDigestRef`, `RuleRef`, `RoutineOutput`, `AuxiliaryBlock`, `RenderOpts`) + `Render(opts RenderOpts) string` method
- MODIFIED `internal/application/phase/service.go:960` `buildPriorContext` body — construct struct with `RawMemoryBlob` populated from the memory-engine bundle, then call `pc.Render(RenderOpts{})`. Public signature unchanged (still returns `string`).
- MODIFIED `internal/application/apply/run.go:844` `loadPriorContext` + `run.go:807` `refreshApplyProgress` bodies — build struct with spec/design/progress named sections, then `Render`. Public signatures unchanged.
- NEW `internal/application/discipline/prior_context_test.go` (~200 LoC): 12 golden-fixture snapshot tests + explicit unit test asserting `RenderOpts{}` (zero-value) is a strict no-op
- NEW `internal/application/discipline/testdata/priorcontext/*.golden.txt` (12 files, ~500 LoC inert data — EXCLUDED from PR budget per operator decision #3)
- NEW `internal/application/discipline/prior_context_bench_test.go` (~80 LoC): 4 benchmarks (2 `Render()` + 2 inline baselines) proving `Render() <= 2x` baseline latency

### Out of Scope (M3 and beyond)
- `StructuralCtx` wiring — Option D (explore.md §4): field declared as opaque `*StructuralContextRef` marker, always `nil` in M0.5, `Render()` skips it. NO touching of INIT-0 detector code.
- Skills moving from sibling `# Skill` section (`prompt_builder.go:97-108`) into `PriorContext` — `Skills []RenderedSkill` stays empty in M0.5 and `Render()` skips it. Operator decision #4.
- Token-budget enforcement — `RenderOpts.MaxTokens` field declared but disabled when zero (operator decision #9)
- Source attribution — `RenderOpts.IncludeSources` field declared but disabled when zero (operator decision #9)
- Episode / change-digest / business-rule / routine population — M3 enrichment job
- Downstream chain signature changes (`runAllGroups`, `runTeamLead`, `runImplementWithRetry`, `dispatchImplement`, `dispatchImplementWithOverride`, `runGroupBuildFeedbackLoop`) — render-at-boundary preserves them as-is (operator decision #6)
- Memory-engine API changes — consumer-side refactor only
- `RawMemoryBlob` decomposition into proper `Episodes`/`ChangeDigests`/`BusinessRules` layers — M3 job; M0.5 documents this interim field explicitly (operator decision #5)

## Capabilities

### New Capabilities
- `priorcontext-struct`: `PriorContext` struct + 8 stub supporting types + `RenderOpts` + `Render()` method with godoc
- `priorcontext-phase-service-migration`: `internal/application/phase/service.go:buildPriorContext` migrated to struct + render-at-boundary; downstream `string` signature preserved
- `priorcontext-apply-migration`: `internal/application/apply/run.go:loadPriorContext` + `refreshApplyProgress` migrated to struct + render-at-boundary; downstream `string` signature preserved
- `priorcontext-snapshot-golden`: 12 golden fixtures + reuse of `GOLDEN_UPDATE=1` pattern from `prompt_builder_test.go:371-394`
- `priorcontext-benchmark`: 4 benchmarks proving `Render() <= 2x` inline-concat baseline

### Modified Capabilities
- None at the spec level. This is a pure internal refactor: public callsite signatures and rendered output are preserved byte-exact.

## Approach (high-level)

1. **Baseline-capture FIRST** (strict TDD — failing test first). Run the existing inline-concatenation path with deterministic fixtures (frozen `Clock`, frozen `IDGenerator`), dump output to `testdata/priorcontext/*.golden.txt`. These are the "before" snapshots. Tests for the new `Render()` initially read these but `Render()` does not exist → red.
2. **Create struct + Render() in `discipline`** package. Reproduce the goldens byte-exact. `RenderOpts` zero-value path emits identical output (no headers added, no token truncation, no source attribution). Tests go green.
3. **Migrate callsite #1** (`phase/service.go:960`): replace `strings.Builder` loop with `pc := discipline.PriorContext{RawMemoryBlob: assembled}; return pc.Render(discipline.RenderOpts{})`. Re-run snapshots — byte-exact match expected.
4. **Migrate callsites #2+3** (`apply/run.go:844` + `:807`): replace inline `## spec` / `## design` / `## Recent progress` builders with struct construction populating named-section fields, then `Render(RenderOpts{})`. Re-run snapshots.
5. **Add 4 benchmarks**, run `go test -bench`, assert `Render()` is within 2x baseline.
6. **Verify**: `RenderOpts{}` no-op explicit unit test, `go test ./...`, `golangci-lint run` clean (INIT-0 lesson, explore.md §10.5).

Render-at-boundary (operator decision #6) is the architectural lever: only the 2 callsite function bodies change; everything downstream still receives `string`.

## Affected Areas

| Area | Impact | Description |
|------|--------|-------------|
| `internal/application/discipline/prior_context.go` | NEW | Struct + Render + 8 supporting types (~150 LoC) |
| `internal/application/discipline/prior_context_test.go` | NEW | 12 snapshot tests + RenderOpts zero-value no-op test (~200 LoC) |
| `internal/application/discipline/prior_context_bench_test.go` | NEW | 4 benchmarks (~80 LoC) |
| `internal/application/discipline/testdata/priorcontext/*.golden.txt` | NEW | 12 fixtures, ~500 LoC inert data (excluded from PR budget) |
| `internal/application/phase/service.go:960` | MODIFIED | `buildPriorContext` body only; signature unchanged (~30 LoC delta) |
| `internal/application/apply/run.go:844` | MODIFIED | `loadPriorContext` body only; signature unchanged (~25 LoC delta) |
| `internal/application/apply/run.go:807` | MODIFIED | `refreshApplyProgress` body only; signature unchanged (~25 LoC delta) |
| `internal/application/discipline/prompt_builder.go` | NO CHANGE | Skills stay in their own `# Skill` section |
| `internal/application/apply/teamlead.go` | NO CHANGE | Render-at-boundary preserves `string` plumbing |
| `internal/application/apply/build_feedback.go` | NO CHANGE | Render-at-boundary preserves `string` plumbing |
| `internal/application/init/detector` | NO CHANGE | Option D defers `StructuralContext` cycle to M3 |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| Byte-exact preservation breaks on whitespace/separator drift | High | 12 golden fixtures; capture baselines BEFORE any code change (explore.md §10.1) |
| `StructuralContext` import cycle | Resolved | Option D — nil-only opaque marker in M0.5; M3 wires (operator decision #2) |
| `RenderOpts{}` zero-value silently activates a hook | High | Explicit unit test asserting zero-value path produces identical output to a control inline-concat case (operator decision #9) |
| `RawMemoryBlob` interim field deviates from V4.1 final shape | Medium | Field godoc explicitly documents "M0.5-interim; M3 decomposes into Episodes/ChangeDigests/BusinessRules" (operator decision #5, #8) |
| Render-at-boundary misses a 3rd callsite | Medium | `rg 'PriorContext' internal/application/{phase,apply,discipline}` in spec phase to enumerate ALL consumers; verify `service.go`, `run.go`, `build_feedback.go` chain is exhaustive |
| `golangci-lint v2.12` stricter than local | Medium | Run `make lint` before push (INIT-0 lesson, explore.md §10.5) |
| Benchmark variance on CI | Low | 2x ratio band is wide enough to absorb runner noise |

## Rollback Plan

Revert the single PR. `phase/service.go:buildPriorContext` and `apply/run.go:loadPriorContext`+`refreshApplyProgress` return to inline concatenation. No data migration. No schema change. No API contract change. Golden fixture files become orphan and can be left for M3 reuse or deleted in the same revert commit.

## Dependencies

- None blocking. M0.5 is a self-contained internal refactor of `internal/application/discipline` + 2 callsite bodies.
- Downstream M3 (PriorContext enrichment) depends on this landing — the struct created here is the integration point M3 writes into.

## Success Criteria (V4.1 §16 verbatim + operator additions)

- [ ] Snapshot tests green on ≥10 fixtures (we ship 12)
- [ ] Byte-exact output preservation vs pre-refactor baseline goldens
- [ ] Zero callsites use inline concatenation; all paths go through `pc.Render()`
- [ ] `PriorContext` struct and `Render()` method documented via godoc
- [ ] Benchmark: `Render() <= 2x` latency of inline concatenation baseline
- [ ] `go test ./...` green
- [ ] `golangci-lint run` clean (operator decision — INIT-0 lesson)
- [ ] `RawMemoryBlob` field godoc explicitly marks it as M0.5-interim with M3 decomposition path (operator decision #8)
- [ ] `RenderOpts{}` zero-value no-op asserted by explicit unit test (operator decision #9)

## Open Questions

None. All 11 operator decisions are locked:
1. Approach A (concrete struct in `discipline`, render-at-boundary)
2. Option D for `StructuralCtx` (nil-only opaque marker, M3 wires)
3. Golden fixtures excluded from 400-LoC PR budget
4. Skills stay in separate `# Skill` section; `Skills` field empty/skipped in M0.5
5. Single struct with `RawMemoryBlob` interim field for phase-service path
6. Render-at-boundary; downstream chain unchanged
7. Snapshot tests are PRIME directive — 12 fixtures, byte-exact
8. Benchmark `Render() <= 2x` baseline
9. `RenderOpts{}` zero-value is no-op with explicit test
10. Conventional commits, NO Co-Authored-By, NO AI attribution
11. Strict TDD — failing test first

## Strict TDD Note

`strict_tdd: true` is active for this project. `sdd-spec` MUST define test-first acceptance per capability with explicit red→green→refactor markers. `sdd-apply` MUST follow `strict-tdd.md`: baseline golden capture (red) → struct + Render to match goldens (green) → callsite migration (green continues) → benchmarks (red→green for the 2x bound). No production code is written before its failing test exists.
