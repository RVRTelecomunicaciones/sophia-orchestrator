# Delta: skills-seeder-backfill

## Capability

`seed_skills.go` switches from `InsertIfAbsent` to `Upsert`, supplying a full V4.1 §7 lifecycle payload for each of the 9 hybrid seeds on every boot. The Upsert is idempotent via the `UNIQUE (name, version)` constraint introduced in migration 010. Seeds become `status=active`, `activation_source=legacy_seed`, `version=v1` automatically on the first boot after M1 deploy.

Refs: proposal §4, explore §9, V4.1 §7.

---

## ADDED Requirements

### Requirement: Seeder uses Upsert with V4.1 §7 payload

`seed_skills.go` MUST call `Upsert` (not `InsertIfAbsent`) for each of the 9 seeds, supplying:

| Field | Value |
|---|---|
| `status` | `"active"` |
| `version` | `"v1"` |
| `activation_source` | `"legacy_seed"` |
| `risk_level` | `"medium"` |
| `scope` | `{"project_id":"*","repo_id":"*","phases":["<phase>"]}` |
| `applies_when` | `{}` (empty) |
| `metrics` | all numeric counts = 0 |

#### Scenario: First boot inserts 9 active seeds

- GIVEN a Postgres instance with migration 010 applied and no rows in `skills`
- WHEN the seeder runs for the first time
- THEN exactly 9 rows exist in `skills`
- AND every row has `status = 'active'`
- AND every row has `activation_source = 'legacy_seed'`
- AND every row has `version = 'v1'`

#### Scenario: Re-boot is a no-op (idempotent)

- GIVEN a Postgres instance with 9 active seeds already present from a previous boot
- WHEN the seeder runs again
- THEN the row count in `skills` remains 9
- AND no error is returned
- AND no row values are changed beyond what the Upsert payload explicitly sets

---

### Requirement: Upsert depends on UNIQUE (name, version) constraint

The seeder's Upsert MUST conflict on `(name, version)` and update lifecycle fields on conflict. It MUST NOT conflict on `(name)` alone (the old constraint is dropped in migration 010).

#### Scenario: Seed content change is propagated on re-boot

- GIVEN a seed already in `skills` with `risk_level = 'medium'`
- WHEN the seeder's Upsert payload for that seed has `risk_level = 'high'`
- THEN after the seeder runs, the row reflects `risk_level = 'high'`

---

### Requirement: Seeder is migration-order-dependent

The seeder MUST NOT run before migration 010 has been applied. In production, golang-migrate is invoked before the application bootstrap that runs the seeder.

#### Scenario: Seeder fails gracefully if lifecycle columns are absent

- GIVEN a Postgres instance with only migration 009 applied (no lifecycle columns)
- WHEN the seeder attempts to Upsert a seed with lifecycle fields
- THEN an error is returned (column not found) and the seeder does not silently corrupt data

---

### Requirement: Seeder integration test validates 9 active seeds

An integration test MUST apply migration 010, run the seeder against testcontainers PG, and assert all 9 rows are present with `status = 'active'` and `activation_source = 'legacy_seed'`. A second pass asserts idempotence. Test runs as part of `make test-integration`.

#### Scenario: Integration test asserts 9 seeds after seeder run

- GIVEN a testcontainers PG 16+ instance with migration 010 applied
- WHEN the integration test invokes the seed routine
- THEN a `SELECT COUNT(*) FROM skills WHERE status='active' AND activation_source='legacy_seed'` returns 9
- AND no error is returned

#### Scenario: Integration test asserts idempotence on second run

- GIVEN the seeder has already run once in the testcontainers PG instance
- WHEN the seeder is invoked a second time
- THEN `SELECT COUNT(*) FROM skills` still returns 9
- AND no error is returned
