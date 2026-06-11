# Delta: skill-get-endpoint

## Capability

`GET /api/v1/skills/{id}` endpoint on sophia-orchestator serving the `SkillSnapshot` JSON contract that the memory-engine worker already consumes at `skills_client.go:36-42`. Closes M2 verify WARNING 1 (live promote/demote inert due to 404).

## ADDED Requirements

### Requirement: GET /api/v1/skills/{id} route exists

The orch HTTP API MUST register `GET /api/v1/skills/{id}` as a protected route under the existing skills route group.

#### Scenario: Route registered

- GIVEN the orch HTTP server is running
- WHEN a client requests `GET /api/v1/skills/{id}` with a valid API key
- THEN the router dispatches to the GetSkill handler without a 404 or 405

### Requirement: SkillSnapshot JSON contract

The handler MUST respond with HTTP 200 and a JSON body matching the `SkillSnapshot` struct in `sophia-memory-engine/internal/ports/outbound/skills_client.go:36-42` field-for-field:

| Field | Type | Notes |
|---|---|---|
| `skill_id` | string | required |
| `status` | string | required |
| `risk_level` | string | required |
| `version` | string | required |
| `metrics.usage_count` | int | required |
| `metrics.success_count` | int | required |
| `metrics.failure_count` | int | required |
| `metrics.tests_passed_count` | int | required |
| `metrics.deprecated_api_hits` | int | required |
| `metrics.rollback_count` | int | required |
| `metrics.avg_retry_reduction` | float | required |

No additional top-level fields MAY be added without a corresponding consumer update.

#### Scenario: Known skill returns 200 with correct shape

- GIVEN a skill with id `skill-abc` exists in the database
- WHEN `GET /api/v1/skills/skill-abc` is called with a valid API key
- THEN the response status is 200
- AND the JSON body contains `skill_id`, `status`, `risk_level`, `version`, and `metrics` with all seven sub-fields

#### Scenario: Contract round-trip — response unmarshals into worker SkillSnapshot

- GIVEN orch returns a 200 response for a known skill
- WHEN the worker's HTTP adapter calls `GetSkill` and unmarshals the body into `SkillSnapshot`
- THEN no unmarshal error occurs and all fields are populated (contract test)

### Requirement: Auth enforcement on GET endpoint

The endpoint MUST require the same API-key authentication as existing skills endpoints. Unauthenticated requests MUST be rejected.

#### Scenario: No auth returns 401

- GIVEN `GET /api/v1/skills/{id}` is called without an API key header
- WHEN the server processes the request
- THEN the response status is 401

### Requirement: Unknown skill returns 404

The handler MUST return HTTP 404 when the skill_id is not found in the database. The response body MUST not expose internal error details.

#### Scenario: Unknown id returns 404

- GIVEN no skill with id `does-not-exist` exists in the database
- WHEN `GET /api/v1/skills/does-not-exist` is called with a valid API key
- THEN the response status is 404
