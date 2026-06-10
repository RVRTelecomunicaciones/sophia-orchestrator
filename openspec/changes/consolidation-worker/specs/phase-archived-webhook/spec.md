# Delta: phase-archived-webhook

## Capability

Adds an outbound best-effort HTTP POST from orch to memory-engine immediately after `phase.archived` is successfully published, so the worker pipeline receives the event to close the learning loop.

## ADDED Requirements

### Requirement: Outbound POST on phase.archived

Orch MUST POST to the configured memory-engine URL (`POST /api/v1/worker/phase-archived`) after `publishEvent` succeeds for `phase.archived`. The POST MUST be fire-and-forget — it MUST NOT block the change advancement flow.

The payload MUST mirror `PhaseArchivedPayload`: `change_id`, `change_name`, `phase_type`, `archived_at`.

The request MUST include an API-key header for memory-engine authentication. The key MUST be sourced from orch configuration (environment variable), not hardcoded.

Failures (network error, non-2xx response, timeout) MUST be logged at WARN level with the error and change_id. A failure MUST NOT cause the orch operation to return an error or roll back.

The HTTP client timeout MUST be configurable with a default of 5 seconds.

#### Scenario: Happy path fires once

- GIVEN orch is configured with a valid memory-engine URL and API key
- AND a change reaches the archive phase
- WHEN `publishEvent` succeeds for `phase.archived`
- THEN orch sends exactly one POST to `{memory_engine_url}/api/v1/worker/phase-archived` with the correct payload and API-key header
- AND orch continues normally without waiting for memory-engine processing

#### Scenario: Network failure is logged and ignored

- GIVEN memory-engine is unreachable
- WHEN orch attempts the POST after `phase.archived`
- THEN the error is logged at WARN level containing the change_id
- AND orch does not return an error to the caller
- AND orch does not retry

#### Scenario: Timeout is logged and ignored

- GIVEN memory-engine responds slower than the configured timeout
- WHEN orch's HTTP client times out
- THEN the timeout is logged at WARN level
- AND orch continues normally

#### Scenario: Memory-engine returns non-2xx

- GIVEN memory-engine returns HTTP 500
- WHEN orch receives the response
- THEN the status code and change_id are logged at WARN level
- AND orch does not propagate the error
