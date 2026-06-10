# Delta: worker-webhook-receiver

## Capability

Exposes the inbound HTTP endpoint `POST /api/v1/worker/phase-archived` on sophia-memory-engine so it can receive webhook notifications from orch and dispatch them to the consolidation worker pipeline.

## ADDED Requirements

### Requirement: POST /api/v1/worker/phase-archived endpoint

`POST /api/v1/worker/phase-archived` MUST accept a JSON body matching the `PhaseArchivedReceived` struct: `change_id`, `change_name`, `phase_type`, `archived_at`.

The endpoint MUST require API-key authentication. A missing or invalid API-key header MUST return HTTP 401 with no processing.

On a valid, authenticated request the endpoint MUST return HTTP 202 Accepted immediately — before the pipeline completes. Processing MUST be dispatched asynchronously in a goroutine.

A malformed or unparseable JSON body MUST return HTTP 400 before any processing occurs.

The endpoint MUST NOT call any LLM API.

#### Scenario: Valid payload returns 202 and queues processing

- GIVEN memory-engine is running with a configured API key
- WHEN orch POSTs a valid `PhaseArchivedReceived` payload with the correct API-key header
- THEN HTTP 202 is returned immediately
- AND the consolidation pipeline is invoked asynchronously for that change

#### Scenario: Invalid JSON returns 400

- GIVEN the endpoint receives a request body that is not valid JSON
- WHEN the handler attempts to decode the body
- THEN HTTP 400 is returned and no pipeline processing is triggered

#### Scenario: Missing API key returns 401

- GIVEN the endpoint is called without an API-key header
- WHEN the auth middleware evaluates the request
- THEN HTTP 401 is returned and no processing occurs

#### Scenario: Wrong API key returns 401

- GIVEN the endpoint is called with an incorrect API-key header value
- WHEN the auth middleware evaluates the request
- THEN HTTP 401 is returned and no processing occurs
