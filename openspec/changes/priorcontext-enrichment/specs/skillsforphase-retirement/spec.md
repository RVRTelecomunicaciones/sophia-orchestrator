# Delta: skillsforphase-retirement

## Capability

The deprecated `SkillsForPhase` wrapper in `internal/adapters/outbound/pg/skill_provider.go` is deleted. Three callsites (`phase/service.go:426`, `apply/teamlead.go:602` via `hydrateSkills` reached from `:487` and `:385`) migrate to `SkillsForContext` / `SkillMatcher` with `SkillQuery{Phase, ProjectID, StructuralContext}`. `discipline.SkillProvider` interface is deleted or reduced to contain only the context-aware method.

## ADDED Requirements

### Requirement: SkillsForPhase wrapper deleted

The `SkillsForPhase` function and its containing `SkillProvider` deprecated wrapper in `pg/skill_provider.go` MUST be deleted. No file in the codebase MUST call or reference `SkillsForPhase` after this change.

#### Scenario: Zero references to SkillsForPhase

- GIVEN the retirement is complete
- WHEN the entire repository is searched for `SkillsForPhase`
- THEN no occurrences are found

### Requirement: Callsites migrate to SkillMatcher via SkillQuery

All three former `SkillsForPhase` callsites MUST be migrated to call `SkillsForContext` or equivalent `SkillMatcher` method using `SkillQuery{Phase: <phase>, ProjectID: <id>, StructuralContext: <ctx>}`.

| Callsite | Location |
|---|---|
| Phase skill fetch | `phase/service.go:426` |
| Apply hydrate | `apply/teamlead.go:602` (`hydrateSkills`) reached from `:487` and `:385` |

#### Scenario: phase/service.go callsite passes StructuralContext

- GIVEN the phase service needs skills for a phase
- WHEN it calls the matcher
- THEN it passes a `SkillQuery` containing `Phase`, `ProjectID`, and `StructuralContext` (nil if unavailable)

#### Scenario: teamlead.go callsite passes StructuralContext

- GIVEN `hydrateSkills` is called during apply
- WHEN it calls the matcher
- THEN it passes a `SkillQuery` containing `Phase`, `ProjectID`, and `StructuralContext` (nil if unavailable)

### Requirement: SkillProvider interface retired

The `discipline.SkillProvider` interface MUST be deleted or reduced to only the context-aware method. No production code MUST depend on the deprecated phase-only signature.

#### Scenario: SkillProvider interface does not expose SkillsForPhase signature

- GIVEN the interface is updated
- WHEN the interface definition is inspected
- THEN no method with a phase-only (no StructuralContext) signature exists

### Requirement: No functional regression in skill injection

After migration, phases and apply operations MUST receive the same or richer set of skills compared to the deprecated path. Skills filtered by structural context MUST be a subset of the unfiltered result (no false negatives when StructuralContext is nil).

#### Scenario: Nil StructuralContext returns same skills as pre-migration

- GIVEN `SkillQuery.StructuralContext = nil`
- WHEN the matcher is called from the migrated callsite
- THEN the returned skills are equivalent to what `SkillsForPhase` would have returned
