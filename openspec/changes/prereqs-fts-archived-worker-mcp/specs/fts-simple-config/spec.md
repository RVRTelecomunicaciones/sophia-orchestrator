# Delta: fts-simple-config

## Capability

Language-agnostic full-text-search configuration across the `memories`, `decisions`, and `heuristics` tables in `sophia-memory-engine`. Migration 005 switches the per-row `fts_language` REGCONFIG default from `'spanish'` to `'simple'` and back-fills existing rows so that English (and any non-Spanish) content is indexed without silent token loss. Code-side defaults in the domain model and the FTS adapter are updated to match.

**Source refs:** proposal §Scope item 1; explore §Item 1 (FTS config D12); explore §Migration concerns.

---

## ADDED Requirements

### Requirement: FTS Language Default — Simple

The system MUST default `fts_language` to `'simple'` for every new row inserted into `memories`, `decisions`, and `heuristics`.

#### Scenario: New memory row — simple tokenization

- GIVEN the `memories` table has `fts_language` column default `'simple'`
- WHEN a new `Memory` record is inserted without an explicit `fts_language` value
- THEN `fts_language` is `'simple'` on the persisted row
- AND `search_vector` is populated via the per-row trigger using the `simple` dictionary

#### Scenario: New decision row — simple tokenization

- GIVEN the `decisions` table has `fts_language` column default `'simple'`
- WHEN a new `Decision` record is inserted without an explicit `fts_language` value
- THEN `fts_language` is `'simple'` on the persisted row

#### Scenario: New heuristic row — simple tokenization

- GIVEN the `heuristics` table has `fts_language` column default `'simple'`
- WHEN a new `Heuristic` record is inserted without an explicit `fts_language` value
- THEN `fts_language` is `'simple'` on the persisted row

---

### Requirement: FTS Language Migration — Idempotent Backfill

Migration 005 MUST update all existing rows whose `fts_language` is `'spanish'` to `'simple'` across all three tables and MUST alter the column default. The migration MUST be idempotent (re-running it produces no error and no double-update).

#### Scenario: Existing row backfilled on up migration

- GIVEN a `memories` row with `fts_language = 'spanish'` and a `search_vector` built from Spanish tokenization
- WHEN migration `005_fts_simple.up.sql` runs
- THEN `fts_language` becomes `'simple'` on that row
- AND the per-row trigger fires on `UPDATE` and rebuilds `search_vector` using the `simple` dictionary

#### Scenario: Migration re-run is safe

- GIVEN migration `005_fts_simple.up.sql` has already run once
- WHEN it is run again
- THEN no rows are modified (the `WHERE fts_language = 'spanish'` predicate matches nothing)
- AND no error is raised

#### Scenario: Down migration reverses the change

- GIVEN migration `005_fts_simple.up.sql` has been applied
- WHEN migration `005_fts_simple.down.sql` runs
- THEN all rows with `fts_language = 'simple'` revert to `'spanish'`
- AND column defaults revert to `'spanish'` on all three tables

---

### Requirement: Per-Row FTS Language Column Preserved

The system MUST NOT hardcode the FTS dictionary in the trigger body. The trigger MUST continue to read `NEW.fts_language` per row so that the column remains overridable.

#### Scenario: Per-row override not broken by migration

- GIVEN a row inserted with an explicit `fts_language = 'english'`
- WHEN the trigger fires on insert
- THEN `search_vector` is built using the `english` dictionary, not `simple`

---

### Requirement: English Content Searchable After Migration

After migration 005 runs, an English text query MUST return rows whose content contains the searched term.

#### Scenario: English query returns relevant row (integration — testcontainers PG)

- GIVEN migration 005 has been applied to a test Postgres instance via testcontainers
- AND a `Memory` row with English content "orchestrator coordinates deterministic workflow" exists
- WHEN a FTS query for `'workflow'` is executed
- THEN the row appears in results

#### Scenario: Previously Spanish row is re-indexed and searchable in English

- GIVEN a row with `fts_language = 'spanish'` existed before migration 005
- WHEN migration 005 runs (backfill + trigger rebuild)
- AND an FTS query for an English term present in that row's content is executed
- THEN the row appears in results

---

### Requirement: Code-Side Default Matches Migration

The Go domain model default for `FTSLanguage` MUST be `"simple"` to match the database column default after migration 005.

#### Scenario: Zero-value struct uses simple

- GIVEN a `Memory` struct constructed without setting `FTSLanguage`
- WHEN it is persisted
- THEN the database row records `fts_language = 'simple'`
