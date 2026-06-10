# Delta: skill-promoter

## Capability

Evaluates each skill referenced in a processed change against V4.1 §6.1 risk-level-aware promotion thresholds and transitions eligible `candidate` skills to `validated` via the orch PATCH status endpoint.

## ADDED Requirements

### Requirement: Risk-level-aware promotion thresholds

The promoter MUST evaluate every skill in `skills_used` whose current status is `candidate` against the thresholds for its `risk_level`:

| risk_level | success_count | failure_count | tests_passed_count | rollback_count | deprecated_api_hits | avg_retry_reduction |
|---|---|---|---|---|---|---|
| low | ≥ 1 | == 0 | ≥ 1 | — | — | — |
| medium | ≥ 2 | == 0 | ≥ 2 | == 0 | == 0 | ≥ 0.20 |
| high | ≥ 2 | == 0 | ≥ 2 | == 0 | == 0 | ≥ 0.20 |
| critical | ≥ 2 | == 0 | ≥ 2 | == 0 | == 0 | ≥ 0.20 |

High and critical thresholds MUST be identical to medium — they MUST NOT be relaxed.

When all thresholds for the skill's risk level are satisfied, the promoter MUST call `SkillsClient.PatchStatus` with `status = "validated"` and a human-readable reason.

When any threshold is not met, the skill MUST remain `candidate` and no PATCH call is made.

#### Scenario: Low-risk skill promotes at 1 success

- GIVEN a `candidate` skill with `risk_level = low`, `success_count = 1`, `failure_count = 0`, `tests_passed_count = 1`
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

The promoter MUST NOT attempt to promote skills whose status is not `candidate`. Skills with status `validated`, `active`, `deprecated`, `blocked`, or `archived` MUST be skipped without any PATCH call.

#### Scenario: Active skill skipped by promoter

- GIVEN a skill with `status = active`
- WHEN the promoter evaluates the skill
- THEN no `PatchStatus` call is made for that skill
