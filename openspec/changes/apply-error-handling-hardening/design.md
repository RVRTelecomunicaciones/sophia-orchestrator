# apply-error-handling-hardening — Technical Design

Make 53 silently-discarded errors observable across the apply pipeline and the pg adapter layer, without changing control flow or public API signatures.

---

## Decision summary

| Question | Decision |
|---|---|
| Audit mechanism | `s.d.Audit.Append(ctx, outbound.AuditEvent{...})` — already wired in both `apply.RunService` and `phase.Service`; reuse inline per existing apply pattern (`run.go:669`) |
| Logging idiom | `slog.Default().ErrorContext(ctx, "op: reason", "change_id", ..., "error", err)` — matches `teamlead.go:635` which is the only pre-existing context-aware log line in the apply package |
| Helper extraction | Add one private helper `appendAuditErr` on `*RunService` in `apply/run.go` (Cluster 2+3); do NOT add a cross-package utility to avoid the `hashPrompt`-duplication smell; phase.Service already has `appendAudit` — no new helper there |
| Cluster 4 per-site mode | Scan-loop sites → skip row and log; single-hydrator sites (non-loop) → return error to caller |
| PR split | **Split recommended**: PR 1 = Clusters 2+3 (application layer); PR 2 = Cluster 4 (adapter layer) |
| Estimated changed lines | **PR 1: ~230 lines; PR 2: ~95 lines; total ~325 lines** |

---

## Open question resolutions (design-phase findings)

### A. Audit-append API

**Finding**: The `outbound.AuditLog` interface (`internal/ports/outbound/audit.go:13`) has a single `Append(ctx, AuditEvent) error` method. Both consumers already have direct access:

- `apply.RunService` — `d.Audit outbound.AuditLog` at `internal/application/apply/run.go:60`; used inline at `run.go:669` with pattern:
  ```go
  _ = s.d.Audit.Append(ctx, outbound.AuditEvent{
      ChangeID:   &cidLocal,
      PhaseID:    &pidLocal,
      EventType:  "apply.finalized",
      Payload:    payload,
      OccurredAt: s.d.Clock.Now(),
  })
  ```
- `phase.Service` — `d.Audit outbound.AuditLog`; accessed via the private `appendAudit` helper at `phase/service.go:1400`. That helper marshals the payload to JSON internally, then calls `s.d.Audit.Append`. All Cluster 3 sites in `phase/service.go` will use the existing `s.appendAudit(...)` wrapper — no new code needed beyond the call itself.

**Decision for Clusters 2+3**: Add one private helper method `appendAuditErr` on `*RunService` in `internal/application/apply/run.go`. This keeps the inline Audit.Append pattern DRY across the ~20 apply-package sites without creating a cross-package utility. The helper signature:

```go
// appendAuditErr records a non-fatal error event in the audit trail.
// Matches the existing inline pattern at run.go:669. Fail-soft: discard the
// Append error (same as all other appendAudit call sites in the codebase).
func (s *RunService) appendAuditErr(ctx context.Context, cid ids.ChangeID, pid ids.PhaseID, op string, err error) {
    payload, _ := json.Marshal(map[string]any{"operation": op, "error": err.Error()})
    _ = s.d.Audit.Append(ctx, outbound.AuditEvent{
        ChangeID:   &cid,
        PhaseID:    &pid,
        EventType:  "apply.error.discarded",
        Payload:    payload,
        OccurredAt: s.d.Clock.Now(),
    })
}
```

For Cluster 3 sites in `phase/service.go`: the existing `s.appendAudit(ctx, &cidLocal, &pidLocal, nil, eventType, payload)` helper at line 1400 is used directly. No new helper needed — call `s.appendAudit(ctx, cid, pid, nil, "phase.apply.error.discarded", map[string]any{"op": op, "error": err.Error()})`.

**No per-site audit API invented.** Both helpers reuse `outbound.AuditLog.Append` via paths already wired and already called in production.

