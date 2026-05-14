-- phase_events: durable SSE event log enabling Last-Event-ID resume + restart
-- recovery. Audit rojo #3 fix.
--
-- Each SSE event the orchestrator publishes is appended here BEFORE being
-- broadcast in-memory. The autoincrement `id` is the canonical sequence the
-- SSE protocol echoes as `id:` and the CLI sends back as `Last-Event-ID`
-- on reconnect — monotonic per phase and globally.
--
-- Distinct from audit_log (005): audit_log is the system-of-record for
-- compliance + cross-phase queries; phase_events is the SSE replay buffer
-- with strict ordering guarantees per phase_id.
CREATE TABLE phase_events (
  id              BIGSERIAL PRIMARY KEY,
  phase_id        CHAR(26) NOT NULL,
  event_type      TEXT NOT NULL,
  payload         JSONB NOT NULL,
  trace_id        TEXT NOT NULL DEFAULT '',
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Replay query: WHERE phase_id=$1 AND id>$2 ORDER BY id.
-- Composite index supports both the equality on phase_id and the range on id.
CREATE INDEX phase_events_phase_idx ON phase_events(phase_id, id);
