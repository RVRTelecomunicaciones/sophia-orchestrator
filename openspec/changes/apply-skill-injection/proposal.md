# Proposal: Apply Skill Injection to SDD Phase Prompts

## Intent

Sophia's SDD dispatches an LLM per phase via opencode and asks it to reason the whole result from scratch. We have observed low task-completion (the apply phase struggles most). Cortex-IA and Gentleman-AI demonstrate that injecting per-phase **skills** — curated instructions + research-backed prompting techniques — makes the same LLM materially more reliable. The injection point already exists: `prompt_builder.Build` composes phase prompts (Iron Laws, HARD-GATE markers, Task, Data Schema), but `hardGatesFor` only returns minimal imperative gates, not rich guidance. This change upgrades that into a real skill system.

## Scope

### In Scope
- New `Skill` domain entity (name, applicable phases, technique tag, content) and a phase→skill(s) registry.
- `PromptBuilder` integration: additive `# Skill` section per phase, potentiating (not replacing) Iron Laws and HARD-GATE markers.
- ~9 skill contents (one per SDD phase: explore, proposal, spec, design, tasks, apply, verify, archive; init may be skipped), adapted from Cortex/Gentleman techniques (constitutional self-critique, chain-of-verification, skeleton-of-thought, extended thinking, ReAct, step-back, inline-why).
- MIT attribution for adapted Cortex content.
- Golden tests proving the `# Skill` section renders correctly per phase.

### Out of Scope
- Measuring real task-completion uplift (operator's Copilot quota exhausted; deferred to a session with quota/fallback).
- CLI or runtime changes — this is purely orch-side prompt composition.
- A user-facing skill authoring/loading API.

## Capabilities

### New Capabilities
- `skill-injection`: domain `Skill` entity, registry, and PromptBuilder integration that injects per-phase skill content into agent prompts.

### Modified Capabilities
- None at spec level (additive; existing prompt composition behavior is preserved).

## Approach

Hybrid design: Sophia's own **persisted domain entity** seeded with content adapted from Cortex/Gentleman. **DECIDED (operator directive — Sabor B): Skills are PERSISTED ENTITIES in PostgreSQL, updatable at runtime without recompiling.** Specifically:

1. **`Skill` is an Aggregate Root** in `internal/domain/skill/` with attributes: name, phase(s), content (rich instructions), techniques (e.g. constitutional-self-critique, chain-of-verification, extended-thinking, skeleton-of-thought, react, step-back, inline-why), plus identity/timestamps for runtime updates.
2. **Persistence (PG)**: this change INCLUDES a DB migration for a `skills` table and its repository (`internal/adapters/outbound/pg/skill_repo.go`), following the existing repo/migration patterns.
3. **Seeding**: a seeding mechanism (data migration or boot-time seeder) inserts the 9 hybrid skills (adapted from Cortex) into the DB at orchestrator startup, so the system is usable without a UI/CLI yet. Idempotent (upsert by name, do not clobber operator edits).
4. **Runtime hydration**: `PromptBuilder.Build` hydrates skills by querying the DB (via the skill repository) in real time before assembling the envelope, rendering a new `# Skill` section per phase. NOT embedded Go data — the source of truth is the DB, so skills evolve at runtime.

This makes Skill a first-class, runtime-updatable entity (CRUD-ready for a future UI/CLI), not a hardcoded prompt fragment. The PromptBuilder gains a dependency on a SkillProvider port (so it stays testable with a fake provider, no DB in unit tests).

## Affected Areas

| Area | Impact | Description |
|------|--------|-------------|
| `internal/domain/skill/` | New | `Skill` entity, technique enum, validation. |
| `internal/domain/skill/skill.go` | New | `Skill` aggregate root (name, phases, content, techniques, timestamps) + invariants. |
| `internal/ports/outbound/skill_repo.go` | New | SkillRepository port (FindByPhase, Upsert, List). |
| `internal/adapters/outbound/pg/skill_repo.go` | New | PG implementation of the skill repository. |
| `migrations/postgres/0NN_skills.{up,down}.sql` | New | `skills` table (next sequential migration number). |
| `internal/application/discipline/skill_provider.go` | New | SkillProvider port consumed by PromptBuilder; PG-backed impl hydrates per phase at runtime. |
| `internal/bootstrap/seed_skills.go` (or a data migration) | New | Idempotent seeding of the 9 hybrid skills at startup. |
| `internal/application/discipline/prompt_builder.go` | Modified | Adds `# Skill` section; golden tests updated. |
| `docs/adr/0011-skill-injection.md` | New | Records skills-as-embedded-data decision. |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| Prompt bloat degrades long-context phases | Med | Per-phase content budget; measure token count in golden tests. |
| Embedded content drifts from upstream Cortex | Low | ADR pins commit/version; periodic refresh task. |
| Skills hurt instead of help (unmeasured) | Med | Structural validation only this session; A/B measurement deferred to a quota-enabled session. |
| MIT attribution missed | Low | NOTICE entry + per-file header on adapted content. |

## Rollback Plan

`PromptBuilder` renders the `# Skill` section only when the SkillProvider returns skills for the phase; if the `skills` table is empty (or the provider errors, fail-soft), the prompt is byte-identical to today's behavior — so an empty/un-seeded DB = current behavior. The migration is reversible (down drops the `skills` table). Reverting the mechanism + seeding commits removes the entity, repo, and section. A feature flag (SOPHIA_SKILLS_ENABLED, default ON) can also disable hydration without a revert.

## Dependencies

- None external. Cortex-IA content is MIT-licensed and vendored at proposal time.

## Success Criteria

- [ ] `Skill` entity + registry unit-tested (domain coverage ≥85%).
- [ ] Golden tests prove `# Skill` section renders correctly for every phase that has a skill.
- [ ] Feature-flag OFF produces byte-identical prompts to pre-change.
- [ ] ADR-0011 merged.
- [ ] Real uplift measurement scheduled (separate change) once dispatcher quota/fallback is configured.

---

**Delivery note (chained-pr):** estimated >400 LOC. Recommend a two-slice chain — Slice 1: mechanism (entity, registry skeleton, PromptBuilder integration, feature flag, golden tests with stub content). Slice 2: content (9 adapted skills + ADR-0011 + attribution). Final decision deferred to `sdd-tasks`.
