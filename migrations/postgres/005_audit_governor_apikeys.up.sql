CREATE TABLE audit_log (
  id              BIGSERIAL PRIMARY KEY,
  change_id       CHAR(26),
  phase_id        CHAR(26),
  session_id      CHAR(26),
  event_type      TEXT NOT NULL,
  payload         JSONB,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX audit_log_change_idx ON audit_log(change_id, created_at DESC);
CREATE INDEX audit_log_event_idx ON audit_log(event_type, created_at DESC);

-- Singleton row for SpawnGovernor active-process counter.
CREATE TABLE spawn_governor_state (
  id              SMALLINT PRIMARY KEY DEFAULT 1,
  active_count    INT NOT NULL DEFAULT 0,
  max_count       INT NOT NULL DEFAULT 4,
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CHECK (id = 1)
);
INSERT INTO spawn_governor_state (id, active_count, max_count, updated_at)
  VALUES (1, 0, 4, NOW());

-- API keys for orchestrator HTTP auth (V1 simple, V2 OIDC).
CREATE TABLE api_keys (
  id              CHAR(26) PRIMARY KEY,
  project         TEXT NOT NULL,
  key_hash        TEXT NOT NULL,
  name            TEXT,
  created_at      TIMESTAMPTZ NOT NULL,
  revoked_at      TIMESTAMPTZ
);
CREATE INDEX api_keys_hash_idx ON api_keys(key_hash) WHERE revoked_at IS NULL;
