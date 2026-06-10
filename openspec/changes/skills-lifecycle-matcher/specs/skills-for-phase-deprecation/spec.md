# Delta: skills-for-phase-deprecation

## Capability

`SkillsForPhase` in `internal/application/discipline/skill_provider.go` becomes a thin deprecated wrapper that delegates to `SkillsForContext(SkillQuery{Phase: phase})` and discards the `[]SkippedSkill` return. Its signature is unchanged, all 3 non-test callsites continue working without modification, and `// Deprecated:` godoc marks the M3 retirement target.

Refs: proposal §7, explore §4 §5 §11, V4.1 operator decision 8.

---

## ADDED Requirements

### Requirement: SkillsForPhase delegates to SkillsForContext

`SkillsForPhase(ctx, phase)` MUST call `SkillsForContext(ctx, SkillQuery{Phase: phase})`, return only the `[]Skill` result, and discard the `[]SkippedSkill` slice. The returned error MUST propagate unchanged.

#### Scenario: SkillsForPhase("apply") returns same skills as SkillsForContext with Phase filter

- GIVEN a `skills` table with 9 active seeds after migration 010 + backfill
- WHEN `SkillsForPhase(ctx, "apply")` is called
- THEN the returned `[]Skill` equals the `[]Skill` returned by `SkillsForContext(ctx, SkillQuery{Phase: "apply"})`

#### Scenario: Error from SkillsForContext propagates through SkillsForPhase

- GIVEN the underlying `SkillsForContext` returns a non-nil error
- WHEN `SkillsForPhase` is called
- THEN the same error is returned by `SkillsForPhase`
- AND the returned `[]Skill` is nil or empty

---

### Requirement: SkillsForPhase has Deprecated godoc

The method MUST carry a `// Deprecated:` comment directing callers to `SkillsForContext` and naming M3 as the removal milestone.

#### Scenario: Deprecated godoc is present and well-formed

- GIVEN the implementation of `SkillsForPhase`
- WHEN `go doc internal/application/discipline SkillsForPhase` is run
- THEN the output contains the word `Deprecated` and a reference to `SkillsForContext`

---

### Requirement: Existing callsites compile and behave identically

The 3 non-test callsites (`skill_provider.go:28`, `phase/service.go:396`, `apply/teamlead.go:594`) MUST require zero code changes. Their behavior MUST be unchanged after M1 applies.

#### Scenario: Phase service callsite compiles without modification

- GIVEN `internal/application/phase/service.go` calls `SkillsForPhase` as before
- WHEN `go build ./internal/application/phase/...` is run after M1 changes
- THEN the build succeeds with no errors and no signature mismatch

#### Scenario: Teamlead callsite compiles without modification

- GIVEN `internal/application/apply/teamlead.go` calls `SkillsForPhase` as before
- WHEN `go build ./internal/application/apply/...` is run after M1 changes
- THEN the build succeeds with no errors
