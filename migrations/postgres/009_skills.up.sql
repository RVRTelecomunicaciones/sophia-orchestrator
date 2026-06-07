-- Migration 009: introduce the skills table for persisted prompt-guidance units.
--
-- Each Skill is a named, operator-editable guidance block injected into SDD
-- phase prompts. Skills are seeded at boot via insert-if-absent semantics so
-- operator runtime edits are never clobbered by restarts (ADR-0011).
--
-- Schema notes:
--   id           — CHAR(26) ULID; primary key.
--   name         — unique human-readable identifier (used as seed key).
--   phases       — TEXT[] of canonical SDD phase names this Skill applies to.
--   content      — full guidance text; the runtime source of truth.
--   techniques   — TEXT[] of allowed cognitive technique tags.
--   created_at   — immutable creation timestamp.
--   updated_at   — bumped on every Update call.
--
-- Indexes:
--   GIN(phases)  — supports efficient ANY(phases) look-ups in FindByPhase.
--   UNIQUE(name) — enforced by the domain; kept at schema level as a guard.

CREATE TABLE IF NOT EXISTS skills (
    id          CHAR(26)                NOT NULL,
    name        TEXT                    NOT NULL,
    phases      TEXT[]                  NOT NULL,
    content     TEXT                    NOT NULL,
    techniques  TEXT[]                  NOT NULL,
    created_at  TIMESTAMPTZ             NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ             NOT NULL DEFAULT now(),
    CONSTRAINT  skills_pkey PRIMARY KEY (id),
    CONSTRAINT  skills_name_unique UNIQUE (name)
);

CREATE INDEX IF NOT EXISTS skills_phases_gin ON skills USING GIN (phases);