### B. Precise line-count estimate

Methodology: each `_ =` site gets a log call (1–2 lines), an audit call (2–4 lines), and in some cases a surrounding context variable declaration. Blank lines between calls add ~1 per site. Helper definition adds ~10 lines once.

#### Cluster 2 — domain-transition errors (teamlead.go + phase/service.go)

Sites confirmed at:

| File | Lines | Count |
|---|---|---|
| `internal/application/apply/teamlead.go` | 152, 159, 161, 188, 318, 407, 460, 462, 515, 590, 592 | 11 |
| `internal/application/phase/service.go` | 585, 600, 612, 619 | 4 |

Total sites: 15. Per site: 3 lines (log + audit + blank) = **~45 lines**, plus helper definition ~10 lines = **~55 lines**.

Note: `teamlead.go:407` is `_ = sess.MarkRunning()` in `dispatchImplementWithOverride`; `teamlead.go:515` is `_ = sess.MarkRunning()` in `dispatchImplement`. These are domain-transition calls where an error means the session is already running (benign), so the log level is WARN not ERROR.

#### Cluster 3 — repo Save errors (teamlead.go + build_feedback.go + phase/service.go)

Sites confirmed at:

| File | Lines | Count |
|---|---|---|
| `internal/application/apply/teamlead.go` | 164, 168, 189, 271, 295, 319, 339, 461, 463, 591, 593 | 11 |
| `internal/application/apply/build_feedback.go` | 153, 179, 192 | 3 |
| `internal/application/phase/service.go` | 583, 586, 601, 613, 620, 1079, 1308 | 7 |

Total sites: 21. Per site: 3–4 lines = **~75 lines**.

Note: `teamlead.go:271` is `_ = s.d.SpawnGov.Release(ctx)` (SpawnGov.Release is a repo-adjacent call); `teamlead.go:318` is `_ = task.Release()` (domain call, Cluster 2); `build_feedback.go:153,179,192` are SaveGroup calls inside the build gate.

**PR 1 total (Clusters 2+3): ~55 + ~75 + test files = ~230 changed lines.**

#### Cluster 4 — ULID parse errors (pg repos)

Sites confirmed at:

| File | Lines | Count | Mode |
|---|---|---|---|
| `internal/adapters/outbound/pg/board_repo.go` | 106, 107 | 2 | single-hydrator (`FindBoardByPhaseID`) — return error |
| `internal/adapters/outbound/pg/board_repo.go` | 139, 140, 143 | 3 | scan-loop (`findGroupsByBoard`) — skip row |
| `internal/adapters/outbound/pg/board_repo.go` | 206, 207, 211 | 3 | scan-loop (`findTasksByGroup`) — skip row |
| `internal/adapters/outbound/pg/board_repo.go` | 252, 253, 257 | 3 | single-hydrator (`FindTaskByID`) — return error |
| `internal/adapters/outbound/pg/session_repo.go` | 113, 114, 115, 118 | 4 | single-hydrator (`scanSession`) — return error |
| `internal/adapters/outbound/pg/worktree_repo.go` | 85, 88 | 2 | single-hydrator (`scanWorktree`) — return error |

Total sites: 17. Per site: log call (2 lines) + conditional return/continue (1–2 lines) = **~55 lines** plus import additions (3 files × 1 line slog import) = **~58 lines + test files ≈ 95 lines**.

**PR 2 total (Cluster 4): ~95 changed lines.**

**Grand total: ~325 changed lines across both PRs. Well within 400-line threshold per PR.**

---

## Per-cluster treatment

### Cluster 2 — domain-transition errors: log WARN + audit, continue

Policy: domain state-machine errors in apply context are soft — `MarkRunning` being already running is harmless; `group.Fail()`/`group.Complete()` errors mean the state machine already transitioned. Log at WARN, append audit, do not change control flow.

