# Spec: rollback-signal-emission

## Capability

When `reeval --revert` reverses a skill's lifecycle transition, the orchestrator
emits a `RollbackDelta=1` signal for each reverted skill via the existing
`PATCH /api/v1/skills/{id}/metrics` path, incrementing that skill's
`rollback_count`. A query-before-emit idempotency guard (`ExistsByRevertsRunID`)
ensures each revert run contributes at most `+1` per skill regardless of retries.

**Shipped in**: `skill-risk-instrumentation` PR1 (sophia-orchestator #115,
branch `feat/rollback-signal-emission`, merged to main 2026-06-29).

**Context**: Before this change, `rollback_count` was permanently zero for every
skill because orch never emitted a signal. The ME demoter's `rollback_count >= 1`
branch and the promoter's `rollback_count == 0` guard were both vacuously
inert (demoter branch unreachable, promoter guard trivially true). This spec
closes the orch half of that gap; `skill-demoter` closes the ME half.

## Requirements

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
is executed or retried. Re-running the same revert run MUST NOT cause a second
`RollbackDelta=1` emission for any skill already incremented by that run.

The observable contract is: after N executions of the same revert run (N >= 1),
each reverted skill's `rollback_count` has increased by exactly 1 compared to
before the first execution.

The mechanism that enforces this is a query-before-emit idempotency guard
(`ExistsByRevertsRunID`) checked once before emission; the persisted revert
audit run is the durable marker.

**ACCEPTED EXCEPTION (W-1)**: the exactly-once guarantee holds for revert runs
that complete emission and persist their audit record. Because emission occurs
before the audit run is saved, a `PatchMetrics` failure mid-loop returns before
persistence, so a subsequent retry MAY re-emit `+1` for skills already
incremented (at-least-once, not exactly-once, in this partial-failure window).
This is intentionally accepted: both consumers gate on a threshold (demoter
`rollback_count >= 1`, promoter `> 0`), so an inflated count never changes a
decision. The save-first/emit-after alternative was rejected because it would
under-count on the same failure and leave a reverted skill un-blocked — a
false-negative on a safety signal.

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

## Implementation Notes (as shipped)

- **Signal ownership**: orch owns emission (D3 of `skill-risk-instrumentation`
  proposal). ME does not detect rollback — consume-only.
- **Idempotency key**: `reverts_run_id` already stored in `reeval_run` table.
  `ExistsByRevertsRunID` is a single indexed `SELECT EXISTS(...)` lookup — no
  migration required.
- **New interface**: `MetricsPatcher` (one method: `PatchMetrics`) added to
  `internal/application/skill/reeval.go`. Satisfied by `*Service` via
  compile-time assertion. `Reevaluator` gains an optional `metricsPatcher` field;
  the dry-run constructor does not receive it (nil-safe throughout).
- **New repo method**: `ExistsByRevertsRunID(ctx, originalRunID string) (bool, error)`
  added to `ReevalAuditRepository` port and its pg implementation.
- **No migration**: `RollbackCount` column already existed in the skills table;
  `reverts_run_id` column already existed in `reeval_run`.
- **W-1 accepted limitation**: partial-failure double-count window (see
  idempotency requirement above). Threshold-inert: inflated count never changes
  a demoter or promoter decision.

## Non-Goals

- `deprecated_api_hits` instrumentation — deferred; no static-analysis detector
  exists (D1 of proposal). Served by the API but always zero. Future milestone.
- New cross-repo HTTP contract — existing `MetricsDelta.RollbackDelta` +
  `PATCH /metrics` path is sufficient.
- `skill_usage` enum change — out of scope.
- Change-level or phase-level rollback attribution — only `reeval --revert`
  skill-level reverts are in scope.
