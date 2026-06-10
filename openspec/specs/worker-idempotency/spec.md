# Delta: worker-idempotency

## Capability

Ensures the consolidation worker pipeline processes each `phase.archived` event exactly once by querying memory-engine for an existing digest before committing any mutations.

## ADDED Requirements

### Requirement: Digest-based idempotency guard

Before executing any pipeline step for a received `phase.archived` event, the worker MUST query memory-engine for a record at `topic_key = digest/{change_id}`.

If a record exists at that topic_key, the worker MUST log the skip at INFO level and return without executing any further pipeline logic.

If no record exists, the worker MUST proceed with the full pipeline and MUST write the digest at `digest/{change_id}` upon successful completion.

The idempotency check MUST be the first step of `Handler.Handle` — no metrics, promoter, demoter, or proposer logic MUST execute before the guard passes.

#### Scenario: First receipt processes change

- GIVEN no record exists at `digest/{change_id}` in memory-engine
- WHEN the worker receives a `PhaseArchivedReceived` event for that change_id
- THEN the pipeline executes in full (metrics update, transitions, digest, proposals)
- AND a record is written at `digest/{change_id}` in memory-engine

#### Scenario: Re-receipt is a no-op

- GIVEN a record already exists at `digest/{change_id}` in memory-engine
- WHEN the worker receives the same `phase.archived` event again
- THEN the idempotency guard detects the existing record
- AND no metrics are mutated, no transitions are evaluated, no proposals are emitted
- AND an INFO-level log entry records the skipped change_id

#### Scenario: Idempotency check failure propagates

- GIVEN memory-engine is unreachable when the guard queries for `digest/{change_id}`
- WHEN the guard cannot determine if the digest exists
- THEN the worker MUST NOT proceed with the pipeline
- AND an error is logged with the change_id
