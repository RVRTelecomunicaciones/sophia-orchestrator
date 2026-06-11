# Tasks: priorcontext-enrichment (M3)

## Review Workload Forecast

| PR | Estimated LoC (prod + test) | 400-line budget risk | Notes |
|----|----------------------------|----------------------|-------|
| PR1 (orch + ME) | ~170 LoC | Low | Handler + service + route (~50) + ME reconcile (~30) + tests (~90) |
| PR2 (orch) | ~280 LoC | Low-Medium | Domain move + aliases + AppliesWhen fields (C-2) + matcher filter + tests (~160 test churn) |
| PR3 (orch) | ~810 LoC | **High — split required** | ~460 prod + ~350 test (14 golden re-baselines + 7 new fixtures + 4 fake migrations + benchmark) |

Decision needed before apply: Yes
Chained PRs recommended: Yes
Chain strategy: stacked-to-main
400-line budget risk: High

### PR3 Split Decision

PR3 exceeds the ~700 LoC contingency threshold. Split as follows:

| Work Unit | Scope | Likely PR | Notes |
|-----------|-------|-----------|-------|
| PR1 | GetSkill endpoint (orch) + proposer reconcile (ME) | PR1 | Base: main |
| PR2 | StructuralContext domain move + AppliesWhen fields (C-2) + matcher filters | PR2 | Base: PR1 merged to main |
| PR3a | Render enrichment: stub types real + layers + budget + attribution + buildPriorContext decompose (DG-1 + Query=c.Name()) + golden re-baseline + callsite migration (SAME PR — sibling section deletion forces it) | PR3a | Base: PR2 merged to main |
| PR3b | SkillsForPhase retirement: wrapper + port file deletes + RunDeps/ServiceDeps/wire.go rewire + fakes cleanup | PR3b | Base: PR3a merged to main |

**Callsite-migration placement:** Per design §4 caveat, the three SkillsForPhase callsites that set `PromptInput.Skills` MUST migrate to `PriorContext.Skills` IN THE SAME PR AS the sibling section deletion (PR3a). PR3b handles only the wrapper/port file deletes, RunDeps/ServiceDeps rewire, and fake cleanups. PR3b is independently revertable.

---

## Locked decisions absorbed

| Key | Decision |
|-----|----------|
| DG-1 | Digests via `Memory.Search(Types:["semantic"], Limit:3, Query:"change digest")` orch-side. NOT IncludeTypes workaround (C-3 — inert on ME side). No ME retrieval changes. |
| C-1 | Only TWO `*StructuralContextRef` markers: `PriorContext.StructuralCtx` + `SkillQuery.StructuralContext`. `Metrics.LastStackVersion` is already `*string` — NOT in scope. |
| C-2 | PR2 MUST add `Framework []string` + `Language []string` to `skill.AppliesWhen` (JSONB round-trip) BEFORE matcher filter activation. |
| C-3 | `IncludeTypes: ["semantic"]` workaround is inert on ME side (BuildContext never reads it). Formally retired. DG-1 replaces it. |
| D-M3-3 | Type aliases in `init/detector/types.go` for zero-blast-radius transition. `StructuralContextRef` deleted. |
| D-M3-6 | Episodes populate via `ContextRequest.Query = c.Name()` (one-line add to existing BuildContext call). No extra round-trip. |
| D-M3-10 | Baseline-capture-first: FIRST commit in PR3a captures new enriched goldens. No prod changes in that commit. |

---

## PR1 Task Groups (orch + ME)

### Group A — GetSkill endpoint (orch)

Satisfies: `skill-get-endpoint` spec (all 4 requirements).

