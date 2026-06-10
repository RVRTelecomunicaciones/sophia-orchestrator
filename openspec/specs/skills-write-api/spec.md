# Delta: skills-write-api

## Capability

Exposes two new HTTP endpoints on orch so the memory-engine worker can atomically update skill metrics and transition skill lifecycle status after processing a `phase.archived` event.

## ADDED Requirements

### Requirement: PATCH /api/v1/skills/{id}/metrics

`PATCH /api/v1/skills/{id}/metrics` MUST accept a JSON body containing delta fields: `success_delta`, `failure_delta`, `tests_passed_delta`, `rollback_delta`, `deprecated_api_hits_delta`, `usage_delta`, and `avg_retry_reduction` (float, optional).

The endpoint MUST apply deltas atomically to the existing `metrics` JSONB column. Negative delta values MUST be rejected with HTTP 422. Non-numeric fields MUST be rejected with HTTP 400.

`last_used_at` MUST be updated to the current timestamp on every successful `PATCH /metrics` call.

The endpoint MUST require API-key authentication. Missing or invalid API key MUST return HTTP 401.

#### Scenario: Valid delta applied atomically

- GIVEN a skill with `success_count = 2` in its metrics JSONB
- WHEN `PATCH /api/v1/skills/{id}/metrics` is called with `{ "success_delta": 1 }` and a valid API key
- THEN the skill's `success_count` becomes 3, `last_used_at` is updated, and HTTP 200 is returned

#### Scenario: Negative delta rejected

- GIVEN a valid skill exists
- WHEN `PATCH /api/v1/skills/{id}/metrics` is called with `{ "failure_delta": -1 }`
- THEN HTTP 422 is returned and no metric is mutated

#### Scenario: Missing auth returns 401

- GIVEN the endpoint is called without an API-key header
- WHEN the request reaches the auth middleware
- THEN HTTP 401 is returned

#### Scenario: Unknown skill returns 404

- GIVEN no skill exists for the given id
- WHEN `PATCH /api/v1/skills/{id}/metrics` is called with valid body and auth
- THEN HTTP 404 is returned

### Requirement: PATCH /api/v1/skills/{id}/status

`PATCH /api/v1/skills/{id}/status` MUST accept a JSON body `{ "status": <enum>, "reason": <string> }`.

`status` MUST be one of the six V4.1 §5.2 lifecycle values: `candidate`, `validated`, `active`, `deprecated`, `blocked`, `archived`. Any other value MUST return HTTP 422.

The endpoint MUST enforce domain invariants — specifically, a direct transition from `candidate` to `archived` MUST be rejected with HTTP 422.

When `status` transitions to `validated`, the endpoint MUST set `last_validated_at` to the current timestamp.

The endpoint MUST require API-key authentication. Missing or invalid API key MUST return HTTP 401.

#### Scenario: Valid status transition succeeds

- GIVEN a skill with status `candidate`
- WHEN `PATCH /api/v1/skills/{id}/status` is called with `{ "status": "validated", "reason": "thresholds met" }` and a valid API key
- THEN the skill's status becomes `validated`, `last_validated_at` is set, and HTTP 200 is returned

#### Scenario: Invalid enum value rejected

- GIVEN a valid skill exists
- WHEN `PATCH /api/v1/skills/{id}/status` is called with `{ "status": "unknown_value" }`
- THEN HTTP 422 is returned and the skill's status is unchanged

#### Scenario: Forbidden skip transition rejected

- GIVEN a skill with status `candidate`
- WHEN `PATCH /api/v1/skills/{id}/status` is called with `{ "status": "archived" }`
- THEN HTTP 422 is returned

#### Scenario: Missing auth returns 401

- GIVEN the endpoint is called without an API-key header
- WHEN the request reaches the auth middleware
- THEN HTTP 401 is returned
