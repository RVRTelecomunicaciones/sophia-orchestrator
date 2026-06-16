-- Migration 013: reeval-run audit trail (loop-hardening D1 follow-up).
-- Records each `reeval --apply --confirm` run as an immutable snapshot of the
-- per-skill status transitions it applied, so `reeval --revert <run-id>` can
-- replay the INVERSE transitions through the same PatchStatus guard. A revert
-- is itself recorded as a new run with mode='revert', preserving the append-only
-- audit semantics (snapshot + explicit inverse op + immutable audit log).
--
-- Additive-only: two new tables, no ALTER of existing schema.

BEGIN;

CREATE TABLE reeval_run (
    id          CHAR(26)    PRIMARY KEY,
    mode        TEXT        NOT NULL
        CHECK (mode IN ('apply','revert')),
    -- For mode='revert', the apply-run whose transitions were inverted.
    reverts_run_id CHAR(26),
    created_at  TIMESTAMPTZ NOT NULL
);

CREATE TABLE reeval_run_item (
    id           CHAR(26) PRIMARY KEY,
    run_id       CHAR(26) NOT NULL REFERENCES reeval_run(id) ON DELETE CASCADE,
    skill_id     CHAR(26) NOT NULL,
    prior_status TEXT     NOT NULL,
    new_status   TEXT     NOT NULL
);

CREATE INDEX idx_reeval_run_created      ON reeval_run(created_at DESC);
CREATE INDEX idx_reeval_run_item_run     ON reeval_run_item(run_id);

COMMIT;
