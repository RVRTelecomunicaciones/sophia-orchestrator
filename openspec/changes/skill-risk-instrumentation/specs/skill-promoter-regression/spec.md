# Delta for skill-promoter-regression

**Repo**: sophia-memory-engine (PR2)
**Affected file**: `internal/application/consolidation/promoter.go` — `Evaluate` function

## Purpose

The promoter already requires `rollback_count == 0` as a promotion gate for
medium, high, and critical risk skills. Once orch begins emitting non-zero
`rollback_count` values (PR1), this guard becomes non-vacuous. No promoter
code change is required. This spec locks the existing observable behavior as
a regression contract.

## ADDED Requirements

### Requirement: Skill with rollback_count >= 1 is not promoted

For skills at risk_level `medium`, `high`, or `critical`, the promoter MUST
NOT call `PatchStatus` with `status = "validated"` when `rollback_count >= 1`,
regardless of whether all other thresholds are satisfied.

This behavior already exists in promoter code. This requirement exists to
create a testable regression scenario that will turn RED if the promoter's
rollback guard is accidentally removed.

#### Scenario: Medium-risk skill blocked from promotion by rollback_count

- GIVEN a `candidate` skill with `risk_level = medium`, `success_count = 2`,
  `failure_count = 0`, `tests_passed_count = 2`, `avg_retry_reduction = 0.25`,
  `deprecated_api_hits = 0`, AND `rollback_count = 1`
- WHEN the promoter evaluates the skill
- THEN no `PatchStatus` call is made (rollback_count == 0 gate fails)

#### Scenario: Medium-risk skill promotes when rollback_count is zero

- GIVEN a `candidate` skill with `risk_level = medium`, `success_count = 2`,
  `failure_count = 0`, `tests_passed_count = 2`, `avg_retry_reduction = 0.25`,
  `deprecated_api_hits = 0`, AND `rollback_count = 0`
- WHEN the promoter evaluates the skill
- THEN `PatchStatus` is called with `status = "validated"`

#### Scenario: Rollback gates promotion at every risk level (including low)

- GIVEN a `candidate` skill with `risk_level = low`, `success_count = 1`,
  `failure_count = 0`, `tests_passed_count = 1`, AND `rollback_count = 1`
- WHEN the promoter evaluates the skill
- THEN it is NOT promoted (`PatchStatus` is NOT called with `status = "validated"`)
- AND this is consistent with low-risk already being gated on `failure_count = 0`
  (per D-M2-6): a rollback is an equally-strong negative signal, so a
  rolled-back skill is ineligible for promotion regardless of risk level. The
  gate is the existing generic check `rollback_count > threshold.RollbackCount`
  where the low-risk threshold's zero-value (`0`) already blocks any rollback.

## Non-Goals

- No promoter code change is required or permitted by this spec — the rollback
  gate already exists for all risk levels via the generic check + zero-value
  thresholds. This spec only adds regression tests that lock that behavior.
- Changing promotion thresholds for any risk level.
