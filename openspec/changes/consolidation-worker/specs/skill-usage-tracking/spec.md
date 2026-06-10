# Delta: skill-usage-tracking

## Capability

Introduces migration 011 and write path to record every skill injection into a durable `skill_usage` table in orch's Postgres database, together with a read endpoint so the memory-engine worker can retrieve per-change usage rows.

## ADDED Requirements

### Requirement: Migration 011 — skill_usage table

Migration 011 MUST create table `skill_usage` with columns: `id` (UUID PK), `change_id` (UUID NOT NULL), `phase_type` (text NOT NULL), `skill_id` (UUID NOT NULL), `skill_version` (text NOT NULL), `injected_at` (timestamptz NOT NULL), `outcome` (text NOT NULL, CHECK IN ('success','failure','blocked','pending')).

Migration 011 MUST create index `idx_skill_usage_change` on `(change_id)` and index `idx_skill_usage_skill` on `(skill_id, injected_at)`.

Down migration MUST drop the table and its indexes via `DROP TABLE IF EXISTS skill_usage`.

#### Scenario: Migration applies cleanly

- GIVEN a Postgres 16+ database at schema version 010
- WHEN migration 011 up is applied
- THEN `skill_usage` table exists with all required columns and constraints
- AND both indexes are present and functional

#### Scenario: Migration reverses cleanly

- GIVEN a Postgres 16+ database at schema version 011
- WHEN migration 011 down is applied
- THEN `skill_usage` table no longer exists
- AND both indexes are gone

### Requirement: Skill injection write path

Orch MUST write a `skill_usage` row at every skill injection callsite: `internal/application/phase/service.go` (phase-level injection) and `internal/application/apply/teamlead.go` (two apply-level injection sites). The row MUST be written with `outcome = 'pending'` at injection time.

Orch MUST update `outcome` on the matching row when the phase envelope status becomes known (post-phase-completion). Status `done` maps to `success`; `blocked` maps to `blocked`; any error maps to `failure`.

The combination `(skill_id, change_id, phase_type)` MUST be unique — re-injection of the same skill within the same phase of the same change MUST be a no-op (upsert or checked insert).

#### Scenario: Row written on injection

- GIVEN a phase begins with one or more skills in context
- WHEN the orchestrator injects a skill into a phase
- THEN a `skill_usage` row exists with `outcome = 'pending'` and correct `change_id`, `phase_type`, `skill_id`, `skill_version`

#### Scenario: Outcome updated on completion

- GIVEN a `skill_usage` row with `outcome = 'pending'` exists for a phase
- WHEN the phase envelope reaches status `done`
- THEN the row's `outcome` is updated to `success`

#### Scenario: Idempotent re-injection

- GIVEN a `skill_usage` row already exists for `(skill_id, change_id, phase_type)`
- WHEN orch attempts to write another row for the same triple
- THEN no duplicate row is created and no error is raised

### Requirement: GET /api/v1/skills/usage endpoint

Orch MUST expose `GET /api/v1/skills/usage` with optional query parameters `skill_id` and `change_id`. The endpoint MUST require API-key authentication. The response MUST be a JSON array of skill_usage objects, filtered by the supplied parameters.

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