- [x] A.1 RED — `handlers/skills_test.go`: add contract test marshaling `getSkillResp` fixture → unmarshal into vendored `workerSkillSnapshot` struct matching `skills_client.go:36-42`; assert all 5 top-level + 7 metrics fields equal. Test MUST fail (no handler).
- [x] A.2 RED — add 404 test: `GET /api/v1/skills/does-not-exist` → assert 404 JSON with `error` + `code` fields. MUST fail.
- [x] A.3 RED — add 401 test: `GET /api/v1/skills/{id}` without API-key header → assert 401. MUST fail (route not registered).
- [x] A.4 GREEN — add `getSkillResp` / `getSkillMetrics` DTOs + `toGetSkillResp(*inbound.GetSkillResult)` in `internal/adapters/inbound/http/handlers/skills.go`. Additive `skill_name`/`scope`/`applies_when` omitempty fields per D-M3-1/D-M3-2.
- [x] A.5 GREEN — add `GetSkill(ctx, skillID string) (*inbound.GetSkillResult, error)` to `SkillService` interface in `internal/ports/inbound/skill.go` + impl in `internal/application/skill/service.go` (parse skillID, delegate to `SkillRepo.FindByID`, return `outbound.ErrNotFound` on miss).
- [x] A.6 GREEN — add `r.Get("/", skillH.GetSkill)` inside the existing `r.Route("/{skill_id}", …)` block at `internal/adapters/inbound/http/router.go:143-146`.
- [x] A.7 VERIFY — `go test ./internal/adapters/inbound/http/... ./internal/application/skill/...` green; contract test passes; 404 + 401 pass.

### Group B — Proposal schema reconcile (ME, sophia-memory-engine)

Satisfies: `proposal-schema-reconcile` spec.

- [x] B.1 RED — ME `consolidation/proposer_test.go`: add shape test asserting emitted `SkillActivationProposal` YAML contains `skill_name`, `scope`, `applies_when`, `risk_level` fields. MUST fail (fields absent).
- [x] B.2 RED — add re-emit/append test: process same skill twice, assert `evidence_changes` accumulates + all 9 §9 fields present on second emit. MUST fail.
- [x] B.3 GREEN — add `ProposerSkillView` (embeds `outbound.SkillSnapshot` + `SkillName`, `Scope map[string]any`, `AppliesWhen map[string]any`) in `ports/outbound/skills_client.go` + type alias in `consolidation/proposer.go`. `ProposerSkillsClient` interface in outbound port. `GetSkillWide` on `orchhttp.SkillsClientHTTP`. `NewProposerWithSkills` constructor.
- [x] B.4 GREEN — extend `SkillActivationProposal` struct with `SkillName`, `Scope`, `AppliesWhen`, `RiskLevel` fields + populate from wide fetch in the proposer.
- [x] B.5 VERIFY — ME `go test ./internal/application/consolidation/...` green; storage metadata (topic_key, tags) unchanged per spec.

### Group C — PR1 checkpoint

- [x] C.1 — `go test ./...` green in orch + ME; `golangci-lint run ./...` clean in both repos.
- [ ] C.2 — HARD GATE: operator approval of PR1. WAIT for PR1 merge to main before starting PR2.

---

## PR2 Task Groups (orch)

### Group D — AppliesWhen Framework/Language fields (C-2 prerequisite)

Satisfies: `matcher-structural-filters` spec (prerequisite fields) + design D-M3-4.

- [x] D.1 RED — `domain/skill/lifecycle_test.go`: add JSONB round-trip test for `AppliesWhen` with `Framework: ["nextjs"]` + `Language: ["typescript"]`; assert marshal→unmarshal preserves both fields. MUST fail (fields absent).
- [x] D.2 GREEN — add `Framework []string \`json:"framework,omitempty"\`` and `Language []string \`json:"language,omitempty"\`` to `skill.AppliesWhen` in `internal/domain/skill/lifecycle.go:117-121`.
- [x] D.3 GREEN — update `scanAppliesWhen` in `internal/adapters/outbound/pg/skill_repo.go` to include `Framework`/`Language` keys in the JSONB unmarshal path.
- [x] D.4 VERIFY — `go test ./internal/domain/skill/... ./internal/adapters/outbound/pg/...` green; no schema migration needed (JSONB column).

### Group E — StructuralContext domain move

Satisfies: `structural-context-domain` spec (all 3 requirements + C-1).

