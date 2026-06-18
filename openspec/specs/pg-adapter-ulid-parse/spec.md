# pg-adapter-ulid-parse Specification

> Source of truth (synced from change `apply-error-handling-hardening`, archived 2026-06-18).
> Covers Cluster 4 of the error-hardening audit: ULID parse errors in the pg adapter layer.
> PR #111 (branch `fix/apply-error-handling-pg-adapter`) merged to main 2026-06-18.

## Purpose

ULID parse calls in `board_repo.go`, `session_repo.go`, and `worktree_repo.go` previously
discarded their error return via `_ =`, allowing zero-value domain IDs to propagate downstream
silently and corrupt lookups and audit entries. This capability mandates that every
`ids.Parse*()` error is observed: scan-loop sites log and skip the bad row; single-row
hydrator sites log and return the error. A zero-value domain ID MUST NEVER be returned by any
pg adapter scan path.

## Requirements

### Requirement: Zero-value ID is never returned downstream

No function in any file under `internal/adapters/outbound/pg/` MUST return a domain ID that
carries the zero value (all-zero ULID, empty string, or equivalent zero representation) as a
result of a ULID parse failure. This prohibition applies to both list-returning (scan-loop)
paths and single-entity-returning paths.

A test MUST be able to assert this property by injecting a malformed ULID string and confirming
that the returned values contain no zero-value ID.

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

In any scan loop that iterates over multiple rows, when a ULID parse call returns a non-nil
error:

1. The system MUST emit exactly one structured error-level log entry identifying at minimum: the repo name (or source file), the column name being parsed, and the raw value that failed to parse.
2. The current row MUST be skipped (not appended to the result slice).
3. The loop MUST continue to the next row.
4. The returned slice MUST contain all successfully-parsed rows.

No additional return or abort at the list-query function level is required by this spec for
scan-loop sites.

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

In any single-row hydration function (a function that fetches and parses exactly one entity),
when a ULID parse call returns a non-nil error:

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

### Requirement: Valid ULID columns parse without change

When all ULID columns in a row parse successfully, the scan-loop and single-row hydration
behavior MUST be byte-identical to pre-change behavior: no log entry is emitted, the row is
included in results (scan loop) or returned as a valid entity (single-row hydrator), and no
error is returned as a result of this change.

#### Scenario: All columns parse successfully — no change in behavior

- GIVEN a scan loop where all rows contain valid ULID strings
- WHEN the scan loop runs
- THEN the returned slice is identical to the pre-change output
- AND no ERROR-level log entries are emitted for any ULID parse operation in that run

### Requirement: No new test regressions from adapter-layer changes

`go test ./...` MUST pass green after all Cluster 4 changes are applied.

#### Scenario: Full test suite passes after Cluster 4 changes

- GIVEN all Cluster 4 `_ =` sites in `board_repo.go`, `session_repo.go`, and `worktree_repo.go` have been updated
- WHEN `go test ./...` is run from the repo root
- THEN all previously-passing tests continue to pass
- AND the new unit tests covering zero-value ID prevention pass

## Implementation note (as shipped)

- **Scan-loop implementation** — `findGroupsByBoard` and `findTasksByGroup` were refactored to
  extract `scanGroupRow` and `scanTaskRow` helper functions. The helpers return an error on any
  ULID parse failure; the callers implement the `continue` (skip-and-proceed) logic. This
  structure made the scan-loop skip behavior testable at the helper level.
- **17 sites implemented** — all confirmed correct per verify report obs #941: 11 in
  `board_repo.go`, 4 in `session_repo.go`, 2 in `worktree_repo.go`.
- **`slog` import** — added to all three pg files; `board_repo.go`, `session_repo.go`,
  `worktree_repo.go` did not previously import `log/slog`.
- **Context-less log in helpers** — `scanGroupRow` and `scanTaskRow` use
  `slog.Default().Error(...)` (no context) because they do not receive `ctx`. Single-hydrator
  functions use `slog.Default().ErrorContext(ctx, ...)`. This is acceptable — context-less is
  still ERROR-level and structured.
- **Accepted follow-up (PR2 WARNING-1)** — the spec scenario "One bad ULID row among valid
  rows — log + skip + valid rows returned" was not fully tested at the loop level before push.
  A mutation guard test was added before push (verified: `continue` → `return nil, err`
  makes 6/6 corrupt-row tests fail = load-bearing). The integration-level multi-row scenario
  (testcontainers) remains a recommended follow-up for full end-to-end confidence.
- **Accepted follow-up (PR2 SUGGESTION-1)** — consider passing `ctx` to `scanGroupRow` and
  `scanTaskRow` in a future change to include trace/request IDs in scan-loop log entries.
- **Behavioral consequence** — `findGroupsByBoard` returns N-1 groups when one row has a
  corrupt ULID. This is an observable consequence of data corruption, not a silent failure.
  The spec explicitly accepts this tradeoff.
