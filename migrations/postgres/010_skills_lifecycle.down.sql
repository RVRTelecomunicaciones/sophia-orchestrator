-- Migration 010 rollback: drop lifecycle/metrics columns, swap UNIQUE back.
-- Idempotent via IF EXISTS guards (spec: skills-schema-migration-010).
-- golang-migrate wraps this file in a transaction automatically.

ALTER TABLE skills DROP CONSTRAINT IF EXISTS skills_name_version_unique;
ALTER TABLE skills ADD CONSTRAINT skills_name_unique UNIQUE (name);

DROP INDEX IF EXISTS idx_skills_applies_gin;
DROP INDEX IF EXISTS idx_skills_scope_gin;
DROP INDEX IF EXISTS idx_skills_status;

ALTER TABLE skills
  DROP CONSTRAINT IF EXISTS skills_activation_source_check,
  DROP CONSTRAINT IF EXISTS skills_risk_check,
  DROP CONSTRAINT IF EXISTS skills_status_check,
  DROP COLUMN     IF EXISTS last_validated_at,
  DROP COLUMN     IF EXISTS last_used_at,
  DROP COLUMN     IF EXISTS metrics,
  DROP COLUMN     IF EXISTS activation_source,
  DROP COLUMN     IF EXISTS risk_level,
  DROP COLUMN     IF EXISTS applies_when,
  DROP COLUMN     IF EXISTS scope,
  DROP COLUMN     IF EXISTS version,
  DROP COLUMN     IF EXISTS status;
