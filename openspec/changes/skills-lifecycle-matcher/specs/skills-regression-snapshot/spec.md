# Delta: skills-regression-snapshot

## Capability

Nine golden JSON files capture the pre-migration `FindByPhase` output for each SDD phase. A snapshot integration test applies migration 010, runs the seeder, then asserts `SkillsForPhase` for each of the 9 phases returns byte-equivalent results against the golden files. This is a BLOCKING CI gate: any divergence means M1 broke PR #76 behavior. Goldens live outside the 400-LoC review budget.

Refs: proposal §8, explore §10, operator decisions 9 §10.

---

## ADDED Requirements

### Requirement: Golden files capture pre-migration baseline

Golden files MUST be created at `internal/adapters/outbound/pg/testdata/skill_phase_baseline/<phase>.golden.json` for each of the 9 phases: `init`, `explore`, `propose`, `spec`, `design`, `tasks`, `apply`, `verify`, `archive`.

Each golden file MUST record, per skill returned by `FindByPhase` before migration 010:

- `skill_id` (string)
- `content_sha256` (hex string of SHA-256 of the skill's content field)

The golden format MUST be deterministic: skills MUST be sorted by `skill_id` ascending in the JSON array.

#### Scenario: Golden file is present for each of 9 phases

- GIVEN the snapshot test begins execution
- WHEN it looks for `testdata/skill_phase_baseline/<phase>.golden.json` for each phase
- THEN all 9 files are found on disk
- AND any missing file causes the test to fail immediately with a clear error naming the missing phase

---

### Requirement: Snapshot test asserts byte-equivalent results post-M1

After applying migration 010 and running the seeder, the snapshot test MUST call `SkillsForPhase` for each of the 9 phases and compare the result against the golden file.

Comparison MUST be:

- Same number of skills
- Same `skill_id` values (set equality, ignoring order in comparison — golden is sorted; test sorts results before compare)
- Same `content_sha256` per skill

Any divergence MUST cause the test to FAIL with a diff output naming the phase and the first diverging field.

#### Scenario: All 9 phases match golden after migration + backfill

- GIVEN migration 010 has been applied and the seeder has run in a testcontainers PG 16+ instance
- WHEN the snapshot test calls `SkillsForPhase` for each of the 9 phases
- THEN each result set matches its golden file exactly (same IDs, same content hashes)
- AND the test reports all 9 phases as PASS

#### Scenario: Phase divergence causes test failure with diff

- GIVEN migration 010 has been applied and the seeder has run
- AND one skill's content was unintentionally altered by the migration
- WHEN the snapshot test runs for the affected phase
- THEN the test FAILS
- AND the failure output identifies the phase name and the diverging skill's ID

---

### Requirement: Missing golden file fails fast with clear error

If a golden file is absent for any phase, the test MUST NOT silently pass or skip. It MUST fail immediately and name the missing phase.

#### Scenario: Missing golden file produces actionable failure

- GIVEN `testdata/skill_phase_baseline/apply.golden.json` does not exist
- WHEN the snapshot test runs
- THEN it fails with an error message containing `"missing golden file"` and `"apply"`

---

### Requirement: Snapshot test is part of make test-integration

The snapshot test MUST run under `make test-integration` (testcontainers, real PG). It MUST NOT run under `make test-unit`.

#### Scenario: Snapshot test skipped under unit test target

- GIVEN the codebase is built and `make test-unit` is run
- WHEN the unit test suite executes
- THEN the snapshot integration test is NOT run (no testcontainers startup, no DB connection attempted)

#### Scenario: Snapshot test runs under integration test target

- GIVEN a Docker environment is available for testcontainers
- WHEN `make test-integration` is run
- THEN the snapshot test executes and all 9 phases are validated