- [x] E.1 — Create `internal/domain/structural/context.go`: move `StructuralContext`, `LanguageInfo`, `FrameworkInfo`, `GraphSummary` verbatim from `init/detector/types.go`; rename const to `SchemaV1 = 1`.
- [x] E.2 — Replace `init/detector/types.go` local definitions with type aliases: `type StructuralContext = structural.StructuralContext` (+ LanguageInfo, FrameworkInfo, GraphSummary, `const StructuralContextSchemaV1 = structural.SchemaV1`). `SophiaDetectorVer` stays in `detector`.
- [x] E.3 — Retype `discipline.PriorContext.StructuralCtx` from `*StructuralContextRef` → `*structural.StructuralContext` in `internal/application/discipline/prior_context.go:25`.
- [x] E.4 — Retype `discipline.SkillQuery.StructuralContext` from `*StructuralContextRef` → `*structural.StructuralContext` in `internal/application/discipline/skill_matcher.go:50`.
- [x] E.5 — Delete `type StructuralContextRef struct{}` from `prior_context.go:64`. Confirm zero references with `rg 'StructuralContextRef'`.
- [x] E.6 — Update `prior_context_test.go:110,138` and `skill_matcher_test.go:30,84-95` to construct `*structural.StructuralContext` (or nil) instead of `&StructuralContextRef{}`.
- [x] E.7 — `go build ./...` clean; INIT-0 detector tests green; no import cycles.

### Group F — Matcher structural filters

Satisfies: `matcher-structural-filters` spec (all requirements) + design D-M3-4.

- [x] F.1 RED — `pg/skill_matcher_structural_test.go`: add framework-match test (skill `framework:["nextjs"]` + ctx `Frameworks:["nextjs"]` → included). CONFIRMED FAIL.
- [x] F.2 RED — add framework-mismatch test (skill `framework:["rails"]` + ctx `Frameworks:["nextjs"]` → excluded with `SkipReason = "structural_mismatch"`). CONFIRMED FAIL.
- [x] F.3 RED — add nil-context fail-open test (skill `framework:["rails"]`, `q.StructuralContext = nil` → NOT excluded). CONFIRMED FAIL.
- [x] F.4 RED — add language-match + language-mismatch tests mirroring F.1/F.2. CONFIRMED FAIL.
- [x] F.5 GREEN — added `const SkipReasonStructuralMismatch = "structural_mismatch"` to discipline/skill_matcher.go. Implemented `structuralMatches(aw, q)` + `anyFrameworkPresent` + `anyLanguagePresent` in pg/skill_matcher.go. Inserted filter call after `appliesWhenMatches` and before `MaxRiskLevel` check in `PGSkillMatcher.SkillsForContext`. Case-insensitive via strings.EqualFold.
- [x] F.6 VERIFY — `go test ./internal/adapters/outbound/pg/...` green; all F.1–F.4 scenarios pass (7 tests); existing matcher tests unaffected.

### Group G — PR2 checkpoint

- [x] G.1 — `make test-unit` green (all packages); `golangci-lint run ./...` 0 issues.
- [ ] G.2 — HARD GATE: operator approval of PR2. WAIT for PR2 merge to main before starting PR3a.

---

## PR3a Task Groups (orch — Render enrichment + callsite migration)

### Group H — Baseline capture FIRST (hard gate, no prod changes)

Satisfies: `golden-rebaseline` spec (baseline commit first requirement).

- [ ] H.1 — HARD GATE: this is the FIRST commit in PR3a. Run `GOLDEN_UPDATE=1 go test ./internal/application/discipline/...` to re-capture all 14 existing golden files (`testdata/priorcontext/*.golden.txt` + `testdata/*.golden`) against the enriched output format.
- [ ] H.2 — Commit with: `test(discipline): rebaseline golden snapshots for enriched PriorContext output`. Zero production file changes in this commit.

### Group I — Stub types become real

Satisfies: `priorcontext-skills-layer` spec (RenderedSkill requirement) + `priorcontext-memory-layers` spec (EpisodeRef, RuleRef, ChangeDigestRef requirements).

- [ ] I.1 RED — `prior_context_test.go`: add `TestRenderedSkill_FieldsPopulated` asserting all 6 fields non-empty when mapped from a fixture `skill.Skill`. MUST fail (struct is stub).
- [ ] I.2 RED — add `TestEpisodeRef_FromRecentEpisodic`, `TestRuleRef_FromDecisions`, `TestRuleRef_FromHeuristics`, `TestChangeDigestRef_Populated`. MUST fail.
- [ ] I.3 GREEN — give real fields to `RenderedSkill{Name, Version, Status, Source, Techniques []string, Content}`, `EpisodeRef{ID, Content}`, `ChangeDigestRef{ChangeID, Content}`, `RuleRef{ID, Kind, Content}` in `internal/application/discipline/prior_context.go`.
- [ ] I.4 GREEN — add `toRenderedSkill(*skill.Skill) RenderedSkill` helper in `prior_context.go` (pure, domain→render-shape).
- [ ] I.5 VERIFY — compile + I.1/I.2 tests green.

