# Delta: phase-archived-webhook

## Capability

Delivery of `phase.archived` from orch to memory-engine moves from fire-and-forget to outbox-backed at-least-once (see `webhook-outbox`). The POST payload stays byte-identical; the ME receiver is untouched.

## MODIFIED Requirements

### Requirement: Outbound delivery on phase.archived

Orch MUST deliver `phase.archived` to memory-engine (`POST /api/v1/worker/phase-archived`) via the transactional outbox + relay poller defined in `webhook-outbox`, NOT via a fire-and-forget goroutine POST. The outbox row MUST be INSERTed in the same transaction that completes the change; delivery is then performed asynchronously by the relay with at-least-once semantics and capped exponential backoff.
(Previously: a fire-and-forget goroutine POST fired once after `publishEvent`, was never retried, and dropped the event permanently if ME was down.)

The POST payload MUST remain byte-identical to today's `PhaseArchivedPayload`: `change_id`, `change_name`, `phase_type`, `archived_at`. The request MUST include the API-key header sourced from orch configuration (environment variable), not hardcoded. The HTTP client timeout MUST be configurable with a default of 5 seconds.

Delivery failures MUST NOT cause the orch operation to return an error or roll back; instead the outbox row stays `pending` and is retried by the relay until delivered. Failures MUST be logged at WARN level with the error and change_id.

#### Scenario: Payload byte-identical to current contract

- GIVEN a change reaches the archive phase
- WHEN the relay POSTs the outbox row to `{memory_engine_url}/api/v1/worker/phase-archived`
- THEN the request body is byte-for-byte identical to the legacy `PhaseArchivedPayload` JSON (`change_id`, `change_name`, `phase_type`, `archived_at`)
- AND the API-key header is present
- AND the ME receiver requires NO change to accept it

#### Scenario: Delivery is retried, not dropped, on failure

- GIVEN ME returns a non-2xx or is unreachable
- WHEN the relay attempts delivery
- THEN the error is logged at WARN level with the change_id
- AND the outbox row stays `pending` for retry (the event is NOT dropped)
- AND orch does not return an error to the caller

#### Scenario: Exactly one logical event per archived change

- GIVEN a change reaches the archive phase exactly once
- WHEN the outbox row is created and eventually delivered
- THEN ME receives the `phase.archived` event for that change at least once
- AND any duplicate redelivery is absorbed by ME's `HasTopic` idempotency guard
