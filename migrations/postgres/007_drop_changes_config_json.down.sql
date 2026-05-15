-- Reversibility: restore the column with its original 001 definition so
-- rollback yields a schema identical to pre-drop state.
ALTER TABLE changes ADD COLUMN config_json JSONB NOT NULL DEFAULT '{}';
