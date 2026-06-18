# Delta: app-layer-repo-errors

## Capability

Makes repository persistence errors observable in the apply hot path. Every call to `BoardRepo.SaveGroup`, `BoardRepo.SaveTask`, `SessionRepo.Save`, and `SpawnGov.Release` that currently discards its error via `_ =` MUST instead emit a structured error log AND record an audit signal. Control flow is not changed. Design invariant D1.2 is not violated further after this change lands.

## Severity-by-failure-class principle

Log severity follows the **failure class**, not the layer:

| Failure class | Examples | Severity |
|---|---|---|
| Persistence / data loss risk | `BoardRepo.SaveGroup`, `BoardRepo.SaveTask`, `SessionRepo.Save` | **ERROR** |
| Resource release / no data loss | `SpawnGov.Release` | **WARN** |
| Domain-transition (soft; state already advanced) | `RecordOutcome`, `group.Fail`, `group.Complete`, `task.Complete`, `MarkRunning`, `task.Release` | **WARN** (see app-layer-transition-errors spec) |

Persistence errors (Save calls) are logged at ERROR because a failed save risks data loss or state divergence. `SpawnGov.Release` is logged at WARN because the resource-release failure has no data-loss consequence — the token is simply leaked until the next process restart or governor reset.

## ADDED Requirements

### Requirement: Structured error log on repo Save failure

When any repo-save or resource-release call (`BoardRepo.SaveGroup`, `BoardRepo.SaveTask`, `SessionRepo.Save`, `SpawnGov.Release`) returns a non-nil error, the application layer MUST emit exactly one structured log entry containing all of the following fields:

| Field | Contract |
|---|---|
| `site` / `operation` | The specific call site or operation name (e.g. `"BoardRepo.SaveGroup"`, `"SessionRepo.Save"`) |
| Identity field | At minimum one of: `change_id`, `group_id`, or `task_id` — whichever is in scope at that call site |
| `error` | The error value returned by the call |

Severity follows the failure class (see table above): Save calls (`BoardRepo.SaveGroup`, `BoardRepo.SaveTask`, `SessionRepo.Save`) MUST be at **ERROR** severity. `SpawnGov.Release` MUST be at **WARN** severity. The log entry MUST be emitted even though the caller continues normally.

#### Scenario: BoardRepo.SaveGroup failure — log is emitted

- GIVEN `BoardRepo.SaveGroup` is called in the apply hot path
- AND `BoardRepo.SaveGroup` returns a non-nil error (e.g., transient Postgres error)
- WHEN the apply handler processes the event
- THEN exactly one ERROR-level log entry is emitted
- AND the log entry contains the operation name (`"BoardRepo.SaveGroup"` or equivalent)
- AND the log entry contains a `change_id` or `group_id` identifying the affected entity
- AND the log entry contains the error value
- AND the handler continues to its next step without returning or aborting the phase

#### Scenario: SessionRepo.Save failure — log is emitted

- GIVEN `SessionRepo.Save` is called in the apply hot path
- AND `SessionRepo.Save` returns a non-nil error
- WHEN the apply handler processes the event
- THEN exactly one ERROR-level log entry is emitted with the operation name, an identity field, and the error
- AND the handler does NOT abort or return early

#### Scenario: SpawnGov.Release failure — log is emitted

- GIVEN `SpawnGov.Release` is called at the end of an apply task
- AND `SpawnGov.Release` returns a non-nil error
- WHEN the handler reaches that call site
- THEN exactly one WARN-level log entry is emitted with the operation name and available identity
- AND the handler does NOT abort or return early

### Requirement: Audit signal on repo Save failure

When any repo-save call returns a non-nil error, the application layer MUST record an audit signal via the existing audit mechanism. The audit signal MUST be recorded at the same site as the log entry. Its failure MUST NOT abort the phase.

#### Scenario: SaveGroup error triggers audit entry

- GIVEN `BoardRepo.SaveGroup` returns a non-nil error
- WHEN the error is handled
- THEN an audit entry is appended via the existing audit mechanism carrying at minimum the operation name and error context
- AND the handler continues after the audit call regardless of the audit call's own return value

### Requirement: No phase abort on repo Save failure

A non-nil error from any repo-save call listed in this spec MUST NOT cause an early return, panic, or phase-level abort in the application layer. The log-and-audit-and-continue policy applies unconditionally to all Cluster 3 sites.

#### Scenario: SaveGroup and SaveTask both fail in one apply run

- GIVEN an apply run where `BoardRepo.SaveGroup` AND `BoardRepo.SaveTask` both return non-nil errors at different call sites in sequence
- WHEN apply processing reaches each site
- THEN each site emits its own ERROR-level log entry
- AND each site appends its own audit entry
- AND the apply phase does not abort between the two sites
- AND the apply phase completes to its normal terminal state

### Requirement: build_feedback.go sites are covered

The three repo-save call sites in `internal/application/apply/build_feedback.go` are subject to the same log + audit + continue contract as the sites in `teamlead.go` and `phase/service.go`. No file in the in-scope set is exempt.

#### Scenario: build_feedback repo-save failure — log is emitted

- GIVEN a repo-save call within `build_feedback.go` processing
- AND the call returns a non-nil error
- WHEN `build_feedback.go` processing executes
- THEN exactly one ERROR-level log entry is emitted for that site
- AND feedback processing continues

## UNCHANGED Requirements

### Requirement: Success path is unaffected

When a repo-save call returns nil, the behavior MUST be byte-identical to pre-change behavior: no log entry is emitted, no audit entry is appended, and control flow is unmodified.

#### Scenario: Successful Save — no spurious log or audit

- GIVEN a repo-save call returns nil
- WHEN the application layer processes the call
- THEN zero ERROR-level log entries are emitted for that call
- AND no additional audit entry is appended for that call
