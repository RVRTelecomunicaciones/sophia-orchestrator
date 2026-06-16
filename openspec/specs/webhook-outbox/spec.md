# webhook-outbox Specification

## Purpose

Make orch→ME delivery of `phase.archived` durable. A transactional outbox table (migration `012`) is written in the same transaction that completes a change, and a relay poller delivers pending events at-least-once with capped exponential backoff. This closes the only data-loss window in the live learning loop. Single producer (`phase.archived`); the table schema is generic for forward compatibility.

## Requirements

### Requirement: Migration 012 — generic outbox table (additive only)

Migration `012` MUST be additive only and MUST create table `webhook_outbox` with columns: `id` (CHAR(26) ULID PK), `event_type` (text NOT NULL), `payload` (BYTEA NOT NULL), `status` (text NOT NULL, CHECK IN ('pending','delivered')), `attempts` (int NOT NULL DEFAULT 0), `next_attempt_at` (timestamptz NOT NULL), `created_at` (timestamptz NOT NULL), `delivered_at` (timestamptz NULL). It MUST create a partial index on `(next_attempt_at)` WHERE `status = 'pending'`. The down migration MUST `DROP TABLE IF EXISTS webhook_outbox`. There is NO dead-letter column and NO expiry column.

> Spec reconciliation (loop-hardening archive, 2026-06-16): the PK is `CHAR(26)` ULID generated via the injectable `IDGenerator`, NOT `UUID`. Every prior migration (009, 011) uses CHAR(26) ULID PKs and repo CLAUDE.md rule 5 forbids `ulid.Make()`/`time.Now()` in domain/application — the repo convention wins (decision obs #883). The `payload` column is `BYTEA`, NOT `jsonb`: JSONB normalizes whitespace and reorders object keys on storage, which breaks the byte-identical delivery contract that `phase-archived-webhook` requires; an outbox is an opaque-blob carrier, so BYTEA is the correct type (decision obs #885).

#### Scenario: Migration applies and reverses cleanly

- GIVEN a Postgres 16+ database at schema version `011`
- WHEN migration `012` up is applied
- THEN `webhook_outbox` exists with all columns, the status CHECK, and the partial pending index
- AND applying `012` down drops the table and index with no residue

### Requirement: Outbox INSERT shares the change-completion transaction

When a change reaches the archive phase, orch MUST INSERT exactly one `webhook_outbox` row with `event_type = 'phase.archived'`, `status = 'pending'`, `attempts = 0`, and `next_attempt_at = now()`, inside the SAME database transaction that commits change completion. The `payload` MUST equal the current `PhaseArchivedPayload` byte-for-byte. The legacy fire-and-forget goroutine POST MUST be removed.

#### Scenario: INSERT commits atomically with change completion

- GIVEN a change advancing to the archive phase
- WHEN the change-completion transaction commits
- THEN exactly one `pending` outbox row exists for that change
- AND no separate fire-and-forget POST is issued at completion time

#### Scenario: Rollback drops the outbox row too

- GIVEN the change-completion transaction fails before commit
- WHEN the transaction rolls back
- THEN no `webhook_outbox` row is left behind for that change

### Requirement: Relay poller delivers at-least-once with capped backoff

A relay poller MUST periodically claim due `pending` rows (`status = 'pending' AND next_attempt_at <= now()`) and POST each to `{memory_engine_url}/api/v1/worker/phase-archived` with the API-key header. On 2xx the row MUST be marked `delivered` (set `delivered_at`). On failure (network error, non-2xx, timeout) the row MUST stay `pending`, increment `attempts`, and set `next_attempt_at = now() + backoff(attempts)` where backoff grows exponentially and is capped at ~5 minutes. There is NO retry ceiling, NO dead-letter, and NO expiry — a row stays `pending` until delivered. Concurrent pollers MUST NOT double-claim the same row (row-level locking / `FOR UPDATE SKIP LOCKED`).

#### Scenario: Happy-path delivery marks delivered

- GIVEN a `pending` outbox row whose `next_attempt_at` is due
- WHEN the relay POSTs and ME returns 2xx
- THEN the row becomes `status = 'delivered'` with `delivered_at` set and is never POSTed again

#### Scenario: ME down during relay keeps row pending with backoff

- GIVEN ME is unreachable
- WHEN the relay attempts delivery
- THEN the row stays `pending`, `attempts` increments, and `next_attempt_at` advances by the capped exponential backoff
- AND no event is lost

#### Scenario: Backoff is capped near 5 minutes

- GIVEN a row has failed enough times that raw exponential backoff would exceed ~5 minutes
- WHEN the relay reschedules it
- THEN the computed `next_attempt_at` delay is clamped at the ~5-minute ceiling

#### Scenario: Orch restart with pending rows resumes delivery

- GIVEN one or more `pending` outbox rows exist
- WHEN orch restarts and the relay poller starts
- THEN the poller claims the due pending rows and delivers them with no manual intervention

#### Scenario: Duplicate delivery absorbed downstream

- GIVEN a row was delivered but the relay crashed before marking it `delivered`
- WHEN the relay re-POSTs the same event after restart
- THEN ME's `HasTopic` idempotency guard absorbs the duplicate and no second digest is produced
