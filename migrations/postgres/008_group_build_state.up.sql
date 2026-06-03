-- Migration 008: persist group-level build-gate state.
--
-- Adds three columns to the groups table:
--   build_status   — current GroupBuildStatus (pending/skipped/passed/failed).
--   build_attempts — how many build attempts have been made (0 = none yet).
--
-- Adds task-state columns that were previously discarded on hydration (V1 TODO):
--   task_status    — persisted on the tasks table (was ignored on read-back).
--   task_claimed_by — session_id of the claimer (already written, now read back).
--   task_attempts  — attempt counter (already written, now read back).
--   task_envelope  — completed envelope JSON (already written, now read back).
--
-- The tasks table already contains status/claimed_by/attempts/envelope columns
-- from migration 003; this migration adds no columns to tasks — it is a
-- documentation note that board_repo.go now reads those columns properly.
-- The only schema change is on the groups table.

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS build_status   TEXT NOT NULL DEFAULT 'pending',
    ADD COLUMN IF NOT EXISTS build_attempts INTEGER NOT NULL DEFAULT 0;
