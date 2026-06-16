-- 014_phase_concerns: durably persist the advisory critic's Concern detail.
--
-- The advisory critic (governance-advisory GAP B) sets phase status
-- done_with_concerns and emits Concern{severity,category,message,evidence}
-- on the phase.completed_with_concerns SSE event, but the concern DETAIL was
-- not persisted — only the status survived on the phase row. An operator
-- reviewing a change post-hoc could not read what the concerns were.
--
-- This adds a nullable JSONB `concerns` column alongside the existing
-- `envelope` JSONB column. Concerns are kept OUT of the envelope JSON because
-- the envelope is the agent → orchestrator contract; concerns are critic
-- metadata derived afterwards. Additive and backward-compatible: existing
-- rows and phases without concerns stay NULL and read exactly as before.
-- Concerns are strictly advisory and do NOT affect phase status semantics.
BEGIN;

ALTER TABLE phases ADD COLUMN concerns JSONB;

COMMIT;
