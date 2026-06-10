# Delta: skills-http-client

## Capability

Defines the outbound `SkillsClient` port interface and its HTTP adapter so the memory-engine worker can call orch's skills write API without coupling the application layer to HTTP mechanics.

## ADDED Requirements

### Requirement: SkillsClient outbound port

The `SkillsClient` port MUST expose the following methods:

| Method | Signature |
|---|---|
| PatchMetrics | `PatchMetrics(ctx context.Context, skill_id string, delta MetricsDelta) error` |
| PatchStatus | `PatchStatus(ctx context.Context, skill_id string, status string, reason string) error` |
| GetSkill | `GetSkill(ctx context.Context, skill_id string) (*Skill, error)` |
| GetUsage | `GetUsage(ctx context.Context, change_id string) ([]SkillUsage, error)` |

The port MUST be defined as a Go interface in `internal/ports/outbound/skills_client.go`.

#### Scenario: Port defines all four methods

- GIVEN the `SkillsClient` interface is defined
- WHEN a compiler checks the interface against the HTTP adapter
- THEN all four methods are present and correctly typed with no compile error

### Requirement: HTTP adapter behaviour

The HTTP adapter MUST send the API-key header on every request to orch, sourced from environment configuration. The orch base URL MUST also be configurable via environment variable.

The adapter MUST parse the JSON response body on `GetSkill` and `GetUsage` calls. On HTTP 4xx responses the adapter MUST return a typed error. On HTTP 5xx responses the adapter MUST return a retriable error.

The adapter MUST NOT call any LLM API.

#### Scenario: PatchMetrics success

- GIVEN orch PATCH /api/v1/skills/{id}/metrics returns HTTP 200
- WHEN the adapter calls `PatchMetrics` with a valid delta
- THEN the method returns nil error

#### Scenario: PatchMetrics 404 returns error

- GIVEN orch PATCH /api/v1/skills/{id}/metrics returns HTTP 404
- WHEN the adapter calls `PatchMetrics`
- THEN the method returns a non-nil error containing the HTTP status

#### Scenario: GetSkill round-trip

- GIVEN orch GET /api/v1/skills/{id} returns a valid JSON Skill body with HTTP 200
- WHEN the adapter calls `GetSkill`
- THEN the method returns a populated `*Skill` with nil error

#### Scenario: Missing API key in adapter config returns error at construction

- GIVEN the adapter is constructed with an empty API key
- WHEN the constructor is called
- THEN it returns an error — the adapter MUST NOT silently accept an empty key

### Requirement: Configurable orch coordinates

The HTTP adapter MUST read the orch base URL and API key from environment variables at construction time. Hard-coding either value in source code is forbidden.

#### Scenario: Env vars configure adapter

- GIVEN `ORCH_BASE_URL` and `ORCH_API_KEY` environment variables are set
- WHEN the HTTP adapter is constructed
- THEN the adapter uses those values for all requests without code changes
