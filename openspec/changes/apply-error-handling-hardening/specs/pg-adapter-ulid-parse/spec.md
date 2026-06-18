# Delta: pg-adapter-ulid-parse

## Capability

Prevents zero-value domain IDs from propagating downstream when a ULID column fails to parse in any pg adapter. Every `ids.Parse*()` call that currently discards its error via `_ =` MUST log the failure and then either skip the bad row (scan loops) or surface the error to the caller (single-row hydrators). A zero-value ID MUST NEVER be returned by any pg adapter scan path after this change lands.

## ADDED Requirements

### Requirement: Zero-value ID is never returned downstream

No function in any file under `internal/adapters/outbound/pg/` MUST return a domain ID that carries the zero value (all-zero ULID, empty string, or equivalent zero representation) as a result of a ULID parse failure. This prohibition applies to both list-returning (scan-loop) paths and single-entity-returning paths.

A test MUST be able to assert this property by injecting a malformed ULID string and confirming that the returned values contain no zero-value ID.

#### Scenario: Malformed ULID in board_repo list query — zero-value ID not returned

- GIVEN a database query in `board_repo.go` returns rows where one row contains a malformed ULID string in a scanned ID column
- WHEN the scan loop processes those rows
- THEN the returned slice MUST NOT contain any entry whose ID field is the zero value
- AND the remaining valid rows MUST be returned intact

#### Scenario: Malformed ULID in session_repo list query — zero-value ID not returned

- GIVEN a query in `session_repo.go` returns rows where one row contains a malformed ULID string
- WHEN the scan loop processes those rows
- THEN the returned slice MUST NOT contain any entry whose ID field is the zero value

#### Scenario: Malformed ULID in worktree_repo query — zero-value ID not returned

- GIVEN a query in `worktree_repo.go` returns a row with a malformed ULID string in an ID column
- WHEN the hydration function runs
- THEN the returned value MUST either be an error (not a zero-value entity) or the row MUST be excluded from results
- AND no zero-value ID is present in any returned value

### Requirement: Scan-loop sites log and skip the bad row

In any scan loop that iterates over multiple rows, when a ULID parse call returns a non-nil error:

1. The system MUST emit exactly one structured error-level log entry identifying at minimum: the repo name (or source file), the column name being parsed, and the raw value that failed to parse.
2. The current row MUST be skipped (not appended to the result slice).
3. The loop MUST continue to the next row.
4. The returned slice MUST contain all successfully-parsed rows.

No additional return or abort at the list-query function level is required by this spec for scan-loop sites.

#### Scenario: One bad ULID row among valid rows — log + skip + valid rows returned

- GIVEN a scan loop over 3 rows where row 2 has an unparseable ULID in column `group_id`
- WHEN the scan loop runs
- THEN exactly one ERROR-level log entry is emitted identifying the repo, the `group_id` column, and the raw bad value
- AND the returned slice contains rows 1 and 3
- AND row 2 is not present in the returned slice
- AND no panic or error is returned to the caller of the list function (scan-loop policy)

#### Scenario: All rows are bad — empty slice returned, each row logged

- GIVEN a scan loop over 2 rows where both have malformed ULIDs
- WHEN the scan loop runs
- THEN two ERROR-level log entries are emitted (one per bad row)
- AND an empty slice is returned
- AND no panic occurs

### Requirement: Single-row hydrator sites log and return an error

In any single-row hydration function (a function that fetches and parses exactly one entity), when a ULID parse call returns a non-nil error:

1. The system MUST emit exactly one structured error-level log entry identifying at minimum: the repo name (or source file), the column name, and the raw value.
2. The function MUST return a non-nil error to its caller.
3. The function MUST NOT return a partially-constructed entity whose ID field is the zero value alongside a nil error.

#### Scenario: Single-row hydration with bad ULID — error returned to caller

- GIVEN a single-row hydration function in any pg repo
- AND the query returns a row where one ULID column contains a malformed value
- WHEN the hydration function runs
- THEN exactly one ERROR-level log entry is emitted with the repo name, column name, and raw bad value
- AND the function returns a non-nil error
- AND the function does NOT return a domain entity alongside the non-nil error (caller-visible contract: error XOR valid entity)

#### Scenario: Hydration caller observes error — does not use zero-value entity

- GIVEN a caller of a single-row hydration function
- AND the hydration function returns a non-nil error due to a ULID parse failure
- WHEN the caller receives the return values
- THEN the caller can detect the error via its non-nil error return
- AND the caller does not receive a domain entity with a zero-value ID alongside a nil error

### Requirement: Error log field contract for ULID parse failures

Every ERROR-level log entry emitted for a ULID parse failure MUST contain:

| Field | Contract |
|---|---|
| Repo / source identifier | Name identifying which pg adapter emitted the log (e.g., `"board_repo"`, `"session_repo"`, `"worktree_repo"`) |
| Column name | The name of the database column whose value failed to parse |
| Raw value | The actual string value that was present in the column |
| `error` | The parse error returned by the `ids.Parse*()` call |

#### Scenario: Log entry completeness for ULID parse failure

- GIVEN a ULID parse failure in `board_repo.go` on column `task_id` with raw value `"not-a-ulid"`
- WHEN the error-handling path runs
- THEN the emitted log entry contains all four required fields: a repo identifier, the column name `task_id`, the raw value `"not-a-ulid"`, and the parse error

## UNCHANGED Requirements

### Requirement: Valid ULID columns parse without change

When all ULID columns in a row parse successfully, the scan-loop and single-row hydration behavior MUST be byte-identical to pre-change behavior: no log entry is emitted, the row is included in results (scan loop) or returned as a valid entity (single-row hydrator), and no error is returned as a result of this change.

#### Scenario: All columns parse successfully — no change in behavior

- GIVEN a scan loop where all rows contain valid ULID strings
- WHEN the scan loop runs
- THEN the returned slice is identical to the pre-change output
- AND no ERROR-level log entries are emitted for any ULID parse operation in that run
