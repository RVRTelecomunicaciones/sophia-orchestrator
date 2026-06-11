# Proposal: priorcontext-enrichment (M3)

**Strategy ref:** V4.1 §12 (PriorContext enrichment layers, §12.2 token budget, §12.3 source attribution), §16 milestone M3 acceptance criteria — the FINAL milestone of the learning-loop arc.
**Exploration:** `openspec/changes/priorcontext-enrichment/explore.md` (authoritative) + engram `sdd/priorcontext-enrichment/explore`.
**Forwarded traceability:** M2 verify report (`sdd/consolidation-worker/verify-report`) WARNING 1 (GetSkill 404) + WARNING 2 (SkillActivationProposal drift); M0.5 proposal (`sdd/priorcontext-struct-refactor/proposal`) stub types now made real.
**Repos:** sophia-orchestator (primary — PR1 + PR2 + PR3) + sophia-memory-engine (minor — PR1 reconcile only).
**Artifact store:** hybrid. **Strict TDD:** true.

---

## Intent

M3 completes the V4.1 learning-loop arc. M2 closed the **capture** side — the consolidation worker promotes/demotes skills from evidence, emits change digests, and proposes activations — but the loop's **consumers** are starved:

1. **Live promote/demote is inert.** The M2 worker calls orch `GET /api/v1/skills/{id}` to read a `SkillSnapshot` before transitioning status, but that endpoint does not exist (M2 verify WARNING 1: `router.go` exposes only `/usage`, `/{id}/metrics`, `/{id}/status`). In production the in-loop GetSkill returns 404, so status transitions never fire. This is the single highest-priority M3 item.
2. **StructuralContext sits orphaned.** It is a pure value object in `init/detector/types.go`, never wired into the matcher or PriorContext. The matcher cannot filter skills by `applies_when.framework`/`language`, so context-aware matching is impossible (M0.5 deferred this as Option D).
3. **Skills render outside PriorContext.** `prompt_builder.go` injects skills as a sibling `# Skill` section, divorced from the canonical PriorContext struct that M0.5 established. The `RenderedSkill` stub is empty.
4. **The prior-context blob is unstructured.** `buildPriorContext` makes one BuildContext call and concatenates everything blindly into `RawMemoryBlob`. The `EpisodeRef`/`ChangeDigestRef`/`RuleRef` layers M0.5 declared are still empty stubs — there is no enrichment, no per-layer budget, no attribution.
5. **The deprecated `SkillsForPhase` API survives.** Three callsites still use the phase-only provider instead of the context-aware `SkillMatcher`, blocking the V4.1 §16 "0 legacy callsites" criterion.

M3 wires every produced artifact into the prompt pipeline: it ships the endpoint that unblocks the loop, moves StructuralContext into the domain so the matcher can use it, makes the M0.5 stub types real, decomposes `RawMemoryBlob` into typed layers, enforces per-layer token budgets with source attribution, and retires the deprecated phase-only API.

**After M3, the loop is operational end-to-end: skills learned from evidence are matched by context, enriched into prompts within budget, and attributed to their source.**

---

## Scope

Three PRs, **stacked-to-main**, each independently deployable and revertable. Sequencing follows dependency: PR1 unblocks the loop and reconciles the cross-repo schema; PR2 lands the domain move the matcher needs; PR3 performs the full enrichment that consumes everything below it.

### PR1 — unblock the loop (orch + ME, ~200 LoC, M2-style 2-repo coordination)

**orch — `GET /api/v1/skills/{id}`:**
- `SkillRepo.FindByID` already exists (`internal/adapters/outbound/pg/skill_repo.go:268-286`) — needs only handler + service method + route.
- ADD `GetSkill(ctx, skillID) (*Skill, error)` to `SkillService` interface (`internal/ports/inbound/skill.go`) + impl.
- ADD `GetSkill` handler in `internal/adapters/inbound/http/handlers/skills.go` (follow PatchMetrics/PatchStatus/GetUsage pattern).
- ADD `GET /{skill_id}` route in `internal/adapters/inbound/http/router.go:138-148`.
- **JSON contract = worker's `SkillSnapshot` verbatim** (`sophia-memory-engine/internal/ports/outbound/skills_client.go:36-42`): `{skill_id, status, risk_level, version, metrics{usage_count, success_count, failure_count, tests_passed_count, deprecated_api_hits, rollback_count, avg_retry_reduction}}`. Match field names and nesting exactly — the consumer is already coded against this shape. Closes M2 verify WARNING 1.

