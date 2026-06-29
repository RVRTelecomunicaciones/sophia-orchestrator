# Spec: skill-promoter

## Capability

Evaluates each skill referenced in a processed change against V4.1 §6.1
risk-level-aware promotion thresholds and transitions eligible `candidate` skills
to `validated` via the orch PATCH status endpoint.

**Clarified in**: `skill-risk-instrumentation` PR2 (sophia-memory-engine #20,
branch `feat/demoter-rollback-gate`, merged to main 2026-06-29) — confirmed that
rollback gates promotion at EVERY risk level (including low), locked by regression
tests. No promoter production code was changed; existing generic check
`rollback_count > threshold.RollbackCount` already enforces this at all levels via
zero-value threshold for low.

## Requirements

### Requirement: Risk-level-aware promotion thresholds

The promoter MUST evaluate every skill in `skills_used` whose current status is `candidate` against the thresholds for its `risk_level`:

| risk_level | success_count | failure_count | tests_passed_count | rollback_count | deprecated_api_hits | avg_retry_reduction |
|---|---|---|---|---|---|---|
| low | ≥ 1 | == 0 | ≥ 1 | == 0 | — | — |
| medium | ≥ 2 | == 0 | ≥ 2 | == 0 | == 0 | ≥ 0.20 |
| high | ≥ 2 | == 0 | ≥ 2 | == 0 | == 0 | ≥ 0.20 |
| critical | ≥ 2 | == 0 | ≥ 2 | == 0 | == 0 | ≥ 0.20 |

High and critical thresholds MUST be identical to medium — they MUST NOT be relaxed.

When all thresholds for the skill's risk level are satisfied, the promoter MUST call `SkillsClient.PatchStatus` with `status = "validated"` and a human-readable reason.

When any threshold is not met, the skill MUST remain `candidate` and no PATCH call is made.

#### Scenario: Low-risk skill promotes at 1 success

- GIVEN a `candidate` skill with `risk_level = low`, `success_count = 1`,
  `failure_count = 0`, `tests_passed_count = 1`, `rollback_count = 0`
- WHEN the promoter evaluates the skill
- THEN `PatchStatus` is called with `status = "validated"`

#### Scenario: Low-risk skill stays candidate at 0 success

- GIVEN a `candidate` skill with `risk_level = low`, `success_count = 0`
- WHEN the promoter evaluates the skill
- THEN no `PatchStatus` call is made

#### Scenario: Medium-risk skill stays candidate at 1 success

- GIVEN a `candidate` skill with `risk_level = medium`, `success_count = 1`, all other thresholds met
- WHEN the promoter evaluates the skill
- THEN no `PatchStatus` call is made (requires success_count ≥ 2)

#### Scenario: Medium-risk skill promotes at 2 successes

- GIVEN a `candidate` skill with `risk_level = medium`, `success_count = 2`, `failure_count = 0`, `rollback_count = 0`, `deprecated_api_hits = 0`, `tests_passed_count = 2`, `avg_retry_reduction = 0.25`
- WHEN the promoter evaluates the skill
- THEN `PatchStatus` is called with `status = "validated"`

#### Scenario: High-risk threshold not relaxed

- GIVEN a `candidate` skill with `risk_level = high`, `success_count = 1`, `tests_passed_count = 1`, `failure_count = 0`
- WHEN the promoter evaluates the skill
- THEN no `PatchStatus` call is made (high requires same thresholds as medium)

### Requirement: Non-candidate skills skipped

The promoter MUST NOT attempt to promote skills whose status is not `candidate`.
Skills with status `validated`, `active`, `deprecated`, `blocked`, or `archived`
MUST be skipped without any PATCH call.

#### Scenario: Active skill skipped by promoter

- GIVEN a skill with `status = active`
- WHEN the promoter evaluates the skill
- THEN no `PatchStatus` call is made for that skill

---

### Requirement: Skill with rollback_count >= 1 is not promoted

For ALL risk levels (including `low`), the promoter MUST NOT call `PatchStatus`
with `status = "validated"` when `rollback_count >= 1`, regardless of whether
all other thresholds are satisfied.

**Policy rationale**: a rollback is an equally-strong negative signal as a
failure — promoting a just-reverted skill is unsafe regardless of risk level.
The gate is the existing generic check `rollback_count > threshold.RollbackCount`
where ALL risk-level thresholds use zero-value `RollbackCount = 0`. This includes
`low`, which already requires `failure_count == 0` (D-M2-6); rollback is an
equivalent bar.

**Implementation note**: no promoter code change was required. The generic
check at `promoter.go:79` (`RollbackCount > t.RollbackCount`) already enforces
this at all levels via the zero-value threshold. The `—` (dash) in the M2 spec
table was incorrect; the correct value is `== 0` for all risk levels. Corrected
by `skill-risk-instrumentation` spec amendment. Regression tests lock this
behavior.

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
- THEN no `PatchStatus` call is made (rollback_count == 0 gate fails)
- AND this is consistent with low-risk already being gated on `failure_count = 0`
  (per D-M2-6): a rollback is an equally-strong negative signal, so a
  rolled-back skill is ineligible for promotion regardless of risk level.
