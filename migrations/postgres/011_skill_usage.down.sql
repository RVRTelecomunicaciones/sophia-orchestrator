BEGIN;
DROP INDEX IF EXISTS idx_skill_usage_skill_injected;
DROP INDEX IF EXISTS idx_skill_usage_change;
DROP TABLE IF EXISTS skill_usage;
COMMIT;
