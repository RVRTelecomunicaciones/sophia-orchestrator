# Delta: skill-usage-tracking

## Capability

The `GET /api/v1/skills/usage` response stops hardcoding `apply_attempts: 0`. The field is now enriched from real `tasks.attempts` for the change, feeding the promoter/demoter gates real data. No migration and no JSON contract change — `apply_attempts` was already in the response, only its value changes.

## MODIFIED Requirements

### Requirement: GET /api/v1/skills/usage endpoint

Orch MUST expose `GET /api/v1/skills/usage` with optional query parameters `skill_id` and `change_id`. The endpoint MUST require API-key authentication. The response MUST be a JSON array of skill_usage objects, filtered by the supplied parameters.

The `apply_attempts` field on each returned object MUST be sourced from the real `tasks.attempts` values for the relevant change — it MUST NOT be hardcoded to `0`. The response JSON shape MUST remain unchanged (same fields, same types); only the `apply_attempts` value changes from a constant `0` to real per-change data.
(Previously: `apply_attempts` was hardcoded to `0`, forcing `avg_retry_reduction` to a constant `0.333`.)

#### Scenario: apply_attempts reflects real tasks.attempts

- GIVEN a change whose apply tasks recorded non-zero `tasks.attempts`
- WHEN a caller sends `GET /api/v1/skills/usage?change_id={id}` with a valid API key
- THEN the returned objects' `apply_attempts` equals the real `tasks.attempts` basis for that change (not `0`)
- AND the JSON shape is otherwise identical to the prior contract

#### Scenario: Filter by change_id

- GIVEN skill_usage rows exist for multiple changes
- WHEN a caller sends `GET /api/v1/skills/usage?change_id={id}` with a valid API key
- THEN only rows matching that change_id are returned with HTTP 200

#### Scenario: Filter by skill_id

- GIVEN skill_usage rows exist for multiple skills
- WHEN a caller sends `GET /api/v1/skills/usage?skill_id={id}` with a valid API key
- THEN only rows matching that skill_id are returned with HTTP 200

#### Scenario: Missing auth returns 401

- GIVEN the endpoint is called without an API-key header
- WHEN the request reaches the auth middleware
- THEN HTTP 401 is returned and no rows are included in the response
