# Delta: skills-schema-migration-010

## Capability

A new migration pair `010_skills_lifecycle.{up,down}.sql` extends the `skills` table with 9 lifecycle/metrics columns, 3 CHECK constraints, 3 indexes, and atomically swaps the `skills_name_unique` constraint for `skills_name_version_unique UNIQUE (name, version)` — all inside a single golang-migrate transaction, reversible via a down migration. Targets Postgres 16+ (testcontainers PG version).

Refs: proposal §1, explore §1 §8, V4.1 §5.2.

---

## ADDED Requirements

### Requirement: Up migration adds lifecycle columns

The up migration MUST add the following columns to the `skills` table with safe defaults (so existing rows survive without a data backfill step):

| Column | Type | Default |
|---|---|---|
| `status` | `TEXT NOT NULL` | `'candidate'` |
| `version` | `TEXT NOT NULL` | `'v1'` |
| `scope` | `JSONB NOT NULL` | `'{}'` |
| `applies_when` | `JSONB NOT NULL` | `'{}'` |
| `risk_level` | `TEXT NOT NULL` | `'medium'` |
| `activation_source` | `TEXT NOT NULL` | `'manual'` |
| `metrics` | `JSONB NOT NULL` | `'{}'` |
| `last_used_at` | `TIMESTAMPTZ` | `NULL` |
| `last_validated_at` | `TIMESTAMPTZ` | `NULL` |

#### Scenario: Fresh DB applies up migration successfully

- GIVEN a Postgres 16+ database with migration 009 applied and 9 existing skill rows
- WHEN migration 010 up is applied
- THEN all 9 columns are present in `skills` (verified via `information_schema.columns`)
- AND all 9 existing rows retain their pre-migration `id`, `name`, `phases`, `content`, `techniques`, `created_at`, `updated_at` values

#### Scenario: Existing rows receive safe defaults after up

- GIVEN a `skills` table with 9 rows and no lifecycle columns
- WHEN migration 010 up is applied
- THEN every existing row has `status = 'candidate'`, `version = 'v1'`, `risk_level = 'medium'`, `activation_source = 'manual'`, `scope = '{}'`, `applies_when = '{}'`, `metrics = '{}'`
- AND `last_used_at` and `last_validated_at` are NULL

---

### Requirement: Up migration adds CHECK constraints

The up migration MUST add 3 CHECK constraints on the `skills` table:

- `status` constrained to: `'candidate'`, `'validated'`, `'active'`, `'deprecated'`, `'blocked'`, `'archived'` (V4.1 §5.2 — 6 values)
- `risk_level` constrained to: `'low'`, `'medium'`, `'high'`, `'critical'`
- `activation_source` constrained to: `'manual'`, `'legacy_seed'`, `'archive_worker'`, `'llm_proposal'`, `'imported'` (V4.1 §5.2 — 5 values)

#### Scenario: Invalid status value rejected by CHECK constraint

- GIVEN migration 010 up has been applied
- WHEN an INSERT with `status = 'unknown'` is attempted on the `skills` table
- THEN Postgres rejects the insert with a CHECK constraint violation

#### Scenario: Valid status value accepted

- GIVEN migration 010 up has been applied
- WHEN an INSERT with `status = 'active'` is attempted
- THEN the insert succeeds

---

### Requirement: Up migration adds indexes

The up migration MUST create the following indexes:

- `idx_skills_status` on `skills(status)`
- `idx_skills_scope_gin` GIN index on `skills(scope)`
- `idx_skills_applies_gin` GIN index on `skills(applies_when)`

#### Scenario: Indexes present after up migration

- GIVEN migration 010 up has been applied
- WHEN `pg_indexes` is queried for the `skills` table
- THEN all three index names are present

---

### Requirement: UNIQUE constraint swap is atomic

The up migration MUST, within a single transaction:

1. DROP CONSTRAINT `skills_name_unique`
2. ADD CONSTRAINT `skills_name_version_unique UNIQUE (name, version)`

The down migration MUST reverse this swap: DROP `skills_name_version_unique`, ADD `skills_name_unique`.

#### Scenario: UNIQUE constraint swap succeeds atomically

- GIVEN migration 009 is applied (constraint `skills_name_unique` exists)
- WHEN migration 010 up is applied
- THEN `skills_name_unique` no longer exists in `pg_constraint`
- AND `skills_name_version_unique` exists in `pg_constraint` with columns `(name, version)`

#### Scenario: Up + down round-trip restores pre-M1 schema

- GIVEN migration 010 up has been applied
- WHEN migration 010 down is applied
- THEN all 9 lifecycle columns are dropped from `skills`
- AND constraint `skills_name_version_unique` is dropped
- AND constraint `skills_name_unique` is restored
- AND the 3 indexes are dropped
- AND the table shape is identical to the post-009 baseline

---

### Requirement: Down migration is idempotent via IF EXISTS guards

Down migration MUST use `IF EXISTS` guards on DROP statements so re-running on an already-reverted schema does not error.

#### Scenario: Idempotent re-run of down migration

- GIVEN migration 010 down has already been applied once
- WHEN migration 010 down is applied again
- THEN no error is raised

---

### Requirement: Migration integration test validates schema

An integration test MUST apply migration 010 up against a testcontainers Postgres instance and verify schema via `information_schema.columns` and `pg_constraint`. A second test pass applies down and verifies revert.

#### Scenario: Integration test verifies up + schema

- GIVEN a testcontainers PG 16+ instance with migration 009 applied
- WHEN the integration test applies migration 010 up
- THEN the test asserts all 9 columns present in `information_schema.columns`
- AND the test asserts `skills_name_version_unique` in `pg_constraint`
- AND the test asserts `skills_name_unique` is absent

#### Scenario: Integration test verifies down + revert

- GIVEN migration 010 up has been applied in a testcontainers PG 16+ instance
- WHEN the integration test applies migration 010 down
- THEN the test asserts all 9 lifecycle columns absent from `information_schema.columns`
- AND the test asserts `skills_name_unique` restored in `pg_constraint`
