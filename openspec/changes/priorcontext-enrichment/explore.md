# Exploration — priorcontext-enrichment (M3)

**Strategy ref:** V4.1 §12 (enrichment layers + token budget + attribution), §16 milestone M3 — FINAL milestone of the learning-loop arc.
**Mode:** SDD explore. NO production code changes; investigation + SCOPING only.
**Scope:** sophia-orchestator (primary) + sophia-memory-engine (minor, PR1 reconcile).
**Engram artifact:** `sdd/priorcontext-enrichment/explore`.

---

## 1. The scoping decision (primary deliverable)

The accumulated backlog from 5 milestones is 10 items. V4.1 §16 M3 acceptance criteria are the contract; items not needed for those criteria defer to M4.

### IN M3 (8 items, ~930 prod LoC + ~600 test = ~1530 total)

| # | Item | LoC est. | Rationale |
|---|---|---|---|
| 1 | **orch `GET /api/v1/skills/{id}`** | ~50 | CRITICAL — M2 promoter/demoter call it; live transitions inert until shipped. SkillRepo.FindByID already exists (`skill_repo.go:268-286`); needs only handler + service method + route |
| 2 | **StructuralContext wiring (Option A)** | ~120 | Resolves M0.5 Option D deferral for all 3 consumers at once |
| 3 | **Skills into PriorContext.Skills** | ~200 | V4.1 §16 M3 criterion: "PriorContext includes only active skills filtered by SkillMatcher". Retires renderSkillSection |
| 4 | **RawMemoryBlob decomposition** | ~250 | The heart of enrichment: Episodes / ChangeDigests / BusinessRules layers |
| 5 | **Token budget activation** | ~60 | V4.1 §12.2 per-layer budgets; RenderOpts.TokenBudget enforcement |
| 6 | **Source attribution activation** | ~40 | V4.1 §12.3 `## Skill: <name> v<version> (active, source=...)` headers |
| 7 | **SkillsForPhase removal** | ~180 | V4.1 §16 M3 criterion: "0 legacy callsites". 3 callsites migrate to SkillsForContext |
| 10a | **SkillActivationProposal reconcile** | ~30 | Trivial glue; rides PR1 (memory-engine side) |

### DEFER M4 (2 items)

| # | Item | LoC est. | Rationale |
|---|---|---|---|
| 8 | Webhook outbox (at-least-once) | ~300 | NOT in V4.1 §16 M3 acceptance criteria. Fire-and-forget acceptable; loop functional |
| 9 | LLM critic opt-in | ~400 | NOT in M3 criteria. D-M2-12 lint guard explicitly retained |
| 10b | GET /usage skill_id param | ~20 | No consumer exists yet |

---

## 2. Current state per IN-M3 item (file:line verified on main)

### Item 1 — GET /skills/{id}
- `internal/adapters/outbound/pg/skill_repo.go:268-286` — **FindByID already exists**
- `internal/adapters/inbound/http/router.go:138-148` — skills routes registered; GET /{skill_id} missing
- `internal/adapters/inbound/http/handlers/skills.go` — PatchMetrics/PatchStatus/GetUsage handlers (pattern to follow)
- `internal/ports/inbound/skill.go` — SkillService interface lacks GetSkill
- ME consumer contract: `sophia-memory-engine/internal/ports/outbound/skills_client.go:36-42` — `SkillSnapshot{skill_id, status, risk_level, version, metrics{}}` is the JSON shape the worker already expects. **Match it exactly.**

### Item 2 — StructuralContext (Option A recommended)
- Today: `internal/application/init/detector/types.go` — StructuralContext (pure value object, no methods/behavior)
- 3 consumers need it: `discipline.PriorContext.StructuralCtx`, `discipline.SkillQuery.StructuralContext`, worker last_stack_version (M4 metrics)
- **Option A**: move to `internal/domain/structural/context.go`. Both `discipline` and `init/detector` import `domain/structural` — no cycle. Replace the 3 `*StructuralContextRef` nil-markers with `*structural.StructuralContext`.
- Option B (interface) rejected: single concrete type, pure data — interface adds indirection without benefit now that all 3 consumers are known.