### Group J — Render() layers + budget + attribution

Satisfies: `render-budget-attribution` spec + `priorcontext-skills-layer` spec (skills precede memory layers) + design D-M3-7/D-M3-8/D-M3-11.

- [ ] J.1 RED — add `TestRender_LayerOrdering`: skills section precedes episodes section. MUST fail.
- [ ] J.2 RED — add `TestRender_NoBlockedSkillRendered`: deprecated/blocked skill in `PriorContext.Skills` is excluded. MUST fail.
- [ ] J.3 RED — add `TestRender_BudgetRespected`: `TokenBudget` set to allow only 1 of 3 skills → truncation marker present. MUST fail.
- [ ] J.4 RED — add `TestRender_AttributionHeaders`: `EnableAttribution=true` → `## Skill: <name> v<version> (active, source=<src>)` present. MUST fail.
- [ ] J.5 RED — add `TestRenderOpts_ZeroValue_IsNoOp`: retained M0.5 contract; all items included, no headers. MUST fail once types change.
- [ ] J.6 GREEN — implement `collectLayers(attr bool) []layerBlock` in `prior_context.go`: Skills → StructuralCtx → Episodes → ChangeDigests → BusinessRules → PhaseIdentity. Skip empty layers. `RawMemoryBlob` NOT collected.
- [ ] J.7 GREEN — implement `enforceBudget(layers []layerBlock, budget int) []layerBlock`: per-layer share (Skills 40%, Episodes 20%, Digests 15%, Rules 15%, PhaseIdentity 10%), redistribute unused, cut-order (related-Episodes first → trailing Episodes → Digests → Rules → Skills → PhaseIdentity last), append truncation marker `\n…[truncated: N {layer} omitted, M bytes over budget]\n`.
- [ ] J.8 GREEN — implement attribution header emission in `renderSkills`/`renderEpisodes`/`renderDigests`/`renderRules` helpers. `## Skill: <name> v<version> (active, source=<src>)` format (D-M3-8). When `EnableAttribution=false`: minimal `## <name>` separator for skills, no headers for other layers.
- [ ] J.9 GREEN — wire `Render(opts RenderOpts)` to call `collectLayers` → `enforceBudget` (if budget>0) → join and return.
- [ ] J.10 VERIFY — J.1–J.5 tests green; `go test ./internal/application/discipline/...` passes.

### Group K — buildPriorContext decomposition + callsite migration + sibling section removal

Satisfies: `priorcontext-memory-layers` spec (RawMemoryBlob removal, typed layers) + `priorcontext-skills-layer` spec (renderSkillSection deleted) + `skillsforphase-retirement` spec (callsites migrate, in PR3a per split caveat).

