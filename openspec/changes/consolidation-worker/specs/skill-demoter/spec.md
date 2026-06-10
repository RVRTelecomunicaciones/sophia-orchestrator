# Delta: skill-demoter

## Capability

Evaluates each `active` skill referenced in a processed change against V4.1 §6.3 demotion thresholds and transitions eligible skills to `deprecated` or `blocked` via the orch PATCH status endpoint.

## ADDED Requirements

### Requirement: active → blocked transition

The demoter MUST transition an `active` skill to `blocked` when either of the following conditions is met:
- `rollback_count ≥ 1`
- `(failure_count / max(usage_count, 1)) > 0.15`

The demoter MUST call `SkillsClient.PatchStatus` with `status = "blocked"` and a reason identifying the triggering condition.

In M2, `rollback_count` is always 0 (not instrumented). Therefore the only active `blocked` path in M2 is the failure-ratio condition.

#### Scenario: Failure ratio exceeds 0.15 triggers blocked

- GIVEN an `active` skill with `failure_count = 2`, `usage_count = 10` (ratio = 0.20 > 0.15)
- WHEN the demoter evaluates the skill
- THEN `PatchStatus` is called with `status = "blocked"` and a reason citing failure ratio

#### Scenario: Failure ratio at or below 0.15 — no demotion

- GIVEN an `active` skill with `failure_count = 1`, `usage_count = 10` (ratio = 0.10 ≤ 0.15)
- WHEN the demoter evaluates the skill
- THEN no `PatchStatus` call is made for `blocked`

#### Scenario: rollback_count path unreachable in M2

- GIVEN any `active` skill in M2 (rollback_count is always 0)
- WHEN the demoter evaluates rollback_count
- THEN the `rollback_count ≥ 1` branch is never triggered
- AND the demoter documentation MUST note this as an M4+ instrumentation gap

### Requirement: active → deprecated transition

The demoter MUST transition an `active` skill to `deprecated` when any of the following conditions is met (evaluated over the Q4 window of last 10 uses):
- `deprecated_api_hits ≥ 1`
- `avg_retry_reduction < 0.05` (over last 10 uses)
- `last_stack_version` mismatch (M3 — skip in M2 when NULL)

In M2, `deprecated_api_hits` is always 0 and `last_stack_version` is always NULL. Therefore the only active `deprecated` path in M2 is `avg_retry_reduction < 0.05`.

The demoter MUST call `SkillsClient.PatchStatus` with `status = "deprecated"` and a reason identifying the triggering condition.

#### Scenario: avg_retry_reduction below 0.05 triggers deprecated

- GIVEN an `active` skill with `avg_retry_reduction = 0.03` (below 0.05 threshold)
- WHEN the demoter evaluates the skill over the last-10-uses window
- THEN `PatchStatus` is called with `status = "deprecated"` and a reason citing retry reduction

#### Scenario: deprecated_api_hits path unreachable in M2

- GIVEN any `active` skill in M2 (deprecated_api_hits is always 0)
- WHEN the demoter evaluates deprecated_api_hits
- THEN the `deprecated_api_hits ≥ 1` branch is never triggered
- AND the demoter documentation MUST note this as an M4+ instrumentation gap

### Requirement: Non-active skills skipped by demoter

The demoter MUST only evaluate skills with status `active`. Skills with any other status MUST be skipped without any PATCH call.

#### Scenario: Candidate skill skipped by demoter

- GIVEN a skill with `status = candidate`
- WHEN the demoter evaluates the skill
- THEN no `PatchStatus` call is made for that skill

### Requirement: blocked takes precedence over deprecated

When a skill meets conditions for both `blocked` and `deprecated` simultaneously, the demoter MUST apply `blocked` (higher severity).

#### Scenario: Both conditions met — blocked applied

- GIVEN an `active` skill with failure ratio > 0.15 AND avg_retry_reduction < 0.05
- WHEN the demoter evaluates the skill
- THEN `PatchStatus` is called with `status = "blocked"` (not `deprecated`)
