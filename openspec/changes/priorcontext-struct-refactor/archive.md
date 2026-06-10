# Archive Report: priorcontext-struct-refactor (M0.5)

**Change**: priorcontext-struct-refactor
**Archived**: 2026-06-08
**Mode**: openspec + Engram (hybrid)
**Verification verdict**: PASS (0 CRITICAL, 0 WARNING, 5 SUGGESTION)
**Strategy doc**: V4.1 §16 M0.5 (Q-H2 resolution from V4 → V4.1 amendment)

## Intent

M0.5 delivered a pure refactor: replaced two inline string-concatenation callsites (`phase/service.go:buildPriorContext` and `apply/run.go:loadPriorContext` + `refreshApplyProgress`) with a single canonical `discipline.PriorContext` struct and deterministic `Render(RenderOpts) string` method. Byte-exact output preservation against pre-refactor golden fixtures is proven by 12 snapshot tests. This refactor is a structural prerequisite for M3 enrichment — without it, M3 would have nowhere to write the structured enrichments (skills, episodes, digests, routines); with it, M3 can populate PriorContext fields and leave Render layer assembly unchanged.

## Capabilities delivered (5)

| Capability | Status | Where (main HEAD c1eb0f2) |
|---|---|---|
| priorcontext-struct | DELIVERED | internal/application/discipline/prior_context.go:13–160 |
| priorcontext-phase-service-migration | DELIVERED | internal/application/phase/service.go:991–992 (buildPriorContext) |
| priorcontext-apply-migration | DELIVERED | internal/application/apply/run.go:829–830 (loadPriorContext) + :807–831 (refreshApplyProgress) |
| priorcontext-snapshot-golden | DELIVERED | internal/application/discipline/testdata/priorcontext/*.golden.txt (12 fixtures) |
| priorcontext-benchmark | DELIVERED | internal/application/discipline/prior_context_bench_test.go (4 benchmarks) |

## PR landed (1 PR, 3 commits)

| PR | Merged | Commits | Notes |
|---|---|---|---|
| sophia-orchestrator#80 | 2026-06-08T20:25:54Z | 8d28998, b011ace, f8b2adb | 174 LoC production + 954 test/bench + 500 golden (inert) |

Main HEAD after merge: `c1eb0f2 Merge pull request #80 from RVRTelecomunicaciones/feat/priorcontext-struct-refactor`.

## Operator-locked decisions (8 design + 11 proposal)

Recap each with evidence in merged code:

1. **Approach A (concrete struct in discipline)** — `prior_context.go:13–55` defines `PriorContext` struct with 9 fields in V4.1 §16 order
2. **Option D for StructuralContextRef (empty struct, nil-only)** — `prior_context.go:61–64` declares `type StructuralContextRef struct{}`, always nil in M0.5; `Render` body at `:135–144` guards with `if pc.StructuralCtx != nil { ... }` (unreachable, documents M3 wiring point)
3. **Golden fixtures excluded from 400-LoC budget** — operator confirmed; 12 `.golden.txt` files (~500 LoC inert data) are not counted against PR size
4. **Skills sibling section unchanged** — `prompt_builder.go:97–108` renderers intact; `PriorContext.Skills []RenderedSkill` empty in M0.5, `Render` skips it per `:218` comment
5. **RawMemoryBlob interim field** — `prior_context.go:47–54` godoc explicitly marks "M0.5-interim: unstructured memory bundle ... M3 will decompose into Episodes / ChangeDigests / BusinessRules and remove this field"
6. **Render-at-boundary preserves 6 downstream signatures** — `apply/teamlead.go:389,488` read string assignments (unchanged); `runAllGroups`, `runTeamLead`, `runImplementWithRetry`, `dispatchImplement`, `dispatchImplementWithOverride`, `runGroupBuildFeedbackLoop` all still receive `string` parameter
7. **12 golden fixtures (5 phase + 7 apply)** — all exist at canonical path `internal/application/discipline/testdata/priorcontext/*.golden.txt`; baseline captured in commit `8d28998` before struct existed
8. **Benchmark target Render() ≤ 2x** — EXCEEDED: phase Render 0.34× baseline, apply Render 0.15× baseline (3–6× FASTER)
9. **RenderOpts{} zero-value no-op** — `prior_context_test.go:21–25` `TestRenderOpts_ZeroValue_IsNoOp` explicitly asserts empty struct path produces `""` (blob-only test at `:32–39` asserts `RawMemoryBlob: "x"` renders as `"x"`); token budget zero guard at `prior_context.go:154`; attribution zero guard at `:157`
10. **Render deterministic (no time/rand/env)** — `rg 'time\.Now|rand\.|os\.Getenv|os\.Environ' prior_context.go` returns 0 hits; only `strings` imported
11. **Stub types co-located in single file** — `prior_context.go:57–84` all 8 stub types (`RenderedSkill`, `StructuralContextRef`, `EpisodeRef`, `ChangeDigestRef`, `RuleRef`, `RoutineOutput`, `AuxiliaryBlock`) declared inline; D-M05-8 single-file co-location wins

## Adaptations approved during apply

1. **Baseline capture committed FIRST as separate commit** (`8d28998`) — protects byte-exact contract per D-M05-5. Goldens captured from PRE-refactor inline code path; no struct exists at this commit. This is the outer RED gate.
2. **Two commits for struct+tests vs callsite migration** — clean separation between "introduce struct" (`b011ace`) and "migrate callsites" (`f8b2adb`). Review can confirm byte-exact reproduction between the two.
3. **golangci-lint pre-push** (INIT-0 lesson #1 applied) — 0 lint surprises in CI. PR shows 8/8 checks SUCCESS from first push.

## CI status

PR #80 8/8 checks SUCCESS at merge:

- Lint ✅
- Wire-contract matrix ✅
- Unit tests ✅
- Integration tests (Postgres) ✅
- govulncheck ✅
- Build binary ✅
- Docker image ✅
- GitGuardian Security Checks ✅

No CI fix journey — clean from first push (INIT-0 lessons applied successfully: `make test-unit` + `make lint` pre-push).

## Performance bonus discovery

Render() benchmark is **3–6× FASTER** than inline concatenation baseline (exceeded 2× ceiling by margin):

- PhaseService: Render 0.34× baseline (3× faster)
- ApplyThreeSections: Render 0.15× baseline (6.7× faster)

Refactor is free in performance. Struct concentrates allocations to a single `strings.Builder` write, whereas inline paths used multiple `fmt.Sprintf` + Builder grow cycles.

## Verification artifact references

**All verification artifacts persisted and accessible**:

- Proposal: `sdd/priorcontext-struct-refactor/proposal` (Engram)
- Specs (5 capabilities): `sdd/priorcontext-struct-refactor/spec` (Engram)
- Design: `sdd/priorcontext-struct-refactor/design` (Engram)
- Tasks: `sdd/priorcontext-struct-refactor/tasks` (Engram)
- Apply-progress: `sdd/priorcontext-struct-refactor/apply-progress` (Engram, observation #797)
- Verify-report: `sdd/priorcontext-struct-refactor/verify-report` (Engram)
- Files on disk: `/Users/russell/Documents/2026/sophia-orchestator/openspec/changes/priorcontext-struct-refactor/` (hybrid mode)

## Process lessons reinforced

1. **`make test-unit` ≠ `make lint`** — run both pre-push. INIT-0 lesson #1. This PR applied it and had 0 lint surprises.
2. **Baseline-capture-FIRST as separate commit** — protects byte-exact refactor contracts. Commit `8d28998` is the outer RED gate; struct and callsite migrations happen AFTER.
3. **Render-at-boundary keeps downstream signatures stable** — minimizes blast radius. 6 methods stay unchanged; review surface is clear.
4. **Empty test fixture dirs need `.gitkeep`** — INIT-0 lesson #2. Applied via `testdata/priorcontext/.gitkeep`.
5. **Strict TDD discipline enforced across 3 commits** — every production line preceded by failing test. Baseline goldens captured BEFORE struct, so refactor must reproduce byte-exact (it does: 12/12 pass).

## Forwarded to M3 (5 SUGGESTIONS from verify)

1. **StructuralCtx wiring**: M3 resolves the `application/init → discipline` cycle (Option B interface or Option A move-to-domain) and populates `StructuralContextRef` (currently nil-only opaque marker)
2. **Skills migration into PriorContext**: M3 moves skill rendering from sibling `# Skill` section (`prompt_builder.go:97–108`) into `PriorContext.Skills + Render` layer
3. **RawMemoryBlob decomposition**: M3 decomposes the interim blob into `Episodes`, `ChangeDigests`, `BusinessRules` proper layers with structured types
4. **Downstream chain receives *PriorContext**: M3 may pass struct further into `runAllGroups` / `runTeamLead` etc. (currently render-at-boundary preserves `string` signatures)
5. **RenderOpts.{TokenBudget, EnableAttribution} implementation**: M3 wires real token-budget enforcement and source-attribution rendering

## V4.1 status update

**Mark M0.5 as DONE.**

Change closed. Struct landed. Byte-exact snapshot tests prove refactor. All design decisions locked and shipped. No risks for M0.5 scope. Verification PASS.

Next milestone in chain: **M1 (skills schema migration 010 + lifecycle + matcher)** — extends the live `skills` table with `status`/`version`/`scope`/`applies_when`/`risk_level`/`activation_source`/`metrics`, backfills 9 legacy seeds, ships `SkillsForContext` matcher.

After M1: M2 (consolidation worker), then M3 (PriorContext enrichment consuming StructuralContext + skills + episodes + digests).

## SDD cycle complete

Explore → Propose → Spec → Design → Tasks → Apply → Verify → Archive ✅

---

**Archived by**: claude-code (SDD Archive executor)
**Timestamp**: 2026-06-08T20:30:00Z
**Result**: PASS — change closed, all artifacts persisted
