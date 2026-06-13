-- Migration 012: generic transactional outbox for durable outbound delivery.
--
-- Closes the only data-loss window in the live learning loop: the phase.archived
-- POST to memory-engine. The outbox row is INSERTed in the SAME transaction that
-- completes a change; a relay poller then delivers pending rows at-least-once with
-- capped exponential backoff. The table is generic (single producer today:
-- 'phase.archived') for forward compatibility.
--
-- Schema notes:
--   id              — CHAR(26) ULID; primary key (repo convention, injectable IDGenerator).
--   event_type      — logical event name (e.g. 'phase.archived').
--   payload         — BYTEA body delivered verbatim (byte-identical to the legacy
--                      POST). BYTEA, not JSONB: JSONB normalizes whitespace and
--                      reorders keys on storage, which would break the spec's
--                      byte-identical contract. The outbox is a generic blob carrier;
--                      it never interprets the payload, so opaque bytes are correct.
--   status          — 'pending' | 'delivered' (closed enum via CHECK).
--   attempts        — delivery attempt counter, bumped on each failed try.
--   next_attempt_at — earliest time the relay may (re)claim this row.
--   created_at      — immutable enqueue timestamp.
--   delivered_at    — set when status flips to 'delivered'; NULL while pending.
--
-- There is NO dead-letter column and NO expiry column: a row stays 'pending'
-- until delivered (retry forever).
--
-- Index:
--   idx_webhook_outbox_pending — partial index on (next_attempt_at) WHERE
--   status='pending'; supports the relay's due-row scan and FOR UPDATE SKIP LOCKED.

BEGIN;

CREATE TABLE webhook_outbox (
    id              CHAR(26)    PRIMARY KEY,
    event_type      TEXT        NOT NULL,
    payload         BYTEA       NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','delivered')),
    attempts        INT         NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL,
    delivered_at    TIMESTAMPTZ NULL
);

CREATE INDEX idx_webhook_outbox_pending
    ON webhook_outbox (next_attempt_at)
    WHERE status = 'pending';

COMMIT;
