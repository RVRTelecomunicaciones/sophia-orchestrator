# ADR-0011 — Skill Injection into SDD Phase Prompts

- **Status**: accepted
- **Date**: 2026-06-07
- **Deciders**: Russell Vergara

## Context

Sophia's SDD workflow dispatches one LLM subprocess per phase and asks the
model to produce results from scratch using phase-specific Iron Laws and prompt
templates. Observations from Cortex-IA and Gentleman-AI indicate that injecting
structured reasoning guidance (cognitive technique prompts) into the phase
context materially improves task-completion rates, particularly in the `apply`
phase.

The guidance blocks need to be:

1. **Runtime-editable** — operators should be able to tune guidance without a
   redeploy.
2. **Per-phase** — different phases benefit from different techniques (e.g.,
   `chain-of-verification` for verify, `constitutional-self-critique` for apply).
3. **Non-breaking** — prompts must be byte-identical to the pre-change baseline
   when the feature is disabled, when the provider fails, or when no Skills
   match a given phase.
4. **PromptBuilder-pure** — the existing `discipline.PromptBuilder` must remain
   a pure, synchronous function with no I/O side effects.

Alternative approaches considered:
- Embedding skill text directly as compile-time constants (no runtime edit).
- Storing skills in JSONB or a join-table model (unnecessary schema complexity).
- Reaching the DB from inside `PromptBuilder.Build` (breaks purity, breaks 20+
  golden tests, pushes `context.Context` into every prompt test).

## Decision

1. **Persisted Skill aggregate** (`internal/domain/skill/`). Each Skill has a
   unique `name`, a list of applicable `phases`, runtime-editable `content`, and
   a set of `technique` tags. Skills are stored in the `skills` Postgres table
   (migration 009) with `TEXT[]` columns for phases and techniques, enabling an
   efficient `ANY(phases)` look-up via a GIN index.

2. **Insert-only idempotent seeding**. Nine canonical Skills are seeded at
   boot-time by `internal/bootstrap/seed_skills.go` using `InsertIfAbsent`
   semantics keyed on `name`. The seeder NEVER overwrites an existing row, so
   operator runtime edits survive process restarts indefinitely.

3. **Caller-hydrates dispatch model**. Application services
   (`phase/service.go`, `apply/run.go`, `apply/teamlead.go`) call
   `SkillProvider.SkillsForPhase(ctx, phase)` before calling
   `discipline.PromptBuilder.Build`. The hydrated `[]*skill.Skill` slice is
   passed as `PromptInput.Skills`. `PromptBuilder` remains a pure, deterministic
   function of its input with zero I/O.

4. **Fail-soft guarantee**. When `SOPHIA_SKILLS_ENABLED=false`, the Skills table
   is empty, or the provider returns an error, services pass `nil` for
   `PromptInput.Skills`. `PromptBuilder` renders no `# Skill` section, producing
   a prompt that is byte-identical to the pre-change baseline.

5. **Additive prompt section**. When one or more Skills are provided,
   `PromptBuilder` inserts a `# Skill` section after the `# HARD-GATE Markers`
   block and before `# Prior Context`. The section augments, and never overrides,
   Iron Laws or HARD-GATE markers.

## Consequences

### Positive

- Operators can improve LLM task-completion by editing skill content at runtime
  without a redeploy or restart.
- `PromptBuilder` purity is preserved; its existing golden test suite requires
  no changes for the flag-off / empty-skills path.
- The `skills` table schema is minimal and uses only proven Postgres features
  (`TEXT[]`, `GIN`, `TIMESTAMPTZ`).
- The insert-only seeder guarantees the feature is safe to deploy to existing
  environments — it will seed once and never clobber operator edits.

### Negative

- Each SDD phase dispatch now requires one additional DB read (`FindByPhase`).
  The query is indexed and expected to be sub-millisecond at any realistic skill
  count.
- The nine canonical seed rows embed adapted prompt content from Cortex-IA
  (MIT) — attribution in `NOTICE` is required.

### Neutral

- Skills are not versioned in the schema. Operator edits replace the live row in
  place. Teams that want a history of skill content changes should use an
  external mechanism (e.g., git-tracked SQL patches).

## Alternatives considered

- **Embedded compile-time constants** — rejected: no runtime editability.
- **JSONB columns** — rejected: adds schema complexity with no query benefit
  given `TEXT[]` + `GIN` satisfies the only query path (`ANY(phases)`).
- **Phase/technique join tables** — rejected: over-models the domain; the
  closed-set constraints are enforced at the domain layer, not via FK relations.
- **PromptBuilder reads DB directly** — rejected: breaks purity, forces
  `context.Context` into every prompt test, and couples a rendering concern to
  the persistence layer.