### Item 3 — Skills into PriorContext
- `internal/application/discipline/prompt_builder.go:97-108` — sibling `# Skill` section injection
- `prompt_builder.go:263-282` — renderSkillSection (retires)
- `prior_context.go` — `Skills []RenderedSkill` stub becomes real: `RenderedSkill{Name, Version, Status, Source, Techniques, Content}`

### Item 4 — RawMemoryBlob decomposition
- `internal/application/phase/service.go:1015-1048` — buildPriorContext does 1 BuildContext HTTP call, concatenates rec.Content blindly into RawMemoryBlob
- memory-engine `internal/application/retrieval/context_builder.go` — **BuildContext ALREADY returns typed sections** (decisions / heuristics / recent_episodic / related)
- Mapping: `recent_episodic` → `[]EpisodeRef`; `decisions`+`heuristics` → `[]RuleRef`; change digests (type semantic, tags change_digest from M2) → `[]ChangeDigestRef`
- **Gap**: `internal/ports/outbound/memory.go` SearchQuery has NO Tags field. Workaround: BuildContext `IncludeTypes: ["semantic"]` pulls digests holistically — avoids port extension entirely.

### Item 5+6 — Token budget + attribution
- `prior_context.go` RenderOpts{TokenBudget, EnableAttribution} declared since M0.5, zero-value no-op contract tested
- M3 activates: per-layer budget cut rules (V4.1 §12.2: skills sort-cut, episodes top-K, digests top-3, truncation markers) + attribution headers (V4.1 §12.3)

### Item 7 — SkillsForPhase removal
- 3 callsites: `phase/service.go:426`, `apply/teamlead.go:602` (hydrateSkills), reached from `teamlead.go:487` + `385`
- All via `discipline.SkillProvider`. Migration: swap for `SkillMatcher` in RunDeps + ServiceDeps; pass `SkillQuery{Phase, ProjectID, StructuralContext}`
- Removes deprecated wrapper in `pg/skill_provider.go`

---

## 3. Key technical findings

1. **GetSkill is nearly free** — repo method exists; handler+service+route ~50 LoC. The SkillSnapshot contract is already defined by the consumer (worker). Ship first.
2. **No memory-engine API changes needed** for Episodes/BusinessRules — BuildContext sections suffice. Digests via IncludeTypes workaround.
3. **Golden snapshots WILL change** — M3 is enrichment; prompts change intentionally. Byte-exactness no longer the contract. New baseline capture commit FIRST (M0.5 pattern), then structural assertions (sections present, ordering, budget respected) replace byte-exact.
4. **p95 < 250ms is realistic**: current 1 HTTP call; M3 adds ~2 more (GetByTopicKey for StructuralContext + digests ride BuildContext). ~3 round-trips × 10-50ms < 200ms. Matcher filters ~100 rows in-process < 50ms.
5. **StructuralContext nil-safety**: changes pre-INIT-0 or degraded INIT have no StructuralContext. Render() nil-guards already exist (`prior_context.go:135-144`).

---

## 4. PR delivery proposal — 3 PRs stacked-to-main

| PR | Scope | LoC | Repos |
|---|---|---|---|
| **PR1** | GET /skills/{id} (orch) + SkillActivationProposal reconcile (ME) | ~200 | 2 repos, M2-style coordination |
| **PR2** | StructuralContext → domain/structural + matcher framework/language filter activation | ~300 | orch only |
| **PR3** | Full enrichment: stub types real, Render() layers, attribution, budgets, decomposition, SkillsForPhase retirement, new goldens | ~430 + tests | orch only |

PR1 has highest standalone value (unblocks live promote/demote). Each independently deployable. Contingency: if PR3 inflates, split PR3a (Render enrichment) + PR3b (SkillsForPhase removal).

---

## 5. Risks

1. **SearchQuery lacks Tags filter** — mitigated via IncludeTypes workaround (no port change)
2. **Golden snapshot cascade** — ~12 existing + prompt_builder tests need re-capture; ~1 day effort; M0.5 baseline-first pattern
3. **StructuralContext nil for old changes** — nil-guards exist; safe
4. **PR3 size** — monitor at tasks; split contingency ready
5. **Two-repo PR1 coordination** — proven M2 pattern (no wire gate, just sequencing)

---

## 6. Skill resolution

Standard SDD skills. Apply needs: go-testing (golden re-capture), api-contracts (GetSkill shape match), domain-modeling (structural move).

`skill_resolution: none`.
