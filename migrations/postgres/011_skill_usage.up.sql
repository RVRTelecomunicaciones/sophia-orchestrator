BEGIN;

CREATE TABLE skill_usage (
    id            CHAR(26) PRIMARY KEY,
    change_id     CHAR(26) NOT NULL,
    phase_type    TEXT     NOT NULL,
    skill_id      CHAR(26) NOT NULL,
    skill_version TEXT     NOT NULL,
    injected_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    outcome       TEXT     NOT NULL DEFAULT 'pending'
        CHECK (outcome IN ('pending','success','failure','blocked')),
    UNIQUE(change_id, phase_type, skill_id, skill_version)
);

CREATE INDEX idx_skill_usage_change         ON skill_usage(change_id);
CREATE INDEX idx_skill_usage_skill_injected ON skill_usage(skill_id, injected_at DESC);

COMMIT;
