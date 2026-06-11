# Delta: structural-context-domain

## Capability

`StructuralContext` is moved from `internal/application/init/detector/types.go` to `internal/domain/structural/context.go` as a pure value object. Three consumers (`discipline.PriorContext.StructuralCtx`, `discipline.SkillQuery.StructuralContext`, worker last_stack_version) replace their `*StructuralContextRef` nil-markers with `*structural.StructuralContext`. `StructuralContextRef` is deleted.

## ADDED Requirements

### Requirement: StructuralContext lives in domain layer

A `StructuralContext` pure value object MUST exist at `internal/domain/structural/context.go`. It MUST carry no methods or behavior — only data fields (Frameworks, Languages, and any existing sub-fields). `internal/application/init/detector` MUST import `domain/structural` and remove its local definition.

#### Scenario: StructuralContext in domain — no cycle

- GIVEN `internal/domain/structural` defines `StructuralContext`
- WHEN `internal/application/init/detector` and `internal/application/discipline` both import `domain/structural`
- THEN `go build ./...` reports no import cycle error

#### Scenario: INIT-0 detector tests remain green after move

- GIVEN `StructuralContext` has been moved to `domain/structural`
- WHEN all detector package tests are run
- THEN all tests pass with no compilation errors and no behavior changes

### Requirement: StructuralContextRef deleted

The type `StructuralContextRef` MUST NOT exist anywhere in the codebase after this change is merged.

#### Scenario: Zero references to StructuralContextRef

- GIVEN the domain move is complete
- WHEN the entire repo is searched for `StructuralContextRef`
- THEN no occurrences are found

### Requirement: Three consumers use *structural.StructuralContext

The following fields MUST be typed as `*structural.StructuralContext`:

| Location | Field |
|---|---|
| `discipline.PriorContext` | `StructuralCtx` |
| `discipline.SkillQuery` | `StructuralContext` |
| consolidation worker | `last_stack_version` equivalent field |

#### Scenario: PriorContext compiles with new type

- GIVEN `discipline.PriorContext.StructuralCtx` is `*structural.StructuralContext`
- WHEN the discipline package is compiled
- THEN no type errors occur and existing nil-guard logic (`prior_context.go:135-144`) remains valid

#### Scenario: Nil StructuralContext preserves no-op behavior

- GIVEN a `PriorContext` with `StructuralCtx = nil`
- WHEN `Render()` is called
- THEN rendering proceeds without panic and structural filters are skipped
