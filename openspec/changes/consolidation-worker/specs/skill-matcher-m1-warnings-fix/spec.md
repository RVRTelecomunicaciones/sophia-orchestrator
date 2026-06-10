# Delta: skill-matcher-m1-warnings-fix

## Capability

Applies three fixes from the M1 warnings list to `PGSkillMatcher`: wires the `MaxRiskLevel` filter that was previously a dead field, reinstates `usage_count desc` as the tertiary sort key, and types the constructor's pool parameter as `*pgxpool.Pool` instead of `interface{}`.

## ADDED Requirements

### Requirement: MaxRiskLevel filter enforcement

`PGSkillMatcher.SkillsForContext` MUST filter out skills whose `risk_level` exceeds `SkillQuery.MaxRiskLevel` when `MaxRiskLevel` is non-zero. Skills that are filtered due to exceeded risk MUST be recorded with the constant `SkipReasonRiskExceeded`.

When `MaxRiskLevel` is zero (unset), the filter MUST be a no-op — all risk levels pass through.

#### Scenario: MaxRiskLevel filters high-risk skills

- GIVEN a skill pool containing one `low` and one `high` risk skill
- AND a `SkillQuery` with `MaxRiskLevel = medium`
- WHEN `SkillsForContext` is called
- THEN only the `low` risk skill is returned
- AND the `high` skill is recorded with `SkipReasonRiskExceeded`

#### Scenario: Zero MaxRiskLevel is a no-op

- GIVEN a skill pool containing `low`, `medium`, `high`, and `critical` risk skills
- AND a `SkillQuery` with `MaxRiskLevel = 0` (unset)
- WHEN `SkillsForContext` is called
- THEN all four skills pass the risk filter and remain eligible

### Requirement: Tertiary sort by usage_count desc

The result ordering in `PGSkillMatcher.SkillsForContext` MUST use `metrics.usage_count desc nulls last` as the tertiary sort key, replacing the previous `id asc` fallback.

#### Scenario: Sort tertiary uses usage_count descending

- GIVEN two skills with identical primary and secondary sort keys
- AND the first skill has `usage_count = 10`, the second has `usage_count = 2`
- WHEN `SkillsForContext` is called
- THEN the skill with `usage_count = 10` appears before the skill with `usage_count = 2`

#### Scenario: Null usage_count sorts last

- GIVEN two skills with identical primary and secondary sort keys
- AND one skill has `usage_count = 5`, the other has `usage_count = NULL`
- WHEN `SkillsForContext` is called
- THEN the skill with `usage_count = 5` appears before the skill with `usage_count = NULL`

### Requirement: Pool parameter typed as *pgxpool.Pool

`NewPGSkillMatcher` MUST declare its first parameter as `*pgxpool.Pool` instead of `interface{}` or any untyped alias.

#### Scenario: Constructor accepts typed pool

- GIVEN a `*pgxpool.Pool` instance
- WHEN `NewPGSkillMatcher` is called with that pool
- THEN the compiler accepts the call without a type assertion or interface cast
- AND no compiler warning or `forbidigo` lint violation is raised
