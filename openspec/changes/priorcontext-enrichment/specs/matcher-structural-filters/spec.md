# Delta: matcher-structural-filters

## Capability

`PGSkillMatcher` activates context-aware filtering: when a `SkillQuery` carries a non-nil `StructuralContext`, the matcher filters candidate skills by `applies_when.framework` and `applies_when.language` against the live context values. Nil `StructuralContext` skips all structural filters (no false negatives). A new `SkipReason` is introduced for structural mismatch.

## ADDED Requirements

### Requirement: Framework filter against StructuralContext

When `SkillQuery.StructuralContext` is non-nil and `applies_when.framework` is non-empty on a candidate skill, the matcher MUST include the skill only if `applies_when.framework` appears in `StructuralContext.Frameworks`. If `StructuralContext.Frameworks` is empty the filter MUST be skipped for that skill (no false negatives).

#### Scenario: Framework match passes filter

- GIVEN a skill with `applies_when.framework = "nextjs"` and `StructuralContext.Frameworks = ["nextjs", "react"]`
- WHEN the matcher evaluates the skill
- THEN the skill is included in the result set

#### Scenario: Framework mismatch skips skill with reason

- GIVEN a skill with `applies_when.framework = "rails"` and `StructuralContext.Frameworks = ["nextjs"]`
- WHEN the matcher evaluates the skill
- THEN the skill is excluded
- AND a `SkipReason` indicating structural mismatch is recorded

#### Scenario: Empty applies_when.framework passes filter unconditionally

- GIVEN a skill with `applies_when.framework = ""`
- WHEN the matcher evaluates the skill regardless of StructuralContext
- THEN the skill is not excluded by the framework filter

### Requirement: Language filter against StructuralContext

When `SkillQuery.StructuralContext` is non-nil and `applies_when.language` is modeled and non-empty, the matcher MUST apply the same include/skip logic as framework filtering against `StructuralContext.Languages`.

#### Scenario: Language match passes filter

- GIVEN a skill with `applies_when.language = "typescript"` and `StructuralContext.Languages = ["typescript"]`
- WHEN the matcher evaluates the skill
- THEN the skill is included

#### Scenario: Language mismatch skips skill

- GIVEN a skill with `applies_when.language = "python"` and `StructuralContext.Languages = ["typescript"]`
- WHEN the matcher evaluates the skill
- THEN the skill is excluded with a structural mismatch SkipReason

### Requirement: Nil StructuralContext skips all structural filters

When `SkillQuery.StructuralContext` is nil, framework and language filters MUST NOT run. All skills that pass other filters MUST be included.

#### Scenario: Nil context skips framework and language filters

- GIVEN `SkillQuery.StructuralContext = nil`
- WHEN the matcher processes a skill with `applies_when.framework = "rails"`
- THEN the skill is not excluded by a structural filter

### Requirement: SkipReason for structural mismatch

A new `SkipReason` constant MUST be added to represent structural mismatch (framework or language). It MUST be distinct from existing skip reasons (phase mismatch, status mismatch, etc.).

#### Scenario: SkipReason value is distinct

- GIVEN the matcher records a structural mismatch skip
- WHEN the SkipReason is inspected
- THEN it differs from all pre-existing SkipReason values in the codebase