- [ ] K.1 RED — `phase/service_test.go`: add `TestBuildPriorContext_Decompose` asserting that with a fixture `MemoryClient` returning `decisions`/`heuristics`/`recent_episodic` sections + a fixture digest `Search` result, `PriorContext.Episodes`, `.Rules`, and `.ChangeDigests` are populated. MUST fail.
- [ ] K.2 RED — add assertion that `ContextRequest.Query` is set to `c.Name()` (non-empty) in the BuildContext call. MUST fail.
- [ ] K.3 RED — add assertion: `Memory.Search(SearchQuery{Types:["semantic"], Limit:3})` is called (DG-1 digest path). MUST fail.
- [ ] K.4 RED — rewrite `fakeSkillProvider` / `applyFakeSkillProvider` / `fakeSkillProviderWithSkills` fakes in `phase/service_test.go:1396`, `phase/skill_usage_test.go:77`, `apply/run_test.go:2581`, `apply/skill_usage_test.go:58` to implement `SkillsForContext(ctx, SkillQuery) ([]*skill.Skill, []SkippedSkill, error)`. MUST fail (wrong interface until production code changes).
- [ ] K.5 GREEN — update `buildPriorContext` (`phase/service.go:1015-1048`): set `ContextRequest.Query = c.Name()`; map `recent_episodic` → `[]EpisodeRef`; map `decisions` → `[]RuleRef{Kind:"decision"}`; map `heuristics` → `[]RuleRef{Kind:"heuristic"}`; call `Memory.Search(SearchQuery{Query:"change digest", Scope:…, Types:["semantic"], Limit:3})` (DG-1); map results → `[]ChangeDigestRef`. Stop writing `RawMemoryBlob`. Delete `RawMemoryBlob` field from struct after verifying `rg 'RawMemoryBlob'` returns zero non-test refs.
- [ ] K.6 GREEN — migrate `phase/service.go:426`: `s.d.Skills.SkillsForPhase(ctx, p.Type())` → `s.d.Skills.SkillsForContext(ctx, discipline.SkillQuery{Phase: p.Type(), ProjectID: c.Project(), StructuralContext: structuralCtx})`. Fetch `structuralCtx` via `Memory.GetByTopicKey` (INIT structural topic key — confirm from INIT persistence; nil-safe).
- [ ] K.7 GREEN — rewrite `hydrateSkills` in `apply/teamlead.go:598-607` to call `SkillsForContext` with `SkillQuery{Phase: pt, ProjectID: c.Project(), StructuralContext: structuralCtx}`. Update callers at `teamlead.go:385` + `teamlead.go:487`. `structuralCtx` sourced same as K.6.
- [ ] K.8 GREEN — delete sibling `# Skill` injection block at `prompt_builder.go:97-102`. Delete `renderSkillSection` at `prompt_builder.go:263-282`. Remove `Skills []*skill.Skill` field from `PromptInput`; remove `import ".../domain/skill"` if no longer needed. Confirm `rg 'renderSkillSection'` → zero.
- [ ] K.9 GREEN — update prompt_builder callsites that set `PromptInput.Skills` (three sites: `phase/service.go:426` path + `teamlead.go:397` + `teamlead.go:499`) to instead set `PriorContext.Skills` via `toRenderedSkill`. `recordSkillUsageInjection` still runs pre-map on the `[]*skill.Skill` slice.
- [ ] K.10 VERIFY — `go test ./internal/application/phase/... ./internal/application/apply/... ./internal/application/discipline/...` green; `rg 'RawMemoryBlob'` → zero; `rg 'renderSkillSection'` → zero.

### Group L — New golden fixtures + structural assertions

Satisfies: `golden-rebaseline` spec (structural assertions + new fixtures requirements).

- [ ] L.1 — Add new golden fixtures: `phase_with_skills.golden.txt`, `phase_with_episodes.golden.txt`, `phase_with_digests.golden.txt`, `phase_all_layers.golden.txt`, `render_budget_truncated.golden.txt`, `render_attribution_on.golden.txt`, `render_attribution_off.golden.txt` under `testdata/priorcontext/`.
- [ ] L.2 — Add `TestRender_LayerOrdering` structural assertion: skills section offset < episodes section offset in output string.
- [ ] L.3 — Add `TestRender_NoBlockedSkillRendered`: inject deprecated + blocked + archived skills; assert none appear in `Render()` output.
- [ ] L.4 — Add `TestRender_BudgetRespected`: per-layer byte caps honoured; truncation marker present when truncated.
- [ ] L.5 — Add `TestRender_AttributionHeaders`: `EnableAttribution=true` → each rendered skill has `## Skill: <name> v<version> (active, source=` prefix.
- [ ] L.6 — Delete/rewrite `prompt_builder_test.go:408-448,553-567` tests that assert `# Skill` section ordering (now skills are inside `# Prior Context`).
- [ ] L.7 — Re-baseline `apply_no_skills_baseline.golden` and `apply_with_skill.golden` (sibling `# Skill` section removed; skill now renders via PriorContext).
- [ ] L.8 VERIFY — `go test ./internal/application/discipline/... ./internal/application/phase/...` green; all structural assertion tests pass.

### Group M — p95 benchmark

Satisfies: acceptance criterion 4 (p95 `buildPriorContext` < 250ms with 50 active skills).

- [ ] M.1 — Add `BenchmarkBuildPriorContext_50Skills` in `internal/application/phase/` with fake `MemoryClient` (fixed bundle + fixed digest Search result + fixed structural record, all in-memory) + fake `SkillMatcher` loaded with 50 active skills spanning frameworks/languages.
- [ ] M.2 — Run benchmark: `go test -bench=BenchmarkBuildPriorContext_50Skills -benchtime=5s ./internal/application/phase/...`; record p95 wall time in PR body. Target < 250ms. (Not a hard CI gate per D-M3-12 / M0.5 convention — benchmark variance on shared runners.)

