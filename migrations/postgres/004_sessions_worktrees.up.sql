CREATE TABLE agent_sessions (
  id              CHAR(26) PRIMARY KEY,
  change_id       CHAR(26) NOT NULL REFERENCES changes(id) ON DELETE CASCADE,
  phase_id        CHAR(26) NOT NULL REFERENCES phases(id) ON DELETE CASCADE,
  agent_role      TEXT NOT NULL,
  provider        TEXT NOT NULL,
  worktree_id     CHAR(26),
  prompt_sha256   TEXT NOT NULL,
  command         TEXT NOT NULL,
  status          TEXT NOT NULL,
  exit_code       INT,
  envelope        JSONB,
  started_at      TIMESTAMPTZ NOT NULL,
  ended_at        TIMESTAMPTZ
);
CREATE INDEX agent_sessions_change_idx ON agent_sessions(change_id);
CREATE INDEX agent_sessions_phase_idx ON agent_sessions(phase_id);

CREATE TABLE worktrees (
  id              CHAR(26) PRIMARY KEY,
  session_id      CHAR(26) REFERENCES agent_sessions(id) ON DELETE SET NULL,
  path            TEXT NOT NULL,
  branch          TEXT NOT NULL,
  status          TEXT NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL,
  cleaned_at      TIMESTAMPTZ
);
CREATE UNIQUE INDEX worktrees_path_active_idx ON worktrees(path)
  WHERE status <> 'cleaned';
