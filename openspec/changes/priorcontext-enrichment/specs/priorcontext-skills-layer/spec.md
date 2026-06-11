# Delta: priorcontext-skills-layer

## Capability

`RenderedSkill` becomes a real type with populated fields. Skills are rendered inside `PriorContext.Render()` — not as a sibling `# Skill` section. `renderSkillSection` in `prompt_builder.go` is deleted. Skill layer renders before memory layers, preserving the existing `# Skill` → `# Prior Context` ordering in the final prompt.

## ADDED Requirements

### Requirement: RenderedSkill is a real type

`RenderedSkill` MUST be a concrete struct with fields: `Name`, `Version`, `Status`, `Source`, `Techniques`, `Content`. All fields MUST be populated when a skill is rendered into a `PriorContext`.

#### Scenario: RenderedSkill fields populated

- GIVEN an active skill matched by `SkillMatcher` for the current phase and project
- WHEN the skill is added to `PriorContext.Skills`
- THEN `Name`, `Version`, `Status`, `Source`, and `Content` are non-empty strings

### Requirement: Skills render inside PriorContext.Render()

`PriorContext.Render()` MUST include all `Skills []RenderedSkill` in its output. The skills layer MUST appear before memory layers (episodes, rules, digests) in the rendered output.

#### Scenario: Active skill appears in Render() output

- GIVEN a `PriorContext` with one active `RenderedSkill`
- WHEN `Render()` is called
- THEN the rendered string contains the skill's content

#### Scenario: Skills section precedes memory sections

- GIVEN a `PriorContext` with one skill and at least one episode
- WHEN `Render()` is called
- THEN the skill content appears before the episode content in the output

### Requirement: No blocked/deprecated/archived skill injected

`PriorContext.Skills` MUST contain only skills with `status = active`. Skills with status `blocked`, `deprecated`, or `archived` MUST NOT appear.

#### Scenario: Non-active skill excluded from PriorContext

- GIVEN a skill with `status = deprecated`
- WHEN skills are loaded into `PriorContext`
- THEN the deprecated skill does not appear in `PriorContext.Skills`

### Requirement: renderSkillSection deleted and sibling section removed

The function `renderSkillSection` in `prompt_builder.go` MUST be deleted. The sibling `# Skill` injection at `prompt_builder.go:97-108` MUST be removed. No prompt output path MUST call `renderSkillSection`.

#### Scenario: prompt_builder does not emit sibling Skill section

- GIVEN a fully built prompt for any phase
- WHEN `prompt_builder.go` assembles the prompt
- THEN no `# Skill` section is emitted by `prompt_builder.go` directly (skills appear via `PriorContext.Render()`)

#### Scenario: Zero references to renderSkillSection

- GIVEN the deletion is complete
- WHEN the repo is searched for `renderSkillSection`
- THEN no occurrences are found
