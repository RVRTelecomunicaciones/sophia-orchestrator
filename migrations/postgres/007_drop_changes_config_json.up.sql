-- changes.config_json: drop unused column.
--
-- The column was created in 001_changes (NOT NULL DEFAULT '{}') but never
-- mapped into the Change domain struct nor referenced by ChangeRepo SQL —
-- a silent schema drift surfaced during the 2026-05-14 cross-repo audit.
--
-- Per YAGNI we drop it instead of mapping it. If V2 introduces per-change
-- configuration (e.g. dispatcher model overrides per-Change), the column
-- can be re-added with a single migration alongside the consuming code.
ALTER TABLE changes DROP COLUMN config_json;
