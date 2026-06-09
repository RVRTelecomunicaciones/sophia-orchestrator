-- Migration 010: skills lifecycle + metrics + scope + applies_when (V4.1 §5.2).
-- Extends the schema from migration 009 with lifecycle metadata and atomically
-- swaps the UNIQUE constraint from (name) to (name, version) so the same name
-- can carry multiple versioned variants (V4.1 §5.5).
--
-- Safe defaults populate the 9 existing seed rows so the DB stays consistent
-- between this migration applying and the seeder Upsert (Option B, D-M1-4)
-- running on the next boot of the new binary.
--
-- Enum sets per V4.1 §5.2 (CORRECTED — 6 status, 5 activation_source, 4 risk_level):
--   status:            candidate, validated, active, deprecated, blocked, archived
--   risk_level:        low, medium, high, critical
--   activation_source: manual, legacy_seed, archive_worker, llm_proposal, imported

ALTER TABLE skills
  ADD COLUMN status             TEXT        NOT NULL DEFAULT 'candidate',
  ADD COLUMN version            TEXT        NOT NULL DEFAULT 'v1',
  ADD COLUMN scope              JSONB       NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN applies_when       JSONB       NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN risk_level         TEXT        NOT NULL DEFAULT 'medium',
  ADD COLUMN activation_source  TEXT        NOT NULL DEFAULT 'manual',
  ADD COLUMN metrics            JSONB       NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN last_used_at       TIMESTAMPTZ,
  ADD COLUMN last_validated_at  TIMESTAMPTZ,
  ADD CONSTRAINT skills_status_check
    CHECK (status IN ('candidate','validated','active','deprecated','blocked','archived')),
  ADD CONSTRAINT skills_risk_check
    CHECK (risk_level IN ('low','medium','high','critical')),
  ADD CONSTRAINT skills_activation_source_check
    CHECK (activation_source IN ('manual','legacy_seed','archive_worker','llm_proposal','imported'));

CREATE INDEX IF NOT EXISTS idx_skills_status      ON skills (status);
CREATE INDEX IF NOT EXISTS idx_skills_scope_gin   ON skills USING GIN (scope);
CREATE INDEX IF NOT EXISTS idx_skills_applies_gin ON skills USING GIN (applies_when);

ALTER TABLE skills DROP CONSTRAINT skills_name_unique;
ALTER TABLE skills ADD CONSTRAINT skills_name_version_unique UNIQUE (name, version);