**ME — SkillActivationProposal reconcile:**
- M2 verify WARNING 2: the proposer struct is narrower than V4.1 §9 spec text (impl followed design.md §6 = 6 fields; spec adds `skill_name`/`scope`/`applies_when`/`risk_level`).
- Reconcile the ME proposer to emit the full V4.1 §9 shape. Trivial glue (~30 LoC), rides PR1 on the ME side.

### PR2 — StructuralContext to domain (orch, ~300 LoC, **Option A**)

- MOVE `StructuralContext` from `internal/application/init/detector/types.go` to **NEW** `internal/domain/structural/context.go` as a pure value object (no interface — Option B rejected: single concrete type, pure data, all 3 consumers known; interface adds indirection without benefit).
- Both `discipline` and `init/detector` import `domain/structural` — verified no import cycle (explore §2 item 2).
- REPLACE 3 `*StructuralContextRef` nil-markers with `*structural.StructuralContext`: `discipline.PriorContext.StructuralCtx`, `discipline.SkillQuery.StructuralContext`, and the worker last_stack_version field.
- ACTIVATE `PGSkillMatcher` framework/language filtering: the matcher reads `applies_when.framework`/`applies_when.language` and filters against the live `StructuralContext`, making context-aware matching real. Nil-safe: changes pre-INIT-0 or with degraded INIT have no StructuralContext; existing `Render()` nil-guards (`prior_context.go:135-144`) and matcher fallback apply.

### PR3 — full enrichment (orch, ~430 prod + tests)

This is the heart of M3. **Baseline-capture-first** (M0.5 pattern): the very first commit re-captures golden baselines for the *intended* enriched output, then structural assertions replace byte-exact comparison (goldens change intentionally; byte-exactness is retired as the contract).

- **Stub types become real** (the M0.5 forward-compat fields): `RenderedSkill{Name, Version, Status, Source, Techniques, Content}`, `EpisodeRef`, `ChangeDigestRef`, `RuleRef` gain real fields and real population.
- **Skills move into PriorContext.Skills:** retire the sibling `# Skill` section (`prompt_builder.go:97-108`) and `renderSkillSection` (`prompt_builder.go:263-282`); skills now live in `PriorContext.Skills []RenderedSkill` and render through `Render()`.
- **RawMemoryBlob decomposition via BuildContext typed sections:** `buildPriorContext` (`phase/service.go:1015-1048`) stops blindly concatenating. BuildContext already returns typed sections (`decisions`/`heuristics`/`recent_episodic`/`related`). Mapping: `recent_episodic` → `[]EpisodeRef`; `decisions`+`heuristics` → `[]RuleRef`; change digests (M2 semantic memories tagged `change_digest`) → `[]ChangeDigestRef`. **Digests via `IncludeTypes: ["semantic"]` workaround** — no `SearchQuery.Tags` port extension (the port has no Tags field; IncludeTypes pulls digests holistically and avoids touching the outbound port at all).
- **Render() emits all layers** with **per-layer token budgets** (V4.1 §12.2: skills sort-cut, episodes top-K, digests top-3, truncation markers) and **source attribution headers** (V4.1 §12.3: `## Skill: <name> v<version> (active, source=...)`). `RenderOpts.TokenBudget`/`EnableAttribution` (declared since M0.5, zero-value no-op tested) move from no-op to active.
- **SkillsForPhase retirement:** the deprecated phase-only wrapper in `pg/skill_provider.go` is deleted; the 3 callsites (`phase/service.go:426`, `apply/teamlead.go:602` hydrateSkills, reached from `teamlead.go:487`+`:385`) migrate to `SkillMatcher` via `RunDeps`/`ServiceDeps`, passing `SkillQuery{Phase, ProjectID, StructuralContext}`.
- **Golden re-baseline:** new baseline captured FIRST, then structural assertions (sections present, ordering correct, budget respected, attribution present) replace byte-exact across the ~12 existing + prompt_builder fixtures.

