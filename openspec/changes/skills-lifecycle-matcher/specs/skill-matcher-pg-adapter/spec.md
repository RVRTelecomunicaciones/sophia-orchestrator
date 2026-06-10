# Delta: skill-matcher-pg-adapter

## Capability

A new PG adapter at `internal/adapters/outbound/pg/skill_matcher.go` implements the `SkillMatcher` interface. It executes the V4.1 §8 algorithm: active-only filter, scope filter (project_id, repo_id, phases), applies_when filter (feature_type, touched_paths via `doublestar/v4` glob, exclude_paths), deterministic sort, and skip-with-reason reporting. `FindByPhase` in `skill_repo.go` gains an `AND status = 'active'` filter. Both share the injected PG connection pool.

Refs: proposal §3 §5, explore §3 §7 §14 R5 R8, V4.1 §8.

---

## ADDED Requirements

### Requirement: SkillsForContext filters by status = 'active'

The adapter MUST SELECT only skills with `status = 'active'` as the first filter. Any skill without `status = 'active'` MUST appear in the `[]SkippedSkill` return with `Reason = "status_not_active"`.

#### Scenario: Non-active skill is returned as skipped

- GIVEN the `skills` table contains one skill with `status = 'candidate'` and one with `status = 'active'`
- WHEN `SkillsForContext` is called with an empty `SkillQuery`
- THEN the return `[]Skill` contains only the `active` skill
- AND the return `[]SkippedSkill` contains the `candidate` skill with `Reason = "status_not_active"`

---

### Requirement: SkillsForContext filters by scope

The adapter MUST apply scope filtering after the active filter:

- A skill's `scope.project_id` MUST match `SkillQuery.ProjectID` exactly OR be `"*"` (wildcard = match all)
- A skill's `scope.repo_id` MUST match `SkillQuery.RepoID` exactly OR be `"*"`
- A skill's `scope.phases` array MUST contain `SkillQuery.Phase` (if Phase is non-empty)
- Skills failing scope matching MUST appear in `[]SkippedSkill` with `Reason = "scope_mismatch"`

#### Scenario: Scope wildcard matches any project

- GIVEN a skill with `scope.project_id = "*"` and `scope.repo_id = "*"`
- WHEN `SkillsForContext` is called with `ProjectID = "proj-abc"` and `RepoID = "repo-xyz"`
- THEN the skill is included in the matched set

#### Scenario: Scope mismatch returns skipped

- GIVEN a skill with `scope.project_id = "proj-A"` and `scope.repo_id = "*"`
- WHEN `SkillsForContext` is called with `ProjectID = "proj-B"`
- THEN the skill appears in `[]SkippedSkill` with `Reason = "scope_mismatch"`
- AND it does NOT appear in `[]Skill`

#### Scenario: Phase filter applies

- GIVEN a skill with `scope.phases = ["apply", "verify"]`
- WHEN `SkillsForContext` is called with `Phase = "spec"`
- THEN the skill appears in `[]SkippedSkill` with `Reason = "scope_mismatch"`

---

### Requirement: SkillsForContext filters by applies_when

After scope filtering, the adapter MUST apply applies_when filters:

- If `SkillQuery.FeatureType` is non-empty, a skill's `applies_when.feature_type` (if present) MUST match; mismatch = skip with `Reason = "applies_when_failed"`
- If `SkillQuery.TouchedPaths` is non-empty and the skill has `applies_when.touched_paths`, at least one touched path MUST match at least one pattern via `doublestar/v4` `Match` — otherwise skip
- If `SkillQuery.TouchedPaths` is non-empty and the skill has `applies_when.exclude_paths`, any touched path matching any exclude pattern MUST cause the skill to be skipped (`Reason = "applies_when_failed"`), even if an include pattern also matched

#### Scenario: applies_when mismatch returns skipped

- GIVEN a skill with `applies_when.feature_type = "auth"`
- WHEN `SkillsForContext` is called with `FeatureType = "billing"`
- THEN the skill appears in `[]SkippedSkill` with `Reason = "applies_when_failed"`

#### Scenario: touched_paths glob match includes skill

- GIVEN a skill with `applies_when.touched_paths = ["internal/domain/**"]`
- WHEN `SkillsForContext` is called with `TouchedPaths = ["internal/domain/skill/skill.go"]`
- THEN the skill is included in the matched set

#### Scenario: exclude_paths wins over include match

- GIVEN a skill with `applies_when.touched_paths = ["**"]` and `applies_when.exclude_paths = ["vendor/**"]`
- WHEN `SkillsForContext` is called with `TouchedPaths = ["vendor/lib/foo.go"]`
- THEN the skill appears in `[]SkippedSkill` with `Reason = "applies_when_failed"`

---

### Requirement: SkillsForContext returns results in deterministic sort order

The matched skill set MUST be ordered by:

1. `risk_level` ascending with custom enum order: `low < medium < high < critical`
2. `last_validated_at` descending, NULLs last
3. `metrics.usage_count` descending

#### Scenario: Sort order correct with 3 skills of different risk levels

- GIVEN 3 active skills with `risk_level` values `"high"`, `"low"`, `"medium"` respectively
- WHEN `SkillsForContext` is called
- THEN the returned slice is ordered `low, medium, high`

---

### Requirement: FindByPhase adds status = 'active' filter

`FindByPhase` in `skill_repo.go` MUST add `AND status = 'active'` to its WHERE clause so future non-active skills do not pollute phase prompts.

#### Scenario: FindByPhase excludes non-active skills

- GIVEN the `skills` table has one `status = 'active'` skill and one `status = 'deprecated'` skill for the same phase
- WHEN `FindByPhase` is called for that phase
- THEN only the `active` skill is returned
- AND the `deprecated` skill is not present in the result

---

### Requirement: PG adapter uses injected connection pool

The adapter MUST receive the PG connection pool via constructor injection (wire). It MUST NOT open new connections independently.

#### Scenario: Adapter integration test uses shared testcontainers pool

- GIVEN a testcontainers PG 16+ instance with migration 010 applied and seeds backfilled
- WHEN `SkillsForContext` is called with a representative `SkillQuery{Phase: "apply", ProjectID: "*", RepoID: "*"}`
- THEN at least one skill is returned in the matched set
- AND the total of matched + skipped equals the total active skills in the DB