**Pattern applied at each site in `apply` package:**

```go
// Before:
_ = group.Fail()

// After:
if err := group.Fail(); err != nil {
    slog.Default().WarnContext(ctx, "apply: group state transition discarded",
        "operation", "group.Fail", "group_id", group.ID().String(), "error", err)
    s.appendAuditErr(ctx, c.ID(), p.ID(), "group.Fail", err)
}
```

**Pattern applied at each site in `phase` package (`service.go:585,600,612,619`):**

```go
// Before:
_ = sess.RecordOutcome(nil, -1, s.d.Clock.Now())

// After:
if err := sess.RecordOutcome(nil, -1, s.d.Clock.Now()); err != nil {
    slog.Error("runPhase: session.RecordOutcome discarded",
        slog.String("phase_id", p.ID().String()),
        slog.String("error", err.Error()),
    )
    s.appendAudit(ctx, &cidLocal, &pidLocal, nil, "phase.apply.error.discarded",
        map[string]any{"op": "session.RecordOutcome", "error": err.Error()})
}
```

Note: `phase/service.go` uses `slog.Error(...)` (no context) matching the existing `advanceChange` pattern at lines 1338–1354. The `apply` package uses `slog.Default().WarnContext(ctx, ...)` matching `teamlead.go:635`.

### Cluster 3 — repo Save errors: log ERROR + audit, continue

Policy: a failed save is a data loss event. Log at ERROR, append audit, do not abort (same low-risk philosophy as proposal).

**Pattern in `apply` package:**

```go
// Before:
_ = s.d.BoardRepo.SaveGroup(ctx, group)

// After:
if err := s.d.BoardRepo.SaveGroup(ctx, group); err != nil {
    slog.Default().ErrorContext(ctx, "apply: BoardRepo.SaveGroup failed; continuing",
        "group_id", group.ID().String(), "error", err)
    s.appendAuditErr(ctx, c.ID(), p.ID(), "BoardRepo.SaveGroup", err)
}
```

**Pattern in `phase/service.go`:**

Uses `slog.Error(...)` matching the `advanceChange` idiom + `s.appendAudit(...)` wrapper.

### Cluster 4 — ULID parse errors: per-site scan-loop vs single-hydrator

#### Scan-loop sites (skip row + log)

Sites: `board_repo.go` lines 139/140/143 (`findGroupsByBoard`) and 206/207/211 (`findTasksByGroup`).

```go
// Before:
groupID, _ := ids.ParseGroupID(gid)
bidParsed, _ := ids.ParseBoardID(bid)

// After:
groupID, err := ids.ParseGroupID(gid)
if err != nil {
    slog.Default().ErrorContext(ctx, "pg.BoardRepo: corrupt group_id; skipping row",
        "raw_id", gid, "error", err)
    continue
}
bidParsed, err := ids.ParseBoardID(bid)
if err != nil {
    slog.Default().ErrorContext(ctx, "pg.BoardRepo: corrupt board_id; skipping row",
        "raw_id", bid, "error", err)
    continue
}
```

For the dependency-ID loop at line 143 (`id, _ := ids.ParseGroupID(d)` inside a `range deps`):

```go
// Before:
id, _ := ids.ParseGroupID(d)
depIDs = append(depIDs, id)

// After:
id, err := ids.ParseGroupID(d)
if err != nil {
    slog.Default().ErrorContext(ctx, "pg.BoardRepo: corrupt dep group_id; skipping dep",
        "raw_id", d, "error", err)
    continue
}
depIDs = append(depIDs, id)
```

#### Single-hydrator sites (return error)

Sites: `board_repo.go` lines 106/107 (`FindBoardByPhaseID`), 252/253/257 (`FindTaskByID`); `session_repo.go` lines 113/114/115/118 (`scanSession`); `worktree_repo.go` lines 85/88 (`scanWorktree`).

