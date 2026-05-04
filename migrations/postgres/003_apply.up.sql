-- apply_boards / groups / tasks: apply phase parallel coordination state.
CREATE TABLE apply_boards (
  id              CHAR(26) PRIMARY KEY,
  phase_id        CHAR(26) NOT NULL REFERENCES phases(id) ON DELETE CASCADE UNIQUE,
  status          TEXT NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE groups (
  id              CHAR(26) PRIMARY KEY,
  board_id        CHAR(26) NOT NULL REFERENCES apply_boards(id) ON DELETE CASCADE,
  name            TEXT NOT NULL,
  depends_on      CHAR(26)[] NOT NULL DEFAULT '{}',
  status          TEXT NOT NULL,
  worktree_path   TEXT,
  branch_name     TEXT
);
CREATE INDEX groups_board_idx ON groups(board_id);

CREATE TABLE tasks (
  id              CHAR(26) PRIMARY KEY,
  group_id        CHAR(26) NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  description     TEXT NOT NULL,
  files_pattern   TEXT[] NOT NULL,
  status          TEXT NOT NULL,
  claimed_by      CHAR(26),
  attempts        INT NOT NULL DEFAULT 0,
  envelope        JSONB
);
CREATE INDEX tasks_group_idx ON tasks(group_id);
CREATE INDEX tasks_pending_idx ON tasks(group_id) WHERE status = 'pending';
