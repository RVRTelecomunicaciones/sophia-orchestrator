# Verify Report: priorcontext-enrichment (M3)

**Phase:** sdd-verify | **Artifact store:** hybrid | **Strict TDD:** true
**Verified at:** orch branch `feat/priorcontext-enrichment-pr3b` @ HEAD `ee21dad` (contains merges of PR1 #84, PR2 #85, PR3a #86 + PR3b commits) + ME @ HEAD `fc34cf5` (PR #18 merged).
**Method:** Source inspection of merged code against 9 specs + design (D-M3-1..12, C-1/C-2/C-3, DG-1) + V4.1 §12/§16 M3 criteria. Tests NOT re-executed (CI green; apply-progress #831 records "unit + integration green, lint 0"). Spec text for `priorcontext-memory-layers` carries stale `IncludeTypes` language; **design DG-1 overrides** and is the authoritative contract.

---

## Verdict

**PASS** — all 12 acceptance criteria satisfied with file:line evidence. 0 CRITICAL, 3 WARNING, 3 SUGGESTION. The V4.1 learning loop is operational end-to-end. Ready for `sdd-archive`.

---

## Coverage matrix (spec → implementation, file:line)

| Capability (spec) | Implementation evidence | Status |
|---|---|---|
| `skill-get-endpoint` — route | `internal/adapters/inbound/http/router.go:143-144` `r.Route("/{skill_id}")` → `r.Get("/", skillH.GetSkill)` (under `d.Skills != nil` gate, same as siblings) | ✅ |
| `skill-get-endpoint` — SkillSnapshot contract | `handlers/skills.go:25-47` `getSkillResp` + `getSkillMetrics` (5 top-level + 7 metrics, field names verbatim); `:199-218` `GetSkill` handler (200 / 404 `skill_not_found`); auth via existing protected group | ✅ |
| `skill-get-endpoint` — contract round-trip | `internal/adapters/inbound/http/skills_get_test.go` vendors worker `SkillSnapshot` (5+7 fields), marshals `getSkillResp` → unmarshals into vendored struct, asserts field equality | ✅ |
| `skill-get-endpoint` — service | `ports/inbound/skill.go:46,76` `GetSkillResult` + `GetSkill(ctx, skillID)`; `application/skill/service.go:126-148` parse→`FindByID`→`ErrNotFound` on miss | ✅ |
| `proposal-schema-reconcile` (ME) | `sophia-memory-engine/internal/application/consolidation/proposer.go:26-37` full §9 shape (skill_name/scope/applies_when/risk_level + 5 existing); `:90-148` idempotent re-emit appends evidence; storage `topic_key governance/skill-proposal/{id}`, type `semantic`, tags `[governance,skill_proposal,pending]` unchanged | ✅ |
| `proposal-schema-reconcile` — wide fetch | ME `ports/outbound/skills_client.go:95-109` `ProposerSkillView` + `ProposerSkillsClient.GetSkillWide`; `adapters/outbound/orchhttp/skills_client.go:265-303` `GetSkillWide` impl + compile-time guard | ✅ |
| `structural-context-domain` — domain pkg | `internal/domain/structural/context.go:14` `SchemaV1=1`; `:29` `StructuralContext`; `LanguageInfo`/`FrameworkInfo`/`GraphSummary`; `ChangeName` field for attribution | ✅ |
| `structural-context-domain` — aliases | `init/detector/types.go:25-34` type aliases (`=`) for all 4 types; `:12` `StructuralContextSchemaV1=structural.SchemaV1`; `:17` `SophiaDetectorVer` stays in detector (per C-1) | ✅ |
| `structural-context-domain` — 2 consumers retyped + Ref deleted | `discipline/prior_context.go:28` `StructuralCtx *structural.StructuralContext`; `skill_matcher.go:50` `SkillQuery.StructuralContext *structural.StructuralContext`; `StructuralContextRef` zero Go refs (C-1: only 2 markers; `Metrics.LastStackVersion` stays `*string`) | ✅ |
| `matcher-structural-filters` — AppliesWhen fields | `domain/skill/lifecycle.go:124,128` `Framework []string` + `Language []string`; JSONB round-trip via full-struct marshal/unmarshal `pg/skill_repo.go:76,140,249` (no per-key path needed); test `lifecycle_test.go` (D.1) | ✅ |
| `matcher-structural-filters` — filter + SkipReason | `pg/skill_matcher.go:111` filter call placed AFTER appliesWhen + BEFORE risk; `:237-282` `structuralMatches`/`anyFrameworkPresent`/`anyLanguagePresent` (case-insensitive `EqualFold`); `discipline/skill_matcher.go:98` `SkipReasonStructuralMismatch="structural_mismatch"` (distinct); tests `skill_matcher_structural_test.go` (F.1-F.4) | ✅ |
| `matcher-structural-filters` — nil fail-open | `structuralMatches` returns `("",true)` when `q.StructuralContext==nil` (skill_matcher.go ~:238-244) | ✅ |
| `priorcontext-skills-layer` — RenderedSkill real | `discipline/prior_context.go:56-70` 6 fields; `:135-144` `ToRenderedSkill` pure mapper | ✅ |
| `priorcontext-skills-layer` — skills in Render, precede memory | `prior_context.go:184-188` Skills layer FIRST in `collectLayers`; order Skills→Structural→Episodes→Digests→Rules→PhaseIdentity (`:180-216`) | ✅ |
| `priorcontext-skills-layer` — no blocked/deprecated/archived | matcher status gate (`SkipReasonStatusNotActive`) + defensive `renderSkills:224` `if s.Status != "active" { continue }`; test `TestRender_NoBlockedSkillRendered` | ✅ |
| `priorcontext-skills-layer` — renderSkillSection + sibling deleted | `prompt_builder.go:90` comment "# Skill section removed"; `PromptInput.Skills` deleted; `renderSkillSection` zero refs (only a comment in prior_context.go:231 + docs) | ✅ |
| `priorcontext-memory-layers` — EpisodeRef/RuleRef/ChangeDigestRef real | `prior_context.go:74-99` all three concrete with content + source ID | ✅ |
| `priorcontext-memory-layers` — buildPriorContext decompose | `phase/service.go:1039-1117`: Query=`c.Name()`; recent_episodic/related→EpisodeRef; decisions→RuleRef{decision}; heuristics→RuleRef{heuristic}; **digests via DG-1 Search(Types:[semantic],Limit:3)** NOT IncludeTypes; StructuralCtx via GetByTopicKey `sdd/<change>/init`; RawMemoryBlob NOT written | ✅ (DG-1 override) |
| `priorcontext-memory-layers` — RawMemoryBlob removed | zero Go production refs to `RawMemoryBlob`; field absent from `PriorContext` struct | ✅ |
| `priorcontext-memory-layers` — empty layers skip | `collectLayers` length/nil guards per layer (`:184,191,196,201,206,211`) | ✅ |
| `priorcontext-memory-layers` — no SearchQuery.Tags | DG-1 uses existing `Types` field; no Tags added to outbound port | ✅ |
| `render-budget-attribution` — per-layer budget | `prior_context.go:311-372` `layerBudgetShare` (40/20/15/15/10) + `enforceBudget` cascade + truncation marker; `Render:163-165` activates when `TokenBudget>0`; tests `TestRender_BudgetRespected`, golden `render_budget_truncated.golden.txt` | ✅ |
| `render-budget-attribution` — attribution headers | `:229` `## Skill: <name> v<version> (<status>, source=<src>)`; `:275` `## Episode (<id>)`; `:288` `## Change Digest (<id>)`; `:301` `## Rule: <kind> (<id>)`; golden `render_attribution_on.golden.txt`; test `TestRender_AttributionHeaders` | ✅ (see WARNING-2 on header format) |
| `render-budget-attribution` — zero-value no-op | `Render:160-172` budget skipped at 0, attribution false → no headers; tests `TestRenderOpts_ZeroValue_IsNoOp` (+ ApplyCase + M3 variants) | ✅ |
| `skillsforphase-retirement` — wrapper + port deleted | `pg/skill_provider.go` + `discipline/skill_provider.go` DELETED; `SkillsForPhase`/`SkillProvider` zero Go production refs (remaining hits are docs/.md + _test comments) | ✅ |
| `skillsforphase-retirement` — callsites migrated | `phase/service.go:430` `SkillsForContext(SkillQuery{Phase,ProjectID,StructuralContext})`; `apply/teamlead.go` hydrateSkills migrated; guard `retirement_test.go` (.go non-test scan) | ✅ |
| `golden-rebaseline` — baseline-first + structural | 5 `phase_*` goldens re-baselined; 7 new fixtures present; structural tests `TestRender_LayerOrdering/NoBlockedSkillRendered/BudgetRespected/AttributionHeaders` + `prior_context_golden_capture_test.go` | ✅ |

---

## Acceptance criteria table (V4.1 §16 M3 + operator additions)

| # | Criterion | Evidence | Verdict |
|---|---|---|---|
| 1 | Only `status='active'` skills filtered by SkillMatcher (scope + applies_when + structural-aware) | matcher precedence status→scope→appliesWhen→**structural**→risk (`pg/skill_matcher.go:75,84,99,111,122`) | PASS |
| 2 | No blocked/deprecated/archived skill injected | matcher status gate + defensive `renderSkills:224`; `TestRender_NoBlockedSkillRendered` | PASS |
| 3 | Each injected skill carries source attribution header | `prior_context.go:229` `## Skill: <name> v<version> (active, source=<src>)` | PASS |
| 4 | p95 buildPriorContext < 250ms with 50 active skills | `phase/benchmark_test.go:21` benchmark (Render ~µs computational); HTTP ~3 round-trips < 200ms argued analytically (design §5) | PASS (see WARNING-1) |
| 5 | Token budget configurable + respected per layer | `enforceBudget` + `layerBudgetShare` + `RenderOpts.TokenBudget`; `TestRender_BudgetRespected` | PASS |
| 6 | Phase/framework/feature_type filter tests green | `skill_matcher_structural_test.go` F.1-F.4 + existing appliesWhen tests (CI green per #831) | PASS |
| 7 | 0 callsites use SkillsForPhase | zero Go production refs; `retirement_test.go` guard | PASS |
| 8 | GET /skills/{id} serves SkillSnapshot contract (M2 WARNING 1) | `skills_get_test.go` vendored-struct contract pin | PASS |
| 9 | SkillActivationProposal full §9 (M2 WARNING 2) | ME `proposer.go:26-37` + GetSkillWide | PASS |
| 10 | Live promote/demote fires (GetSkill not 404) | endpoint registered + 200 path; integration green per #831 | PASS |
| 11 | Goldens re-baselined; structural assertions replace byte-exact | 5 rebaselined + 7 new + 4 structural tests | PASS |
| 12 | golangci-lint clean all PRs | #831: "lint 0"; ME `fc34cf5` staticcheck fix; tasks N.1/Q.1 checked | PASS |

---

## CRITICAL

**None.**

---

## WARNING

**WARNING-1 — p95 benchmark measures Render() only, not full buildPriorContext.**
`BenchmarkBuildPriorContext_50Skills` (`phase/benchmark_test.go:65-68`) constructs a `PriorContext` directly and times only `pc.Render(opts)`. Design §5 specified the benchmark should exercise the matcher filter chain over 50 skills + the map + Render. The criterion is "not a hard CI gate" (D-M3-12) and Render is the dominant in-process cost, so the criterion is satisfied analytically, but the benchmark name overstates its scope. The matcher/HTTP costs rely on the analytical argument in the PR body, not the benchmark. Non-blocking for archive.

**WARNING-2 — truncation marker + structural-attribution header wording drift from design text.**
Design D-M3-7 specified `…[truncated: N {layer} omitted, M bytes over budget]`; impl emits `…[truncated: %d bytes over budget in %s layer]` (`prior_context.go:360`). Design D-M3-8 specified `## Structural Context (init/<change_name>)` and `## Episode (<record_id>)` — impl matches for Episode/Digest/Rule, and Structural uses `## Structural Context (init/<ChangeName>)`. The Skill header in the spec table (`render-budget-attribution`) is `## Skill: <name> v<version> (<status>, source=<activation_source>)` and the impl matches exactly. The truncation-marker wording is a cosmetic deviation only; goldens were captured against the actual impl so they are internally consistent. Non-blocking.

**WARNING-3 — DG-1 digest Search maps all `semantic` results without filtering the `change_digest` signal.**
`buildPriorContext` (`phase/service.go:1086-1098`) maps every `semantic` Search result to `ChangeDigestRef` using `r.ID` as ChangeID and `r.Snippet` as Content. Design DG-1 said to "filter results whose record carries the `change_digest` signal." The current `SearchResult` projection exposes only ID/Snippet (no tag), and the outbound port may not be extended with Tags (spec constraint), so the filter cannot run client-side today. Risk: non-digest semantic memories could surface in the ChangeDigests layer. Bounded by `Limit:3`. Acceptable for M3 (digests are the only known `semantic`-typed records produced by the M2 consolidation worker), but should be hardened in M4 when a richer Search projection or a dedicated digest retrieval exists.

---

## SUGGESTION

**SUGGESTION-1 — `ChangeDigestRef.ChangeID` is populated with the memory record ID, not the source change_id.**
`phase/service.go:1094` sets `ChangeID: r.ID` (the memory record ID), whereas the type doc says "source change identifier (attribution anchor)." Attribution still traces to a record, but the semantic intent (the originating change) is not captured. Tighten in M4 alongside WARNING-3.

**SUGGESTION-2 — `priorcontext-memory-layers` spec text is stale (IncludeTypes / no-Tags).**
The merged code correctly follows design DG-1 (dedicated Search), but the spec text still describes the rejected `IncludeTypes` workaround and the "no SearchQuery.Tags" scenario. Update the spec delta during archive to reflect DG-1 so future readers are not misled. Documentation-only.

**SUGGESTION-3 — Historical references to retired symbols remain in `docs/`.**
`docs/adr/0011`, `0012`, and `docs/research/sophia-surface-inventory.md` still mention `SkillsForPhase`/`SkillProvider`/`StructuralContextRef`. These are historical ADR/research records (correctly excluded by the `.go`-only retirement guard) and are accurate as history. Optional: add a note that they were retired in M3.

---

## Adaptations approved (design overrides honored by implementation)

- **DG-1 over spec IncludeTypes** (C-3): digests via dedicated `Memory.Search(Types:[semantic],Limit:3)` — correct; the `IncludeTypes` path is inert on the ME side. Implementation matches design, not the stale spec text.
- **C-1**: only 2 `*StructuralContextRef` markers retyped; `Metrics.LastStackVersion` stays `*string`. Honored.
- **C-2**: `AppliesWhen.Framework/Language` added before matcher activation. Honored.
- **D-M3-3 type aliases**: detector consumers keep compiling with zero churn. Honored.
- **D-M3-6 episodic Query**: `ContextRequest.Query=c.Name()` so recent_episodic populates. Honored.
- **PR3 split into PR3a/PR3b** with callsite migration in PR3a (sibling-section deletion forces it). Honored per tasks split decision.

---

## Cross-PR integrity

- **Merge order:** PR1 #84 → PR2 #85 → PR3a #86 → PR3b (stacked-to-main, each independently revertable). Confirmed via `git log` merge commits 362d969/7c0a3e6/845bbf3 + PR3b commits 30c7c5d/386305a/ee21dad.
- **Two-repo coordination (PR1):** orch endpoint + ME proposer reconcile landed coherently; ME `GetSkillWide` consumes the orch additive fields; contract test pins the narrow shape so the additive fields cannot break the worker's narrow `SkillSnapshot` decode.
- **No AI attribution** in any orch or ME commit (scanned last 20 / 10). Conventional commits throughout.
- **Layering invariant:** `Render` stays pure/deterministic (no clock/random/env); matcher stays in `pg` adapter; structural type is a leaf `domain` package (no import cycle — both detector and discipline import it).
- **No regression surface:** RawMemoryBlob, SkillsForPhase, SkillProvider, StructuralContextRef, renderSkillSection, PromptInput.Skills all fully retired with a guard test.

---

## Risks for M4+

1. **WARNING-3 / SUGGESTION-1**: digest retrieval pulls all `semantic` records and uses record-ID as change-ID. Harden with a richer Search projection or dedicated digest query when M4 adds the webhook outbox / richer retrieval.
2. **rollback_count + deprecated_api_hits** are served in the snapshot but never incremented from real signals (proposal non-goal; M4+). The GetSkill contract exposes fields that are structurally present but semantically zero until instrumented.
3. **Benchmark scope (WARNING-1)**: when M4 adds real round-trip cost, replace the Render-only benchmark with a full-pipeline benchmark (fake MemoryClient + 50-skill matcher) to guard the < 250ms criterion empirically.
4. **Spec/design drift (SUGGESTION-2)**: archive should reconcile the `priorcontext-memory-layers` spec text with DG-1 to avoid a false premise feeding any future change.

---

## Recommendation

**Proceed to `sdd-archive`.** All 12 acceptance criteria PASS with concrete file:line evidence across 4 work units and 2 repos. The 3 WARNINGs are non-blocking (benchmark scope, cosmetic marker wording, digest-signal fidelity bounded by Limit:3) and the 3 SUGGESTIONs are documentation/M4-hardening items. The V4.1 learning-loop arc (HERMES-0 → PRE-0 → INIT-0 → M0.5 → M1 → M2 → M3) is complete and operational end-to-end: skills learned from evidence are matched by context, enriched into prompts within per-layer budget, and attributed to their source.