```go
// Before (board_repo.go:106–107):
boardID, _ := ids.ParseBoardID(bid)
pidParsed, _ := ids.ParsePhaseID(pid)

// After:
boardID, err := ids.ParseBoardID(bid)
if err != nil {
    slog.Default().ErrorContext(ctx, "pg.BoardRepo: corrupt board_id in FindBoardByPhaseID",
        "raw_id", bid, "error", err)
    return nil, wrapErr("BoardRepo.FindBoardByPhaseID.parseBoard", err)
}
pidParsed, err := ids.ParsePhaseID(pid)
if err != nil {
    slog.Default().ErrorContext(ctx, "pg.BoardRepo: corrupt phase_id in FindBoardByPhaseID",
        "raw_id", pid, "error", err)
    return nil, wrapErr("BoardRepo.FindBoardByPhaseID.parsePhase", err)
}
```

Same pattern in `FindTaskByID`, `scanSession`, `scanWorktree` — log + return `wrapErr(...)`.

The optional pointer path (`claimedBy != nil` / `sessionIDStr != nil`) at lines 211 (`board_repo.go`) and 118 (`session_repo.go`) and 88 (`worktree_repo.go`) follow the same pattern — log + return error because these are single-hydrator paths where a zero-value pointer-field ID would corrupt the aggregate.

**Slog import**: `board_repo.go`, `session_repo.go`, and `worktree_repo.go` do not currently import `"log/slog"`. Each file gains one import line.

---

## Helper placement

**`appendAuditErr` method on `*RunService`** — placed in `internal/application/apply/run.go` alongside the existing `fmtErr`, `hashPrompt`, and `roleForApply` package-private helpers at the bottom of that file (~line 1147+). This avoids a cross-package utility and keeps the apply package self-contained.

**Do not** add to a new shared file. The `hashPrompt` duplication between `apply/run.go:1144` and `phase/service.go:1529` is a pre-existing smell; this change does not add a third cross-package duplicate.

**`phase.Service`** already has `appendAudit` at `phase/service.go:1400`. No new helper needed there.

---

## Log field schema

All new log calls emit these fields to ensure greppability and structured query support:

| Context | Key | Value |
|---|---|---|
| apply package (context-aware) | `"operation"` | method name string |
| apply package | `"group_id"` or `"task_id"` | ULID string |
| apply package | `"error"` | `err.Error()` |
| phase package (no context) | `slog.String("phase_id", ...)` | ULID string |
| phase package | `slog.String("error", err.Error())` | error string |
| pg package (context-aware) | `"raw_id"` | raw DB string value |
| pg package | `"error"` | `err.Error()` |

---

## Delivery decision

**Recommendation: split into two PRs.**

The total estimated line count (~325 lines) is within the 400-line threshold in aggregate, but the risk profiles are asymmetric. Cluster 4 introduces skip/return logic — the only control-flow change in this whole hardening — and touches 3 different files in a separate architectural layer (pg adapter). Isolating it:

- Keeps the application-layer PR reviewable and low-risk (log + audit, no control flow).
- Makes the adapter-layer PR's correctness decisions (skip vs return) independently verifiable.
- Aligns with the hexagonal boundary already identified in the proposal as the natural seam.

| PR | Scope | Files | Estimated lines | Risk |
|---|---|---|---|---|
| PR 1 | Clusters 2 + 3 | `apply/teamlead.go`, `apply/build_feedback.go`, `apply/run.go` (+helper), `phase/service.go` | ~230 | Low — no control-flow change |
| PR 2 | Cluster 4 | `pg/board_repo.go`, `pg/session_repo.go`, `pg/worktree_repo.go` | ~95 | Medium — introduces skip/return logic |

PR 2 depends on PR 1 only for conventional-commit ordering; there is no code dependency between them.

---

## Test strategy — Strict TDD

Test runner: `go test ./...` from repo root. Every site lands RED → GREEN.

### Cluster 2+3 — apply package

