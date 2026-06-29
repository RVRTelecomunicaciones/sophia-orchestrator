# Delta for rollback-signal-emission

**Repo**: sophia-orchestator (PR1)
**Affected file**: `internal/application/skill/reeval.go` — `revertRun` function

## Purpose

When `reeval --revert` reverses a skill's lifecycle transition, orch MUST emit
a `RollbackDelta=1` signal for each reverted skill via the existing
`PATCH /api/v1/skills/{id}/metrics` path. This is currently never called;
`rollback_count` is therefore permanently zero for every skill.

## ADDED Requirements

### Requirement: Rollback delta emitted per reverted skill

After `revertRun` reverses a skill's lifecycle transition, orch MUST call the
skills metrics endpoint with `RollbackDelta=1` for EACH skill_id whose status
was actually reverted in that run.

Skills that belong to the same change but were NOT reverted in this run MUST
NOT receive a `RollbackDelta` increment.

#### Scenario: Single reverted skill receives delta

- GIVEN a `reeval --revert` run that reverts exactly one skill (skill_id = A)
- WHEN `revertRun` completes the lifecycle reversal for skill A
- THEN `PATCH /api/v1/skills/A/metrics` is called with `rollback_delta = 1`
- AND no metrics PATCH is issued for any skill not in the revert set

#### Scenario: Multiple reverted skills each receive exactly one delta

- GIVEN a `reeval --revert` run that reverts skills A and B (two skills)
- WHEN `revertRun` completes lifecycle reversals for A and B
- THEN `PATCH /api/v1/skills/A/metrics` is called with `rollback_delta = 1`
- AND `PATCH /api/v1/skills/B/metrics` is called with `rollback_delta = 1`
- AND exactly two metrics PATCH calls are made (one per reverted skill)

#### Scenario: Non-reverted skills in the same change are not incremented

- GIVEN a change with skills A, B, and C
- AND a `reeval --revert` run whose revert set is {A} only
- WHEN `revertRun` executes
- THEN `PATCH /api/v1/skills/A/metrics` is called with `rollback_delta = 1`
- AND no metrics PATCH is issued for skills B or C

---

### Requirement: Rollback delta idempotency per revert run

A given revert run (identified by its `RevertsRunID`) MUST contribute at most
`+1` to each skill's `rollback_count`, regardless of how many times that run
is executed or retried. Re-running the same revert run MUST NOT cause a
second `RollbackDelta=1` emission for any skill already incremented by that
run.

The observable contract is: after N executions of the same revert run (N ≥ 1),
each reverted skill's `rollback_count` has increased by exactly 1 compared
to before the first execution.

The mechanism that enforces this (e.g., a check on the audit record, a
revert-run-scoped flag) is left to design.

#### Scenario: First execution emits delta

- GIVEN a revert run with RevertsRunID = R1 that has never been executed
- WHEN `revertRun` executes for the first time
- THEN `PATCH /api/v1/skills/{id}/metrics` is called with `rollback_delta = 1`
  for each reverted skill

#### Scenario: Repeated execution of the same run is a no-op

- GIVEN a revert run with RevertsRunID = R1 whose delta was already emitted
- WHEN `revertRun` is executed again for the same RevertsRunID R1
- THEN no `PATCH /api/v1/skills/{id}/metrics` call is made for any skill
- AND each reverted skill's `rollback_count` remains at the value it had after
  the first execution

#### Scenario: Different revert runs are independent

- GIVEN revert run R1 has already been executed and emitted deltas
- AND a new, distinct revert run R2 (different RevertsRunID) exists that
  reverts the same skill A
- WHEN `revertRun` executes for R2
- THEN `PATCH /api/v1/skills/A/metrics` is called with `rollback_delta = 1`
  for run R2

---

### Requirement: Existing revert behavior is unchanged

The lifecycle reversal itself (status transitions, audit record creation) MUST
be identical to the behavior before this change. The only addition is the
`RollbackDelta=1` emission that follows a successful reversal. No existing
revert output, error surface, or state machine is altered.

#### Scenario: Revert lifecycle transition unaffected

- GIVEN any valid `reeval --revert` input
- WHEN `revertRun` executes
- THEN skill status transitions, audit records, and all other existing outputs
  are identical to pre-change behavior
- AND the only observable difference is the additional metrics PATCH call

## Non-Goals

- `deprecated_api_hits` instrumentation — deferred, no static-analysis
  detector exists (D1).
- New cross-repo HTTP contract — existing `MetricsDelta.RollbackDelta` +
  `PATCH /metrics` path is sufficient.
- `skill_usage` enum change — out of scope.
- Change-level or phase-level rollback attribution — out of scope (only
  `reeval --revert` skill-level reverts).
