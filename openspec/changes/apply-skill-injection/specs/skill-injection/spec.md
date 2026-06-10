# Delta for skill-injection

## ADDED Requirements

### Requirement: Skill is a persisted, runtime-updatable entity

Each Skill MUST be persisted with unique `name`, non-empty `phases`, `content`, technique tags, and `createdAt`/`updatedAt`. Persisted `content` is the runtime source of truth: repository updates MUST take effect on subsequent hydrations without recompile or restart.

#### Scenario: Runtime update reflected on next hydration

- GIVEN a persisted Skill applicable to `apply`
- WHEN an operator updates its `content` via the repository
- THEN the next `apply` prompt MUST render the updated `content` without process restart

#### Scenario: No stale cache after update

- GIVEN a Skill whose `content` was just updated
- WHEN a subsequent phase build uses that Skill
- THEN the `# Skill` section MUST reflect the latest persisted `content`

### Requirement: Per-phase hydration of skills into the prompt

Before dispatching each SDD phase, the system MUST query Skills applicable to that phase and render them in a single `# Skill` section. If no Skill matches, the prompt MUST NOT contain a `# Skill` section.

#### Scenario: Matching skills render a Skill section

- GIVEN the `design` phase and a persisted Skill listing `design`
- WHEN the orchestrator builds the prompt
- THEN it MUST contain exactly one `# Skill` section with that Skill's name, techniques, and content

#### Scenario: No matching skills omit the section

- GIVEN a phase no persisted Skill lists
- WHEN the orchestrator builds the prompt
- THEN it MUST NOT contain a `# Skill` section

### Requirement: Skill invariants are enforced

The system MUST reject any Skill with empty `name`, empty `content`, zero valid phases, or any technique tag outside: `constitutional-self-critique`, `chain-of-verification`, `extended-thinking`, `skeleton-of-thought`, `react`, `step-back`, `inline-why`.

#### Scenario: Invalid technique tag is rejected

- GIVEN a Skill creation with technique tag `freeform-thinking`
- WHEN the aggregate validates it
- THEN it MUST fail and no Skill MUST be persisted

#### Scenario: Empty content is rejected

- GIVEN a Skill create or update with empty `content`
- WHEN the aggregate validates it
- THEN it MUST fail with a domain validation error

### Requirement: Insert-only idempotent seeding at startup

At startup the system MUST seed the 9 hybrid Skills using insert-if-absent by `name`. The seeder MUST NOT overwrite, delete, or modify an existing Skill. Two consecutive startups MUST leave existing rows byte-identical.

#### Scenario: Operator edit survives restart

- GIVEN a persisted Skill whose `content` was operator-edited at runtime
- WHEN the orchestrator restarts and the seeder runs
- THEN the edited `content` MUST remain unchanged

#### Scenario: Empty table is fully seeded

- GIVEN an empty `skills` table
- WHEN the seeder runs
- THEN all 9 hybrid Skills MUST be present afterward

### Requirement: Fail-soft preserves pre-change prompt

The prompt MUST be byte-identical to the pre-change prompt for that phase when ANY holds: `SOPHIA_SKILLS_ENABLED=false`, the `skills` table is empty, or the Skill provider returns an error.

#### Scenario: Feature flag disabled

- GIVEN `SOPHIA_SKILLS_ENABLED=false`
- WHEN the orchestrator builds a prompt for any phase
- THEN it MUST be byte-identical to the pre-change prompt

#### Scenario: Provider error does not abort the phase

- GIVEN the Skill provider returns an error during hydration
- WHEN the orchestrator builds the prompt
- THEN it MUST be byte-identical to the pre-change prompt AND the phase MUST proceed

### Requirement: PromptBuilder remains pure given its input

PromptBuilder MUST be a pure, deterministic function of its input and MUST NOT access database, network, or filesystem while rendering. Skills MUST be supplied as already-hydrated input by the caller.

#### Scenario: Identical input yields identical prompt

- GIVEN two `PromptInput` values with identical fields including the same `Skills`
- WHEN PromptBuilder renders each
- THEN the two prompts MUST be byte-identical

#### Scenario: Builder performs no I/O

- GIVEN a PromptBuilder invocation
- WHEN the builder executes
- THEN it MUST NOT call any database, network, filesystem, or Skill provider operation
