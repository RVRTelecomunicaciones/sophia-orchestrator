-- changes: SDD Change aggregate root.
CREATE TABLE changes (
  id              CHAR(26) PRIMARY KEY,
  name            TEXT NOT NULL,
  project         TEXT NOT NULL,
  status          TEXT NOT NULL,
  current_phase   TEXT,
  artifact_store  TEXT NOT NULL,
  config_json     JSONB NOT NULL DEFAULT '{}',
  base_ref        TEXT,
  created_at      TIMESTAMPTZ NOT NULL,
  updated_at      TIMESTAMPTZ NOT NULL,
  UNIQUE (project, name)
);
CREATE INDEX changes_project_status_idx ON changes(project, status);
CREATE INDEX changes_status_idx ON changes(status) WHERE status = 'active';