**PR3 split contingency:** if the tasks forecast inflates beyond budget, split into **PR3a** (Render enrichment: stub types real + layers + budget + attribution + decomposition + golden re-baseline) and **PR3b** (SkillsForPhase removal: wrapper delete + 3 callsite migration). Decision deferred to `sdd-tasks` Review Workload Forecast.

### Non-goals (explicit)

- **Webhook outbox** (at-least-once delivery) — M4. Fire-and-forget is acceptable; the loop is functional without it. Not in V4.1 §16 M3 criteria.
- **LLM critic opt-in** — M4. The D-M2-12 lint guard (`no_llm_guard_test.go` — zero LLM imports in consolidation/) is explicitly **retained**.
- **GET /usage `skill_id` param** — M4. No consumer exists yet; the worker only filters by `change_id`.
- **`rollback_count` + `deprecated_api_hits` instrumentation** — M4+. Branches currently unreachable; the fields are served in the snapshot but not yet incremented from real signals.
- **governance-core HTTP surface** — future milestone.
- **Routines layer population** (Graphify/Context7/LSP pre-phase hooks) — declared in the PriorContext struct since M0.5, stays empty in M3; future milestone.
- **AuxiliaryMemory layer population** — stays nil; future milestone.

---

## Approach (high level)

**PR sequencing rationale.** PR1 first because it has the highest standalone value and zero internal dependencies: it unblocks the M2 live promote/demote path (WARNING 1) and reconciles the cross-repo schema (WARNING 2). PR2 next because the StructuralContext domain move is the prerequisite for context-aware matching, and it is a self-contained orch refactor with no enrichment coupling. PR3 last because it consumes both: it needs the matcher's structural filtering (PR2) to populate `PriorContext.Skills` correctly, and it is the largest, riskiest slice.

**Baseline-capture-first for PR3.** Following the proven M0.5 pattern: capture the golden baseline as the FIRST commit so the enriched output is frozen as the new contract, then write structural assertions. This makes the intentional prompt change reviewable as a single baseline diff rather than scattered across test churn.

**Byte-exact retirement note.** M0.5 established byte-exact goldens as the contract for the *refactor*. M3 is *enrichment* — the prompt changes by design. Byte-exactness is therefore explicitly retired as the M3 contract. The new contract is structural: required sections present, layer ordering correct, per-layer budgets respected, attribution headers present, no blocked/deprecated/archived skill injected. The new baseline goldens are captured for diff review, not for byte-for-byte regression.

**No memory-engine API changes for enrichment.** Episodes/BusinessRules come from BuildContext's existing typed sections; digests ride `IncludeTypes: ["semantic"]`. The only ME change is PR1's proposer reconcile. This keeps the cross-repo surface minimal and the p95 budget achievable (current 1 HTTP call; M3 adds GetByTopicKey for StructuralContext + digests ride the existing BuildContext call — ~3 round-trips × 10–50ms < 200ms; matcher filters ~100 rows in-process < 50ms).

---

## Affected systems

Per explore §2, with file:line. **NEW** vs **MODIFIED** marked per PR.

### PR1 (orch + ME)
| Area | Impact | Description |
|------|--------|-------------|
| `internal/adapters/outbound/pg/skill_repo.go:268-286` | NO CHANGE | `FindByID` already exists; reused |
| `internal/ports/inbound/skill.go` | MODIFIED | Add `GetSkill` to `SkillService` interface |
| `internal/adapters/inbound/http/handlers/skills.go` | MODIFIED | Add `GetSkill` handler serving `SkillSnapshot` |
| `internal/adapters/inbound/http/router.go:138-148` | MODIFIED | Register `GET /{skill_id}` route |
| ME `internal/ports/outbound/skills_client.go:36-42` | NO CHANGE | Contract source of truth (matched, not edited) |
| ME proposer (SkillActivationProposal) | MODIFIED | Emit full V4.1 §9 shape (skill_name/scope/applies_when/risk_level) |