**Fake infrastructure already exists**: `internal/application/apply/run_test.go:281` has `fakeAudit` that captures `[]outbound.AuditEvent`. Use it.

**Forcing error paths RED:**

| Site type | RED mechanism |
|---|---|
| `group.Fail()`, `group.Complete()`, `sess.RecordOutcome()`, `task.Complete()` | Fake domain object with a `forceErr` field that returns the configured error from the method |
| `sess.MarkRunning()` | Same fake session with state pre-set to `Running` (already running → error) |
| `BoardRepo.SaveGroup`, `BoardRepo.SaveTask`, `SessionRepo.Save`, `SpawnGov.Release` | Existing fake repos extended with `saveGroupErr`, `saveTaskErr`, `saveErr`, `releaseErr` fields |

**Test assertions:**

```go
// Example for Cluster 3 BoardRepo.SaveGroup site:
func TestRunTeamLead_SaveGroupErr_LogsAndAudits(t *testing.T) {
    audit := &fakeAudit{}
    repo := &fakeBoardRepo{saveGroupErr: errors.New("db timeout")}
    s := newTestRunService(t, RunDeps{BoardRepo: repo, Audit: audit, ...})
    // drive runTeamLead through completion path
    ...
    // Assert: audit has "apply.error.discarded" event
    require.Len(t, audit.events, 1)
    assert.Equal(t, "apply.error.discarded", audit.events[0].EventType)
    // Assert: log output contains "BoardRepo.SaveGroup" (use slog test handler)
}
```

For `phase.Service` Cluster 2+3 sites: add cases to the existing `service_test.go` / `service_outbox_test.go` with fake session repos and fake audit capturing the `phase.apply.error.discarded` event type.

### Cluster 4 — pg adapter

**Requires testcontainers** (integration tests), or a unit test with a fake `scannable` that returns malformed ULID strings.

The `scannable` interface (used by `scanSession`, `scanWorktree`) is already defined in the pg package. Write a unit-level fake:

```go
type fakeScannable struct{ vals []any }
func (f *fakeScannable) Scan(dest ...any) error { /* copy f.vals into dest */ }
```

**Malformed ULID fixture**: use `"not-a-valid-ulid"` as the raw column value. `ids.ParseBoardID("not-a-valid-ulid")` returns a non-nil error.

**Test cases per site type:**

| Site | Input | Expected behaviour |
|---|---|---|
| `scanSession` — `idStr` malformed | `"bad-id"` for session ID | returns `outbound.ErrNotFound`-wrapped error (via `wrapErr`) |
| `findGroupsByBoard` — `gid` malformed | `"bad-id"` for group ID | row skipped, result slice has zero elements; slog captures ERROR |
| `FindBoardByPhaseID` — `bid` malformed | `"bad-id"` for board ID | returns error, no nil-field board returned |

Use `slog/slogtest` (stdlib, Go 1.21+) or a custom `slog.Handler` to assert log records in unit tests. This codebase is on Go 1.26.2 so `slogtest` is available.

---

## Constraints and conventions

- All new log calls use `"log/slog"` — already imported in `apply/teamlead.go`, `apply/run.go`, and `phase/service.go`. Must be added to `pg/board_repo.go`, `pg/session_repo.go`, `pg/worktree_repo.go`.
- No public API signature changes.
- No database schema changes.
- Conventional commits: `fix(apply)` for PR 1, `fix(pg)` for PR 2.
- No `Co-Authored-By` lines.
- `forbidigo` / `wrapcheck` / `errorlint` linters: all new error handling uses `wrapErr(...)` in pg files (existing pattern) and `fmt.Errorf("...: %w", err)` in application files where propagated.
- Strict TDD: test file added or extended before implementation file change for each cluster.

---

## Next step

`sdd-tasks` — break the above into ordered work units per PR, confirm delivery strategy (stacked-to-main), and produce the task list with TDD sequencing.