### Group N — PR3a lint + checkpoint

- [ ] N.1 — `golangci-lint run ./...` clean. Fix any `forbidigo`/`wrapcheck`/`errorlint` issues.
- [ ] N.2 — HARD GATE: operator approval of PR3a. WAIT for PR3a merge to main before starting PR3b.

---

## PR3b Task Groups (orch — SkillsForPhase retirement cleanup)

### Group O — Wrapper/port deletion + wiring cleanup

Satisfies: `skillsforphase-retirement` spec (wrapper deleted, interface retired, RunDeps/wire.go rewired).

- [x] O.1 RED — add `TestSkillsForPhase_ZeroReferences` confirming `rg 'SkillsForPhase'` returns zero non-comment/non-test matches after deletion. (Test is documentation; actual check is the grep.) MUST fail until code deleted.
- [x] O.2 GREEN — delete `internal/adapters/outbound/pg/skill_provider.go` (entire file — `SkillProvider` deprecated wrapper).
- [x] O.3 GREEN — delete `internal/application/discipline/skill_provider.go` (`SkillProvider` interface + `SkillsForPhase` method signature).
- [x] O.4 GREEN — rewire `RunDeps.Skills discipline.SkillProvider` → `RunDeps.Skills discipline.SkillMatcher` in `apply/run.go:74`. (Already done in PR3a — verified, no change needed.)
- [x] O.5 GREEN — rewire `ServiceDeps.Skills discipline.SkillProvider` → `discipline.SkillMatcher` in `phase/service.go:126`. (Already done in PR3a — verified, no change needed.)
- [x] O.6 GREEN — update `wire.go`: remove stale SkillProvider comment; skillMatcher already wired to both Skills: fields from PR3a.
- [x] O.7 — Confirm `rg 'SkillsForPhase'` → zero; `rg 'SkillProvider'` → zero (no non-test references). Also deleted RawMemoryBlob field from PriorContext struct.

### Group P — Fake migrations cleanup

- [x] P.1 — Renamed fakeSkillProvider→fakeSkillMatcher, fakeSkillProviderWithSkills→fakeSkillMatcherWithSkills, fakeApplySkillProvider→fakeApplySkillMatcher, applyFakeSkillProvider→applyFakeSkillMatcher. Deleted pg integration tests for the retired SkillProvider wrapper.
- [x] P.2 VERIFY — `go test ./internal/application/apply/... ./internal/application/phase/...` fully green; no fake compilation errors.

### Group Q — PR3b lint + final checkpoint

- [x] Q.1 — `golangci-lint run ./...` clean (0 issues).
- [x] Q.2 — `make test-unit` green across the full orch module (all packages ok).
- [ ] Q.3 — HARD GATE: operator approval of PR3b. All acceptance criteria (AC 1–12) verified.

---

## Strict TDD discipline + merge gates

- strict_tdd: true — NO production code before its failing test exists. RED→GREEN→REFACTOR for every group.
- Merge order: PR1 → PR2 → PR3a → PR3b (stacked-to-main). Each is independently revertable.
- Conventional commits only: `feat(http)`, `feat(skill)`, `test(discipline)`, `refactor(discipline)`, `chore(wire)`, etc. NO Co-Authored-By, NO AI attribution.
- `golangci-lint run ./...` clean is a hard gate at every PR checkpoint.
- Two-repo coordination: PR1 coordinates orch + ME (M2-style). All other PRs are orch-only.

---

## Out of scope (M4 + future)

- Webhook outbox / at-least-once delivery (M4).
- LLM critic opt-in; `no_llm_guard_test.go` lint guard RETAINED (M4).
- `GET /usage skill_id` param (M4 — no consumer yet).
- `rollback_count` + `deprecated_api_hits` real instrumentation (M4+).
- `AppliesWhen.StateModel` field (only Framework/Language in M3).
- Real tokenizer for budget (byte semantics only — determinism).
- ME `BuildContext` IncludeTypes activation / DG-2 (rejected).
- apply-path spec/design/progress re-decomposition (stays in PhaseIdentity — D-M3-6).
- `Metrics.LastStackVersion` retype (stays `*string` — C-1).
- Routines layer / AuxiliaryMemory population (future milestone).
- governance-core HTTP surface (future).
- Detector direct-import sweep (aliases stay; cosmetic sweep deferred).
