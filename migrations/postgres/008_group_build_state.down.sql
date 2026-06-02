-- Migration 008 rollback: remove group-level build-gate columns.
ALTER TABLE groups
    DROP COLUMN IF EXISTS build_status,
    DROP COLUMN IF EXISTS build_attempts;