### PR2 (orch)
| Area | Impact | Description |
|------|--------|-------------|
| `internal/domain/structural/context.go` | NEW | `StructuralContext` pure value object (moved) |
| `internal/application/init/detector/types.go` | MODIFIED | Remove `StructuralContext`; import `domain/structural` |
| `internal/application/discipline/prior_context.go` | MODIFIED | `StructuralCtx` field: `*StructuralContextRef` → `*structural.StructuralContext` |
| `internal/application/discipline` SkillQuery | MODIFIED | `StructuralContext` field typed to `*structural.StructuralContext` |
| worker last_stack_version | MODIFIED | 3rd nil-marker replacement |
| `PGSkillMatcher` | MODIFIED | Activate `applies_when.framework`/`language` filter via StructuralContext |

### PR3 (orch)
| Area | Impact | Description |
|------|--------|-------------|
| `internal/application/discipline/prior_context.go` | MODIFIED | `RenderedSkill`/`EpisodeRef`/`ChangeDigestRef`/`RuleRef` real; `Render()` layers + budget + attribution |
| `internal/application/discipline/prompt_builder.go:97-108` | MODIFIED | Retire sibling `# Skill` injection |
| `internal/application/discipline/prompt_builder.go:263-282` | REMOVED | `renderSkillSection` retired |
| `internal/application/phase/service.go:1015-1048` | MODIFIED | `buildPriorContext` decomposes BuildContext sections into typed layers |
| `internal/application/phase/service.go:426` | MODIFIED | Migrate SkillsForPhase callsite → SkillMatcher |
| `internal/application/apply/teamlead.go:602` (`:487`,`:385`) | MODIFIED | Migrate hydrateSkills callsite → SkillMatcher |
| `internal/adapters/outbound/pg/skill_provider.go` | REMOVED | Deprecated SkillsForPhase wrapper deleted |
| `internal/application/discipline/testdata/priorcontext/*.golden.txt` | MODIFIED | Re-baselined (enriched output); structural assertions added |

---

## Risks and mitigations

Per explore §5.

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| `SearchQuery` lacks Tags filter (digests) | Resolved | `IncludeTypes: ["semantic"]` workaround — no outbound port change |
| Golden snapshot cascade (~12 + prompt_builder tests) | High | M0.5 baseline-capture-first pattern; byte-exact retired; structural assertions; ~1 day effort |
| StructuralContext nil for pre-INIT-0 / degraded changes | Low | Existing `Render()` nil-guards (`prior_context.go:135-144`) + matcher fallback; safe |
| PR3 size inflates beyond budget | Medium | Split contingency ready (PR3a Render / PR3b SkillsForPhase); decided at tasks |
| Two-repo PR1 coordination | Low | Proven M2 pattern — no wire gate, just sequencing |
| GetSkill JSON drifts from consumer SkillSnapshot | Medium | Match `skills_client.go:36-42` field-for-field; contract test asserts shape |
| p95 regression from added round-trips | Low | ~3 round-trips < 200ms; matcher in-process < 50ms; benchmark gate at verify |

---

## Acceptance criteria (V4.1 §16 M3 verbatim + operator additions)

1. PriorContext includes **only skills with `status='active'`** filtered by SkillMatcher (scope + `applies_when` + StructuralContext-aware).
2. **No blocked/deprecated/archived skill** is ever injected.
3. Each injected skill carries a **source attribution header** (V4.1 §12.3).
4. **p95 `buildPriorContext` < 250ms** with 50 active skills (V4.1 §16).
5. **Token budget configurable and respected per layer** (V4.1 §12.2).
6. **Phase/framework/feature_type filter tests green.**
7. **0 callsites use `SkillsForPhase`** (deprecated wrapper deleted).

Operator additions:
8. `GET /api/v1/skills/{id}` serves the `SkillSnapshot` contract field-for-field (closes M2 WARNING 1).
9. SkillActivationProposal emits full V4.1 §9 shape (closes M2 WARNING 2).
10. Live promote/demote **fires in an integration test** (GetSkill no longer 404).
11. Golden snapshots **re-baselined**; structural assertions replace byte-exact.
12. `golangci-lint run` clean (INIT-0 lesson, carried since M0.5).

