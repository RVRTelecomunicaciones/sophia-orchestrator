-- phases: one execution of a SDD phase. Idempotency via UNIQUE
-- (change_id, phase_type, attempts) — replay-everything per spec § 1.5 R10.
CREATE TABLE phases (
  id              CHAR(26) PRIMARY KEY,
  change_id       CHAR(26) NOT NULL REFERENCES changes(id) ON DELETE CASCADE,
  phase_type      TEXT NOT NULL,
  status          TEXT NOT NULL,
  envelope        JSONB,
  confidence      NUMERIC(3,2),
  retry_budget    INT NOT NULL DEFAULT 3,
  attempts        INT NOT NULL DEFAULT 0,
  started_at      TIMESTAMPTZ,
  completed_at    TIMESTAMPTZ,
  UNIQUE (change_id, phase_type, attempts)
);
CREATE INDEX phases_change_idx ON phases(change_id);
CREATE INDEX phases_running_idx ON phases(change_id, status)
  WHERE status IN ('running', 'interrupted');
