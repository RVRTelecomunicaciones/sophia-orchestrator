# apply-error-observability Specification

> Source of truth (synced from change `apply-error-handling-hardening`, archived 2026-06-18).
> Covers Clusters 2 and 3 of the error-hardening audit: domain state-machine transition errors
> and repository persistence errors in the apply hot path. PR #110 (branch
> `fix/apply-error-handling-app-layer`) merged to main 2026-06-18.

## Purpose

Three clusters of silently-discarded errors existed in the apply pipeline. Clusters 2 and 3
are the application-layer half: `_ =` discards on domain-transition calls (`RecordOutcome`,
`group.Fail`, `group.Complete`, `task.Complete`, `MarkRunning`, `task.Release`) and on
repository persistence calls (`BoardRepo.SaveGroup`, `BoardRepo.SaveTask`, `SessionRepo.Save`,
`SpawnGov.Release`). This capability mandates that every such failure is made observable via
structured logging and an audit trail, while preserving existing control flow (log-and-continue
policy). It also locks the regression contract: the success path MUST remain byte-identical.

## Severity-by-failure-class principle

Log severity follows the failure class, not the layer:

| Failure class | Examples | Severity |
|---|---|---|
| Persistence / data loss risk | `BoardRepo.SaveGroup`, `BoardRepo.SaveTask`, `SessionRepo.Save` | **ERROR** |
| Resource release / no data loss | `SpawnGov.Release` | **WARN** |
| Domain-transition (soft; state already advanced) | `RecordOutcome`, `group.Fail`, `group.Complete`, `task.Complete`, `MarkRunning`, `task.Release` | **WARN** |

Persistence errors (Save calls) are logged at ERROR because a failed save risks data loss or
state divergence. `SpawnGov.Release` is logged at WARN because resource-release failure has no
data-loss consequence. Domain-transition errors are logged at WARN because the domain state has
already advanced or the transition is idempotent.

## Requirements

### Requirement: Structured error log on domain-transition failure

When any domain-transition call (`RecordOutcome`, `group.Fail`, `group.Complete`, `task.Complete`,
`MarkRunning`, `task.Release`) returns a non-nil error, the application layer MUST emit exactly
one structured log entry containing all of the following fields:

| Field | Contract |
|---|---|
| `site` / `operation` | The specific call site or operation name (e.g. `"group.Fail"`, `"task.Complete"`) |
| Identity field | At minimum one of: `change_id`, `group_id`, or `task_id` — whichever is in scope at that call site |
| `error` | The error value returned by the call |

The log entry MUST be at **WARN** severity. The log entry MUST NOT be omitted even if the caller
continues normally.

#### Scenario: group.Fail returns error — log is emitted

- GIVEN an application layer handler where `group.Fail` is invoked on a domain group
- AND `group.Fail` returns a non-nil error (e.g., invalid state transition)
- WHEN the handler processes the event
- THEN exactly one WARN-level log entry is emitted
- AND the log entry contains the operation name (`"group.Fail"` or equivalent site identifier)
- AND the log entry contains a `change_id` or `group_id` identifying the affected entity
- AND the log entry contains the error value
- AND the handler continues to its next step without returning or aborting the phase

#### Scenario: task.Complete returns error — log is emitted

- GIVEN an application layer handler where `task.Complete` is invoked on a domain task
- AND `task.Complete` returns a non-nil error
- WHEN the handler processes the event
- THEN exactly one WARN-level log entry is emitted containing the operation name, a task or group identity, and the error
- AND the handler does NOT abort or return early

#### Scenario: RecordOutcome returns error — log is emitted

- GIVEN a call to `RecordOutcome` in the application layer
- AND the call returns a non-nil error
- WHEN the site is reached during apply processing
- THEN exactly one WARN-level log entry is emitted with the operation name, an identity field in scope, and the error
- AND execution continues to the next line after the call

### Requirement: Audit signal on domain-transition failure

When any domain-transition call returns a non-nil error, the application layer MUST record an
audit signal via the existing audit mechanism (`outbound.AuditLog.Append`). The audit signal
MUST be recorded at the same site as the log entry. Its persistence failure MUST NOT abort the
phase (consistent with the log-and-continue policy).

#### Scenario: Transition error triggers audit entry

- GIVEN `group.Complete` returns a non-nil error during apply processing
- WHEN the error is handled
- THEN an audit entry is appended via the existing audit mechanism
- AND the audit entry is distinguishable from a success-path audit entry (carries error context or an error-class marker)
- AND the handler continues after the audit call regardless of the audit call's own return value

### Requirement: No phase abort on domain-transition failure

A non-nil error from any domain-transition call listed in this spec MUST NOT cause an early
return, panic, or phase-level abort in the application layer. The log-and-audit-and-continue
policy applies unconditionally to all Cluster 2 sites.

#### Scenario: Multiple transition failures in one apply run — all are logged, none abort

- GIVEN an apply run where `group.Fail` AND `task.Release` both return non-nil errors at different call sites
- WHEN apply processing reaches each site in sequence
- THEN each site emits its own WARN-level log entry
- AND each site appends its own audit entry
- AND the apply phase does not abort between the two sites
- AND the apply phase completes to its normal terminal state

### Requirement: Structured error log on repo Save failure

When any repo-save or resource-release call (`BoardRepo.SaveGroup`, `BoardRepo.SaveTask`,
`SessionRepo.Save`, `SpawnGov.Release`) returns a non-nil error, the application layer MUST
emit exactly one structured log entry containing all of the following fields:

| Field | Contract |
|---|---|
| `site` / `operation` | The specific call site or operation name (e.g. `"BoardRepo.SaveGroup"`, `"SessionRepo.Save"`) |
| Identity field | At minimum one of: `change_id`, `group_id`, or `task_id` — whichever is in scope at that call site |
| `error` | The error value returned by the call |

Severity follows the failure class (see table above). The log entry MUST be emitted even though
the caller continues normally.

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

When any repo-save call returns a non-nil error, the application layer MUST record an audit
signal via the existing audit mechanism. The audit signal MUST be recorded at the same site as
the log entry. Its failure MUST NOT abort the phase.

#### Scenario: SaveGroup error triggers audit entry

- GIVEN `BoardRepo.SaveGroup` returns a non-nil error
- WHEN the error is handled
- THEN an audit entry is appended via the existing audit mechanism carrying at minimum the operation name and error context
- AND the handler continues after the audit call regardless of the audit call's own return value

### Requirement: No phase abort on repo Save failure

A non-nil error from any repo-save call listed in this spec MUST NOT cause an early return,
panic, or phase-level abort in the application layer. The log-and-audit-and-continue policy
applies unconditionally to all Cluster 3 sites.

#### Scenario: SaveGroup and SaveTask both fail in one apply run

- GIVEN an apply run where `BoardRepo.SaveGroup` AND `BoardRepo.SaveTask` both return non-nil errors at different call sites in sequence
- WHEN apply processing reaches each site
- THEN each site emits its own ERROR-level log entry
- AND each site appends its own audit entry
- AND the apply phase does not abort between the two sites
- AND the apply phase completes to its normal terminal state

### Requirement: build_feedback.go sites are covered

The three repo-save call sites in `internal/application/apply/build_feedback.go` are subject to
the same log + audit + continue contract as the sites in `teamlead.go` and `phase/service.go`.
No file in the in-scope set is exempt.

#### Scenario: build_feedback repo-save failure — log is emitted

- GIVEN a repo-save call within `build_feedback.go` processing
- AND the call returns a non-nil error
- WHEN `build_feedback.go` processing executes
- THEN exactly one ERROR-level log entry is emitted for that site
- AND feedback processing continues

### Requirement: Apply hot path produces identical results on success

All outputs of the apply hot path (persisted state, emitted events, returned envelopes) MUST be
identical to pre-change outputs when all domain-transition calls and repo-save calls return nil.

#### Scenario: Full apply run with no errors — behavior is unchanged

- GIVEN an apply run where every domain-transition call (`RecordOutcome`, `group.Fail`, `group.Complete`, `task.Complete`, `MarkRunning`, `task.Release`) returns nil
- AND every repo-save call (`BoardRepo.SaveGroup`, `BoardRepo.SaveTask`, `SessionRepo.Save`, `SpawnGov.Release`) returns nil
- WHEN the apply phase completes
- THEN the persisted board state, session state, and audit log are identical to pre-change behavior
- AND zero ERROR-level log entries are emitted by any Cluster 2 or Cluster 3 site

### Requirement: No new test regressions from application-layer changes

`go test ./...` MUST pass green after all Cluster 2 and Cluster 3 changes are applied.

#### Scenario: Full test suite passes after Cluster 2+3 changes

- GIVEN all Cluster 2 and Cluster 3 `_ =` sites have been updated with log + audit + continue
- WHEN `go test ./...` is run from the repo root
- THEN all previously-passing tests continue to pass
- AND the new unit tests covering log + audit behavior pass

### Requirement: Out-of-scope sites are not touched

The following sites MUST NOT be modified by this change:

- `phase/service.go:1291` (`fallbackToMemory` bug — separate change)
- Any Prometheus metrics instrumentation in `obs/metrics.go`
- Any skill-risk instrumentation for `rollback_count` or `deprecated_api_hits`
- Any public-facing HTTP API handler or domain port interface signature

#### Scenario: Out-of-scope file is unchanged

- GIVEN the set of files changed in this PR
- WHEN the diff is inspected
- THEN `obs/metrics.go` is NOT modified
- AND no public API interface signatures are modified
- AND `phase/service.go:1291` is NOT modified by this change

## Implementation note (as shipped)

- **`appendAuditErr` helper** — added as a private method on `*RunService` in
  `internal/application/apply/run.go` (line ~1160). Wraps `outbound.AuditLog.Append` with the
  `"apply.error.discarded"` event type. Reuses the inline pattern at `run.go:669`.
- **`phase.Service` sites** — use the pre-existing `s.appendAudit(...)` helper at
  `phase/service.go:1400`. No new cross-package utility invented.
- **Log idiom** — apply package uses `slog.Default().WarnContext(ctx, ...)` matching
  `teamlead.go:635`; phase package uses `slog.Error(...)` (no context) matching the
  `advanceChange` pattern at lines 1338–1354.
- **Known asymmetry** — the same "soft" domain-transition errors are WARN in the apply package
  and ERROR in the phase package. This is intentional: each package matches its pre-existing
  logging idiom. Document if this causes production log-filtering confusion.
- **Accepted follow-up** — PR1 WARNING-2: `TestBuildFeedback_SaveGroupErr` uses
  `saveGroupErrAfterN=3` and asserts `op == "BoardRepo.SaveGroup"`, which would pass even if
  `build_feedback.go:154` were not instrumented (teamlead.go:186 would still fire). The
  implementation IS correct; the test has weak isolation. Remediation: add a distinct op marker
  at build_feedback sites or use a call-counter assertion. Low urgency.