---

## Open questions

**None.** All operator decisions are locked (scope = 8 items IN; M4 deferrals fixed; 3-PR stacked-to-main; Option A for StructuralContext; SkillSnapshot contract verbatim; digests via IncludeTypes; baseline-first goldens; p95 < 250ms; strict TDD + conventional commits + no AI attribution; PR3 split contingency held in reserve for tasks).

---

## Rollback plan

Each PR is independently revertable (stacked-to-main, no shared migration):

- **PR1 revert:** removes `GET /skills/{id}` route+handler+service method and the ME proposer reconcile. Worker GetSkill returns to 404 (loop reverts to inert promote/demote — same as M2 main). No schema change.
- **PR2 revert:** moves `StructuralContext` back to `init/detector`, restores the 3 `*StructuralContextRef` nil-markers, deactivates matcher framework/language filtering. No schema change.
- **PR3 revert:** restores the sibling `# Skill` section + `renderSkillSection`, reverts `buildPriorContext` to `RawMemoryBlob` concatenation, restores the SkillsForPhase wrapper + 3 callsites, restores the M0.5 byte-exact goldens. Stub types return to empty. No schema change, no data migration.

---

## Strict TDD note

`strict_tdd: true` is active. `sdd-spec` MUST define test-first acceptance per capability with explicit red→green→refactor markers. `sdd-apply` MUST follow `strict-tdd.md`: no production code before its failing test exists.

- **PR1:** contract test asserting `GetSkill` JSON == `SkillSnapshot` shape (red) → handler+service (green); ME proposer shape test (red) → reconcile (green).
- **PR2:** matcher framework/language filter test (red) → StructuralContext move + filter activation (green); nil-safety test for missing StructuralContext.
- **PR3:** **baseline golden re-capture FIRST** (the new enriched contract), then per-layer structural assertions (red) → stub types real + Render() layers + budget + attribution + decomposition (green) → SkillsForPhase migration (green continues, then wrapper deleted). p95 benchmark gate (< 250ms with 50 skills).

---

## Capabilities (for sdd-spec contract)

### New Capabilities

**PR1**
- `skill-get-endpoint`: `GET /api/v1/skills/{id}` serving the `SkillSnapshot` contract (skill_id, status, risk_level, version, metrics{}) field-for-field per `skills_client.go:36-42`.
- `proposal-schema-reconcile`: ME proposer emits the full V4.1 §9 SkillActivationProposal shape (adds skill_name/scope/applies_when/risk_level).

**PR2**
- `structural-context-domain`: `StructuralContext` moved to `internal/domain/structural`; 3 consumers wired (PriorContext.StructuralCtx, SkillQuery.StructuralContext, worker last_stack_version) replacing nil-markers.
- `matcher-structural-filters`: `PGSkillMatcher` filters `applies_when.framework`/`language` via live StructuralContext; nil-safe fallback.

**PR3**
- `priorcontext-skills-layer`: `RenderedSkill` real + skills inside `PriorContext.Skills`; `renderSkillSection` retired; sibling `# Skill` section removed.
- `priorcontext-memory-layers`: Episodes/ChangeDigests/BusinessRules populated from BuildContext typed sections (recent_episodic / decisions+heuristics / semantic-tagged digests via IncludeTypes).
- `render-budget-attribution`: per-layer token budget (V4.1 §12.2) + source attribution headers (V4.1 §12.3) activated in `Render()`.
- `skillsforphase-retirement`: deprecated wrapper deleted + 3 callsites migrated to `SkillsForContext`/`SkillMatcher`.
- `golden-rebaseline`: new enriched baseline captured first; structural assertions replace byte-exact.

### Modified Capabilities

- **None at the spec level.** All M3 work is additive (new endpoint, new layers, new filters) or replaces deprecated surfaces (SkillsForPhase wrapper, renderSkillSection, RawMemoryBlob concatenation). No existing public spec contract is broken — the cross-repo `SkillSnapshot` shape is matched, not changed.
