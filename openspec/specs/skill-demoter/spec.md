# Spec: skill-demoter

## Capability

Evaluates each `active` skill referenced in a processed change against V4.1 §6.3
demotion thresholds and transitions eligible skills to `deprecated` or `blocked`
via the orch PATCH status endpoint.

**Updated in**: `skill-risk-instrumentation` PR2 (sophia-memory-engine #20,
branch `feat/demoter-rollback-gate`, merged to main 2026-06-29) — activated the
`rollback_count >= 1 → blocked` branch that was previously unreachable code.

## Requirements

### Requirement: active → blocked transition

The demoter MUST transition an `active` skill to `blocked` when ANY of the
following conditions is met:
- `rollback_count >= 1`
- `(failure_count / max(usage_count, 1)) > 0.15`

The demoter MUST call `SkillsClient.PatchStatus` with `status = "blocked"` and
a reason identifying the triggering condition.

The `rollback_count >= 1` branch MUST short-circuit evaluation before any
failure-ratio or retry-reduction arithmetic (see Requirement: rollback axis
evaluated before deprecated axes).

Note: In M2, `rollback_count` was always 0 (orch did not emit the signal). The
`rollback_count >= 1` branch was unreachable in M2. From `skill-risk-instrumentation`
onwards, orch emits `RollbackDelta=1` per reverted skill at `reeval --revert`
time, making this branch reachable (M3-active).

#### Scenario: Failure ratio exceeds 0.15 triggers blocked

- GIVEN an `active` skill with `failure_count = 2`, `usage_count = 10`
  (ratio = 0.20 > 0.15)
- WHEN the demoter evaluates the skill
- THEN `PatchStatus` is called with `status = "blocked"` and a reason citing
  failure ratio

#### Scenario: Failure ratio at or below 0.15 — no blocked on this axis

- GIVEN an `active` skill with `failure_count = 1`, `usage_count = 10`
  (ratio = 0.10 <= 0.15), `rollback_count = 0`
- WHEN the demoter evaluates the skill
- THEN no `PatchStatus` call is made for `blocked`

#### Scenario: rollback_count >= 1 triggers blocked

- GIVEN an `active` skill with `rollback_count = 1`, `failure_count = 0`,
  `usage_count = 10` (failure ratio = 0.00)
- WHEN the demoter evaluates the skill
- THEN `PatchStatus` is called with `status = "blocked"` and a reason citing
  rollback_count

#### Scenario: rollback_count = 0 does not trigger blocked on this axis

- GIVEN an `active` skill with `rollback_count = 0`, failure ratio <= 0.15
- WHEN the demoter evaluates the skill
- THEN no `PatchStatus` call is made for `blocked` (rollback axis is quiet)

#### Scenario: rollback_count >= 1 takes precedence — blocked applied

- GIVEN an `active` skill with `rollback_count = 2` AND failure ratio > 0.15
  (both blocked conditions met)
- WHEN the demoter evaluates the skill
- THEN `PatchStatus` is called with `status = "blocked"` exactly once

---

### Requirement: active → deprecated transition

The demoter MUST transition an `active` skill to `deprecated` when any of the
following conditions is met (evaluated over the Q4 window of last 10 uses):
- `deprecated_api_hits >= 1`
- `avg_retry_reduction < 0.05` (over last 10 uses)
- `last_stack_version` mismatch (M3 — skip in M2 when NULL)

In M2/M3, `deprecated_api_hits` is always 0 and `last_stack_version` is always
NULL. The only active `deprecated` path in M2/M3 remains `avg_retry_reduction < 0.05`.
The `deprecated_api_hits >= 1` branch is served but always 0 — future milestone.

The demoter MUST call `SkillsClient.PatchStatus` with `status = "deprecated"`
and a reason identifying the triggering condition.

#### Scenario: avg_retry_reduction below 0.05 triggers deprecated

- GIVEN an `active` skill with `avg_retry_reduction = 0.03` (below 0.05),
  `rollback_count = 0`
- WHEN the demoter evaluates the skill over the last-10-uses window
- THEN `PatchStatus` is called with `status = "deprecated"` citing retry
  reduction

#### Scenario: deprecated_api_hits path still unreachable

- GIVEN any `active` skill (deprecated_api_hits is always 0)
- WHEN the demoter evaluates deprecated_api_hits
- THEN the `deprecated_api_hits >= 1` branch is never triggered

---

### Requirement: Non-active skills skipped by demoter

The demoter MUST only evaluate skills with status `active`. Skills with any
other status MUST be skipped without any PATCH call.

#### Scenario: Candidate skill skipped by demoter

- GIVEN a skill with `status = candidate`
- WHEN the demoter evaluates the skill
- THEN no `PatchStatus` call is made for that skill

---

### Requirement: blocked takes precedence over deprecated

When a skill meets conditions for both `blocked` and `deprecated` simultaneously,
the demoter MUST apply `blocked` (higher severity).

#### Scenario: Both conditions met — blocked applied

- GIVEN an `active` skill with failure ratio > 0.15 AND avg_retry_reduction
  < 0.05
- WHEN the demoter evaluates the skill
- THEN `PatchStatus` is called with `status = "blocked"` (not `deprecated`)

---

### Requirement: rollback axis evaluated before deprecated axes

When `rollback_count >= 1`, the demoter MUST return `blocked` immediately
without evaluating `avg_retry_reduction` or `deprecated_api_hits`. The
`blocked` state is terminal for a given evaluation pass.

#### Scenario: rollback_count short-circuits deprecated evaluation

- GIVEN an `active` skill with `rollback_count = 1` AND `avg_retry_reduction
  = 0.03` (would also trigger deprecated)
- WHEN the demoter evaluates the skill
- THEN `PatchStatus` is called with `status = "blocked"` only
- AND `PatchStatus` is NOT called with `status = "deprecated"`

## Non-Goals

- `deprecated_api_hits` demoter branch — the commented-out branch for this axis
  is NOT activated in this change. Deferred; no static-analysis detector exists.
- Any change to `avg_retry_reduction` or `failure_rate` thresholds or evaluation
  logic.
- No new cross-repo contract — ME consumes `rollback_count` from the existing
  skill snapshot; no new endpoint.
