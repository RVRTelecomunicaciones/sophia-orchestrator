# Delta: app-layer-transition-errors

## Capability

Makes domain state-machine transition errors observable in the application layer. Every call to `RecordOutcome`, `group.Fail`, `group.Complete`, `task.Complete`, `MarkRunning`, and `task.Release` that currently discards its error via `_ =` MUST instead emit a structured error log AND record an audit signal. Control flow is not changed.

## ADDED Requirements

### Requirement: Structured error log on transition failure

When any domain-transition call (`RecordOutcome`, `group.Fail`, `group.Complete`, `task.Complete`, `MarkRunning`, `task.Release`) returns a non-nil error, the application layer MUST emit exactly one structured error-level log entry containing all of the following fields:

| Field | Contract |
|---|---|
| `site` / `operation` | The specific call site or operation name (e.g. `"group.Fail"`, `"task.Complete"`) |
| Identity field | At minimum one of: `change_id`, `group_id`, or `task_id` — whichever is in scope at that call site |
| `error` | The error value returned by the call |

The log entry MUST be at ERROR severity. The log entry MUST NOT be omitted even if the caller continues normally.

#### Scenario: group.Fail returns error — log is emitted

- GIVEN an application layer handler where `group.Fail` is invoked on a domain group
- AND `group.Fail` returns a non-nil error (e.g., invalid state transition)
- WHEN the handler processes the event
- THEN exactly one ERROR-level log entry is emitted
- AND the log entry contains the operation name (`"group.Fail"` or equivalent site identifier)
- AND the log entry contains a `change_id` or `group_id` identifying the affected entity
- AND the log entry contains the error value
- AND the handler continues to its next step without returning or aborting the phase

#### Scenario: task.Complete returns error — log is emitted

- GIVEN an application layer handler where `task.Complete` is invoked on a domain task
- AND `task.Complete` returns a non-nil error
- WHEN the handler processes the event
- THEN exactly one ERROR-level log entry is emitted containing the operation name, a task or group identity, and the error
- AND the handler does NOT abort or return early

#### Scenario: RecordOutcome returns error — log is emitted

- GIVEN a call to `RecordOutcome` in the application layer
- AND the call returns a non-nil error
- WHEN the site is reached during apply processing
- THEN exactly one ERROR-level log entry is emitted with the operation name, an identity field in scope, and the error
- AND execution continues to the next line after the call

### Requirement: Audit signal on transition failure

When any domain-transition call returns a non-nil error, the application layer MUST record an audit signal via the existing audit mechanism. The audit signal MUST be recorded at the same site as the log entry, using information available at that call site (at minimum: operation name and the entity identity in scope).

The audit record MUST be appended AFTER the log entry. Its persistence failure MUST NOT abort the phase (consistent with the log-and-continue policy).

#### Scenario: Transition error triggers audit entry

- GIVEN `group.Complete` returns a non-nil error during apply processing
- WHEN the error is handled
- THEN an audit entry is appended via the existing audit mechanism
- AND the audit entry is distinguishable from a success-path audit entry (e.g., carries error context or an error-class marker)
- AND the handler continues after the audit call regardless of the audit call's own return value

### Requirement: No phase abort on transition failure

A non-nil error from any domain-transition call listed in this spec MUST NOT cause an early return, panic, or phase-level abort in the application layer. The log-and-audit-and-continue policy applies unconditionally to all Cluster 2 sites.

#### Scenario: Multiple transition failures in one apply run — all are logged, none abort

- GIVEN an apply run where `group.Fail` AND `task.Release` both return non-nil errors at different call sites
- WHEN apply processing reaches each site in sequence
- THEN each site emits its own ERROR-level log entry
- AND each site appends its own audit entry
- AND the apply phase does not abort between the two sites
- AND the apply phase completes to its normal terminal state

## UNCHANGED Requirements

### Requirement: Success path is unaffected

When a domain-transition call returns nil, the behavior MUST be byte-identical to pre-change behavior: no log entry is emitted, no audit entry is appended, and control flow is unmodified.

#### Scenario: Successful transition — no spurious log or audit

- GIVEN a domain-transition call returns nil
- WHEN the application layer processes the event
- THEN zero ERROR-level log entries are emitted for that call
- AND no additional audit entry is appended for that call
