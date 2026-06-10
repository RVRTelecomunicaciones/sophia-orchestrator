# Delta: skill-matcher-port

## Capability

A new port is declared in `internal/application/discipline/skill_matcher.go` defining the `SkillMatcher` interface, `SkillQuery` struct, `SkippedSkill` struct, and the opaque `StructuralContextRef` marker type. `SkillQuery.StructuralContext` is typed as `*StructuralContextRef` and is always nil in M1 (no import cycle, mirrors M0.5 Option D). The single method `SkillsForContext` replaces `SkillsForPhase` as the canonical resolution API from M1 forward.

Refs: proposal §5, explore §6 §7, V4.1 §8.

---

## ADDED Requirements

### Requirement: SkillMatcher interface declared in discipline package

The `discipline` package MUST declare a `SkillMatcher` interface with a single method:

```
SkillsForContext(ctx context.Context, q SkillQuery) ([]Skill, []SkippedSkill, error)
```

The interface MUST be in `internal/application/discipline/skill_matcher.go`.

#### Scenario: discipline package compiles with SkillMatcher interface

- GIVEN `skill_matcher.go` is added to `internal/application/discipline/`
- WHEN `go build ./internal/application/discipline/...` is run
- THEN the build succeeds with no errors

#### Scenario: SkillMatcher is publicly importable from another package

- GIVEN a package outside `discipline` that imports `internal/application/discipline`
- WHEN that package references `discipline.SkillMatcher`
- THEN the compiler resolves the type without error

---

### Requirement: SkillQuery type is defined with all required fields

`SkillQuery` MUST be a public struct in `discipline` with the following fields:

| Field | Type | Meaning |
|---|---|---|
| `Phase` | `string` | filter by phase name |
| `ProjectID` | `string` | filter by scope.project_id (`*` = match all) |
| `RepoID` | `string` | filter by scope.repo_id (`*` = match all) |
| `StructuralContext` | `*StructuralContextRef` | nil-only in M1; reserved for M3 |
| `FeatureType` | `string` | filter by applies_when.feature_type (empty = skip filter) |
| `TouchedPaths` | `[]string` | glob match against applies_when.touched_paths |
| `RiskLevel` | `string` | optional risk filter (empty = all) |

#### Scenario: Zero-value SkillQuery is valid input

- GIVEN a `SkillQuery{}` with no fields set
- WHEN passed to `SkillsForContext`
- THEN no panic or validation error occurs; the method proceeds with no filters applied (returns all active skills)

#### Scenario: SkillQuery fields are accessible by external callers

- GIVEN a caller in a package outside `discipline`
- WHEN the caller constructs `discipline.SkillQuery{Phase: "apply", ProjectID: "*"}`
- THEN the struct literal compiles and field values are accessible

---

### Requirement: SkippedSkill type is defined

`SkippedSkill` MUST be a public struct in `discipline` with at minimum:

| Field | Type |
|---|---|
| `SkillID` | `string` (or equivalent ID type) |
| `Reason` | `string` |

Valid `Reason` values MUST be one of: `"scope_mismatch"`, `"applies_when_failed"`, `"status_not_active"`.

#### Scenario: SkippedSkill is constructable

- GIVEN a caller creates `discipline.SkippedSkill{SkillID: "id-1", Reason: "scope_mismatch"}`
- WHEN the type is used in `go vet`
- THEN no vet warnings are emitted

---

### Requirement: StructuralContextRef is an opaque nil-only marker

`StructuralContextRef` MUST be declared as an empty struct. In M1 it MUST NOT carry any data. `SkillQuery.StructuralContext` MUST always be `nil` in M1 (the PG adapter ignores this field). This MUST NOT introduce any import of `internal/application/init/detector`.

#### Scenario: StructuralContextRef does not cause import cycle

- GIVEN `discipline` declares `StructuralContextRef` as an empty struct
- WHEN `go build ./...` is run on the full module
- THEN no import cycle error is reported

#### Scenario: PG adapter ignores StructuralContext nil value

- GIVEN a `SkillQuery` with `StructuralContext = nil`
- WHEN `SkillsForContext` is invoked on the PG adapter
- THEN the adapter proceeds without error and does not attempt to dereference the nil pointer
