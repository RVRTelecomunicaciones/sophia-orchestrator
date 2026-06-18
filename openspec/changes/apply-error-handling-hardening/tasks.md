# apply-error-handling-hardening — Task Checklist

## Review Workload Forecast

| Metric | Value |
|---|---|
| Estimated changed lines — PR 1 (Clusters 2+3) | ~230 |
| Estimated changed lines — PR 2 (Cluster 4) | ~95 |
| Total estimated changed lines | ~325 |
| Chained PRs recommended | Yes |
| 400-line budget risk | Low (each PR is well under 400) |
| Decision needed before apply | Yes — confirm stacked-to-main delivery before starting |

PR 2 has no code dependency on PR 1. Split is driven by risk profile, not code coupling.
Delivery strategy: `ask-on-risk`. Orchestrator must confirm before launching `sdd-apply`.

---

## Work-Unit Conventions

- Each work unit = RED (failing test) → GREEN (implementation) → VERIFY (`go test ./...`)
- Tests ship in the same commit as the behavior they verify
- Every commit uses a Conventional Commit message; no `Co-Authored-By`
- Rollback of one unit must not break unrelated units

---

## PR 1 — Application Layer (Clusters 2 + 3)

**Scope**: `internal/application/apply/`, `internal/application/phase/`
**Files**: `apply/run.go`, `apply/teamlead.go`, `apply/build_feedback.go`, `phase/service.go`
**Conventional commit prefix**: `fix(apply)`
**Estimated lines**: ~230

### WU-1: `appendAuditErr` helper on `*RunService`

**Target file**: `internal/application/apply/run.go`
**Spec**: app-layer-transition-errors (audit signal requirement), app-layer-repo-errors (audit signal requirement)
**Dependency**: none — this is the foundation for all PR 1 work units

**Pre-flight check (do before any code)**:
```bash
# Confirm no name collision exists
rg "appendAuditErr" /Users/russell/Documents/2026/sophia-orchestator/internal/application/apply/
```
Expected: no matches. If a match exists, resolve name conflict before proceeding.

**RED — write failing test first**

File: `internal/application/apply/run_test.go` (extend existing file)

Add `TestRunService_appendAuditErr_emitsEvent`:
- Wire a `*RunService` with `fakeAudit` (already defined at run_test.go:276)
- Call the (not-yet-existent) `appendAuditErr` method directly via a thin exported test shim or via reflection. Preferred: make `appendAuditErr` exported temporarily as `AppendAuditErr` only during this RED step, then lowercase before GREEN merge. Alternative: write the test to drive an existing handler that will eventually call it (deferred RED — acceptable if helper is unexported).
- Assert: `fakeAudit.events` contains one entry with `EventType == "apply.error.discarded"` and `Payload` containing `"operation"` key

Run `go test ./internal/application/apply/... -run TestRunService_appendAuditErr` → must fail (method does not exist).

**GREEN — implement**

Add at bottom of `internal/application/apply/run.go` (after `roleForApply`, ~line 1155):

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

Confirm `encoding/json` is already imported in `run.go`. If not, add it.

**VERIFY**:
```bash
go test ./internal/application/apply/... -run TestRunService_appendAuditErr
go test ./...
```

**Commit**: `test(apply): add appendAuditErr helper and unit test`

---

### WU-2: Cluster 2 — domain-transition sites in `apply/teamlead.go`

**Target file**: `internal/application/apply/teamlead.go`
**Spec**: app-layer-transition-errors (all scenarios)
**Sites**: lines 152, 159, 161, 188, 318, 407, 460, 462, 515, 590, 592 (11 sites)
**Depends on**: WU-1 (appendAuditErr must exist)

**RED — extend `run_test.go` or add `teamlead_test.go`**

Write `TestRunTeamLead_GroupFail_LogsWarnAndAudits`:
- Use existing `fakeAudit` captured from `newRunService` or a dedicated setup
- Inject a fake domain group whose `Fail()` method returns `errors.New("already failed")`
- Drive the code path that calls `group.Fail()` (teamlead flow to completion)
- Assert: `fakeAudit` captures one event with `EventType == "apply.error.discarded"` and payload containing `"group.Fail"`
- Assert: test slog handler captures one WARN-level entry containing `"group.Fail"` and the error text

Write `TestRunTeamLead_MarkRunning_LogsWarnAndAudits` (for lines 407, 515):
- Inject a fake session already in Running state so `MarkRunning()` returns an error
- Assert WARN log + audit event

Write `TestRunTeamLead_TransitionErrors_NoPhasAbort`:
- Inject fakes where both `group.Fail()` and `task.Release()` return errors
- Assert apply phase completes to terminal state (no early return)
- Assert two audit events captured (one per site)

Run `go test ./internal/application/apply/... -run TestRunTeamLead_GroupFail` → must fail (sites still use `_ =`).

**GREEN — implement**

Apply the Cluster 2 pattern to all 11 sites in `teamlead.go`:

```go
// Before:
_ = group.Fail()

// After (for state-machine calls):
if err := group.Fail(); err != nil {
    slog.Default().WarnContext(ctx, "apply: group state transition discarded",
        "operation", "group.Fail", "group_id", group.ID().String(), "error", err)
    s.appendAuditErr(ctx, c.ID(), p.ID(), "group.Fail", err)
}
```

Note: `teamlead.go:407` and `:515` (`MarkRunning`) and `:318` (`task.Release`) are WARN-level (benign state errors).
Note: `teamlead.go:152,159,161,188,460,462,590,592` are the group/task complete/fail sites — also WARN-level per Cluster 2 policy.

Identity fields to use at each site: use whichever of `c.ID()`, `p.ID()`, `group.ID()`, `task.ID()` is in scope. At minimum one identity field is required per spec.

`slog` is already imported in `teamlead.go` — no import change needed.

**VERIFY**:
```bash
go test ./internal/application/apply/... -run TestRunTeamLead
go test ./...
```

**Commit**: `fix(apply): log+audit domain-transition errors in teamlead (Cluster 2)`

---

### WU-3: Cluster 3 — repo Save sites in `apply/teamlead.go`

**Target file**: `internal/application/apply/teamlead.go`
**Spec**: app-layer-repo-errors (all scenarios)
**Sites**: lines 164, 168, 189, 271, 295, 319, 339, 461, 463, 591, 593 (11 sites)
**Depends on**: WU-1 (appendAuditErr must exist)
**Note**: WU-2 and WU-3 touch the same file. Implement sequentially within the same commit batch to avoid conflicts.

**RED — extend tests from WU-2 or add new cases**

Write `TestRunTeamLead_SaveGroupErr_LogsErrorAndAudits`:
- Inject `fakeBoardRepo` (already defined at run_test.go:31) configured to return an error from `SaveGroup`
- Drive the path where `s.d.BoardRepo.SaveGroup` is called
- Assert: `fakeAudit` captures one event with `EventType == "apply.error.discarded"` and payload containing `"BoardRepo.SaveGroup"`
- Assert: slog handler captures one ERROR-level entry containing the operation name and error

Write `TestRunTeamLead_SaveErrors_NoPhaseAbort`:
- Inject fakes where `SaveGroup` AND `SaveTask` both return errors
- Assert apply phase completes normally (spec: success-path-regression unchanged invariant)
- Assert two audit events captured

Write `TestRunTeamLead_SpawnGovRelease_LogsErrorAndAudits` (line 271 — `SpawnGov.Release`):
- Inject fake SpawnGov whose `Release` returns an error
- Assert ERROR log + audit

Run tests → must fail (sites still use `_ =`).

**GREEN — implement**

Apply the Cluster 3 pattern to all 11 sites in `teamlead.go`:

```go
// Before:
_ = s.d.BoardRepo.SaveGroup(ctx, group)

// After:
if err := s.d.BoardRepo.SaveGroup(ctx, group); err != nil {
    slog.Default().ErrorContext(ctx, "apply: BoardRepo.SaveGroup failed; continuing",
        "operation", "BoardRepo.SaveGroup", "group_id", group.ID().String(), "error", err)
    s.appendAuditErr(ctx, c.ID(), p.ID(), "BoardRepo.SaveGroup", err)
}
```

Use appropriate identity fields at each site (`group_id`, `task_id`, or available IDs in scope).

**VERIFY**:
```bash
go test ./internal/application/apply/... -run TestRunTeamLead
go test ./...
```

**Commit**: `fix(apply): log+audit repo Save errors in teamlead (Cluster 3)`

---

### WU-4: Cluster 3 — repo Save sites in `apply/build_feedback.go`

**Target file**: `internal/application/apply/build_feedback.go`
**Spec**: app-layer-repo-errors (build_feedback.go sites requirement)
**Sites**: lines 153, 179, 192 (3 sites — all `SaveGroup` inside build gate)
**Depends on**: WU-1

**RED**

Extend `build_feedback_test.go` (already exists):

Write `TestBuildFeedback_SaveGroupErr_LogsAndAudits`:
- Use the existing test harness in `build_feedback_test.go` (note: `fakeAudit` already used at lines 148, 189, etc.)
- Configure `fakeBoardRepo.SaveGroup` to return an error
- Drive the build feedback path that calls `SaveGroup`
- Assert ERROR log + audit event with `EventType == "apply.error.discarded"`

Run `go test ./internal/application/apply/... -run TestBuildFeedback_SaveGroupErr` → must fail.

**GREEN — implement**

Apply Cluster 3 pattern to 3 sites in `build_feedback.go`. Use `slog.Default().ErrorContext(ctx, ...)` + `s.appendAuditErr(...)` matching teamlead pattern.

Confirm `slog` is already imported in `build_feedback.go`. If not, add `"log/slog"` to imports.

**VERIFY**:
```bash
go test ./internal/application/apply/... -run TestBuildFeedback
go test ./...
```

**Commit**: `fix(apply): log+audit repo Save errors in build_feedback (Cluster 3)`

---

### WU-5: Clusters 2+3 — sites in `phase/service.go`

**Target file**: `internal/application/phase/service.go`
**Spec**: app-layer-transition-errors (RecordOutcome scenarios), app-layer-repo-errors (SessionRepo.Save scenarios)
**Sites**:
- Cluster 2 (transition): lines 585, 600, 612, 619 (`sess.RecordOutcome(...)`)
- Cluster 3 (repo): lines 583, 586, 601, 613, 620, 1079, 1308 (`SpawnGov.Release`, `SessionRepo.Save`)
**Depends on**: none (phase package uses existing `appendAudit` — no new helper needed)

**CRITICAL SCOPE CHECK (gotcha #3 from design)**:

Lines 583–586 are inside `runAsync`, where `cidLocal` / `pidLocal` are NOT declared. The function has `c *change.Change` and `p *phase.Phase` in scope. Before calling `s.appendAudit(ctx, &cidLocal, &pidLocal, ...)`, the task must declare local variables:

```go
// Insert BEFORE the first appendAudit call in this block:
cidLocal := c.ID()
pidLocal := p.ID()
```

Do NOT use `&c.ID()` (non-addressable). The local variable pattern is established by lines 680–681, 765–766, 848–849 in the same file.

Check which blocks have `cidLocal`/`pidLocal` already declared and which do not (lines 583–620 block does NOT have them declared).

**RED**

File: `internal/application/phase/service_test.go` or `service_outbox_test.go`

Write `TestPhaseService_SpawnGovRelease_LogsAndAudits`:
- Inject fake SpawnGov whose `Release` returns an error
- Inject `fakeAudit` capturing events
- Run a phase that reaches the dispatch path (post-acquire)
- Assert `fakeAudit` has event with `EventType == "phase.apply.error.discarded"` or equivalent marker
- Assert slog captures an ERROR entry containing `"SpawnGov.Release"`

Write `TestPhaseService_SessionRepoSave_LogsAndAudits` for SessionRepo.Save sites.

Write `TestPhaseService_RecordOutcome_LogsAndAudits` for RecordOutcome sites (lines 585, 600, 612, 619).

Run tests → must fail.

**GREEN — implement**

The phase package does NOT add `appendAuditErr`. Instead it uses the existing `s.appendAudit(...)` at line 1400 and `slog.Error(...)` (no context, matching `advanceChange` pattern at lines 1338–1354).

Pattern for Cluster 2 in `phase/service.go`:

```go
// Before (line 585 inside dispatchErr block):
_ = sess.RecordOutcome(nil, -1, s.d.Clock.Now())

// After:
if err := sess.RecordOutcome(nil, -1, s.d.Clock.Now()); err != nil {
    slog.Error("runPhase: session.RecordOutcome discarded",
        slog.String("phase_id", pidLocal.String()),
        slog.String("error", err.Error()),
    )
    s.appendAudit(ctx, &cidLocal, &pidLocal, nil, "phase.apply.error.discarded",
        map[string]any{"op": "session.RecordOutcome", "error": err.Error()})
}
```

For lines 583, 586 (SpawnGov.Release, SessionRepo.Save in same `dispatchErr` block): declare `cidLocal := c.ID()` and `pidLocal := p.ID()` at the start of that block before the first new `appendAudit` call.

For lines 601, 613, 620 (in schema_mismatch and success blocks): check if `cidLocal`/`pidLocal` are already in scope in those blocks. If not, declare them locally.

For lines 1079, 1308: check enclosing function and declare locals as needed.

**VERIFY**:
```bash
go test ./internal/application/phase/... -run TestPhaseService
go test ./...
```

**Commit**: `fix(apply): log+audit discarded errors in phase/service (Clusters 2+3)`

---

### WU-6: Success-path regression test for PR 1

**Target files**: `apply/run_test.go` or dedicated `regression_test.go`
**Spec**: success-path-regression (all scenarios for PR 1)
**Depends on**: WU-2, WU-3, WU-4, WU-5

**RED** (these tests should already pass after GREEN of each prior WU — run first to confirm no regression):

Write `TestApplyLayer_SuccessPath_NoSpuriousLogOrAudit`:
- Configure all fakes to return `nil` from every domain-transition and repo-save call
- Run apply through normal completion
- Assert: `fakeAudit.events` contains ZERO entries with `EventType == "apply.error.discarded"`
- Assert: slog handler captures zero ERROR/WARN entries from Cluster 2/3 sites

Write `TestPhaseService_SuccessPath_NoSpuriousLogOrAudit`:
- Same but for phase package paths

**GREEN**: If these tests fail, track down which site is emitting spuriously and fix.

**VERIFY**:
```bash
go test ./internal/application/...
go test ./...
```

**Commit**: `test(apply): success-path regression guard for Clusters 2+3`

---

## PR 2 — Adapter Layer (Cluster 4)

**Scope**: `internal/adapters/outbound/pg/`
**Files**: `pg/board_repo.go`, `pg/session_repo.go`, `pg/worktree_repo.go`
**Conventional commit prefix**: `fix(pg)`
**Estimated lines**: ~95
**No code dependency on PR 1** — can be developed in parallel if desired, but deploy after PR 1 per recommended ordering.

---

### WU-7: Add `slog` imports to the three pg files

**Target files**: `pg/board_repo.go`, `pg/session_repo.go`, `pg/worktree_repo.go`
**Spec**: pg-adapter-ulid-parse (error log field contract — requires slog)
**Depends on**: none

**Pre-flight — verify slog is not already imported**:
```bash
rg '"log/slog"' /Users/russell/Documents/2026/sophia-orchestator/internal/adapters/outbound/pg/board_repo.go
rg '"log/slog"' /Users/russell/Documents/2026/sophia-orchestator/internal/adapters/outbound/pg/session_repo.go
rg '"log/slog"' /Users/russell/Documents/2026/sophia-orchestator/internal/adapters/outbound/pg/worktree_repo.go
```

Expected: no matches. If already imported, skip this WU.

**Pre-flight — verify forbidigo lint allows slog**:
```bash
# Check golangci-lint config for forbidigo rules
rg "forbidigo" /Users/russell/Documents/2026/sophia-orchestator/.golangci.yml 2>/dev/null || rg "forbidigo" /Users/russell/Documents/2026/sophia-orchestator/.golangci.yaml 2>/dev/null
```

Confirm `log/slog` is not in a deny-list. If it is, resolve before proceeding (would block all Cluster 4 implementation).

**GREEN** (mechanical — no behavior change, no RED needed):

Add `"log/slog"` to the import block of each of the three files.

**VERIFY**:
```bash
go build ./internal/adapters/outbound/pg/...
go vet ./internal/adapters/outbound/pg/...
```

**Commit**: `chore(pg): add log/slog import to board_repo, session_repo, worktree_repo`

---

### WU-8: Cluster 4 — scan-loop sites in `board_repo.go`

**Target file**: `internal/adapters/outbound/pg/board_repo.go`
**Spec**: pg-adapter-ulid-parse (scan-loop sites requirement — log + skip)
**Sites**:
- `findGroupsByBoard`: lines 139, 140, 143 (group_id, board_id, dep group_ids)
- `findTasksByGroup`: lines 206, 207, 211 (task_id, group_id, claimedBy session_id)
**Depends on**: WU-7 (slog import)

**CRITICAL gotcha (#1 from design)**: the test for `findGroupsByBoard` MUST assert that the returned slice contains NO entry whose group ID is the zero value AND that valid rows are returned intact. A test that only checks the error is insufficient.

**RED — unit test using fake scannable**

File: `internal/adapters/outbound/pg/board_repo_test.go` (create or extend)

```go
// fakeScannable stubs pgx.Rows for unit-level injection
type fakeScannable struct{ vals []any; err error }
func (f *fakeScannable) Scan(dest ...any) error { /* copy f.vals into dest pointers */ }
```

Write `TestFindGroupsByBoard_CorruptGroupID_SkipsRowReturnsRest`:
- Construct a fake row set: row1 (valid ULID), row2 (group_id = `"not-a-valid-ulid"`), row3 (valid ULID)
- Call `findGroupsByBoard` (unexported — may require a thin test-exported wrapper or test within same package using `package pg`)
- Assert: returned slice has length 2 (rows 1 and 3)
- Assert: no entry in returned slice has a zero-value ID
- Assert: slog test handler captures exactly one ERROR entry with `"group_id"` column and `"not-a-valid-ulid"` raw value

Write `TestFindGroupsByBoard_AllRowsCorrupt_ReturnsEmpty`:
- All rows have bad group_id
- Assert: empty slice returned, two ERROR log entries

Write `TestFindTasksByGroup_CorruptTaskID_SkipsRow`:
- Same pattern for `findTasksByGroup`

Run `go test ./internal/adapters/outbound/pg/... -run TestFindGroupsByBoard` → must fail.

**GREEN — implement scan-loop skip pattern**

```go
// Before (findGroupsByBoard, lines 139-140):
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

For line 143 (dep loop inside `range deps`):
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

Apply same skip pattern to `findTasksByGroup` (lines 206, 207, 211).

Note: use `err :=` on first parse in each function scope (may shadow outer `err` — use a distinct variable name if the scan loop already uses `err` for `rows.Scan(...)`).

**VERIFY**:
```bash
go test ./internal/adapters/outbound/pg/... -run TestFindGroupsByBoard
go test ./internal/adapters/outbound/pg/... -run TestFindTasksByGroup
go test ./...
```

**Commit**: `fix(pg): skip corrupt ULID rows in findGroupsByBoard and findTasksByGroup`

---

### WU-9: Cluster 4 — single-hydrator sites in `board_repo.go`

**Target file**: `internal/adapters/outbound/pg/board_repo.go`
**Spec**: pg-adapter-ulid-parse (single-row hydrator requirement — log + return error)
**Sites**:
- `FindBoardByPhaseID`: lines 106, 107
- `FindTaskByID`: lines 252, 253, 257
**Depends on**: WU-7

**RED**

Write `TestFindBoardByPhaseID_CorruptBoardID_ReturnsError`:
- Inject fake scannable where `bid` column = `"not-a-valid-ulid"`
- Assert: function returns non-nil error
- Assert: returned board pointer is nil (error XOR valid entity contract)
- Assert: ERROR log entry with repo identifier, column name, and raw value

Write `TestFindTaskByID_CorruptTaskID_ReturnsError`:
- Same pattern

Run tests → must fail.

**GREEN — implement single-hydrator return-error pattern**

```go
// Before (board_repo.go:106-107):
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

Apply same pattern to `FindTaskByID` (lines 252, 253, 257).

For line 257 (optional `claimedBy` pointer path): only parse if `claimedBy != nil`; if parse fails, return error (single-hydrator policy applies to optional fields too — a corrupt pointer-field ID corrupts the aggregate).

**VERIFY**:
```bash
go test ./internal/adapters/outbound/pg/... -run TestFindBoardByPhaseID
go test ./internal/adapters/outbound/pg/... -run TestFindTaskByID
go test ./...
```

**Commit**: `fix(pg): return error on corrupt ULID in FindBoardByPhaseID and FindTaskByID`

---

### WU-10: Cluster 4 — single-hydrator sites in `session_repo.go`

**Target file**: `internal/adapters/outbound/pg/session_repo.go`
**Spec**: pg-adapter-ulid-parse (single-row hydrator requirement)
**Sites**: `scanSession` lines 113, 114, 115, 118
**Depends on**: WU-7

**RED**

Write `TestScanSession_CorruptSessionID_ReturnsError`:
- Inject fake scannable where `idStr` = `"bad-id"`
- Assert: `scanSession` returns non-nil error and nil session
- Assert: ERROR log with `"session_repo"` identifier, `"id"` column, `"bad-id"` raw value

Write `TestScanSession_CorruptOptionalField_ReturnsError` (line 118 — `sessionIDStr != nil` path):
- Inject fake where optional sessionID field is non-nil but malformed
- Assert: returns error

Run tests → must fail.

**GREEN — implement**

Apply single-hydrator pattern to `scanSession` lines 113, 114, 115:

```go
// Before (line 113):
id, _ := ids.ParseSessionID(idStr)

// After:
id, err := ids.ParseSessionID(idStr)
if err != nil {
    slog.Default().ErrorContext(ctx, "pg.SessionRepo: corrupt session_id in scanSession",
        "raw_id", idStr, "error", err)
    return nil, wrapErr("SessionRepo.scanSession.parseID", err)
}
```

For line 118 (optional `sessionIDStr != nil` pointer):
```go
// Before:
sessionID, _ := ids.ParseSessionID(*sessionIDStr)

// After:
sessionID, err := ids.ParseSessionID(*sessionIDStr)
if err != nil {
    slog.Default().ErrorContext(ctx, "pg.SessionRepo: corrupt claimed_by session_id in scanSession",
        "raw_id", *sessionIDStr, "error", err)
    return nil, wrapErr("SessionRepo.scanSession.parseClaimedBy", err)
}
```

**VERIFY**:
```bash
go test ./internal/adapters/outbound/pg/... -run TestScanSession
go test ./...
```

**Commit**: `fix(pg): return error on corrupt ULID in scanSession`

---

### WU-11: Cluster 4 — single-hydrator sites in `worktree_repo.go`

**Target file**: `internal/adapters/outbound/pg/worktree_repo.go`
**Spec**: pg-adapter-ulid-parse (single-row hydrator requirement)
**Sites**: `scanWorktree` lines 85, 88
**Depends on**: WU-7

**RED**

Write `TestScanWorktree_CorruptWorktreeID_ReturnsError`:
- Inject fake scannable where worktree ID column = `"bad-id"`
- Assert: `scanWorktree` returns non-nil error and nil worktree
- Assert: ERROR log with `"worktree_repo"` identifier, column name, and raw value

Write `TestScanWorktree_CorruptOptionalField_ReturnsError` (line 88 — optional pointer field):
- Assert same return-error behavior

Run tests → must fail.

**GREEN — implement**

Apply single-hydrator pattern at lines 85, 88. Same `wrapErr(...)` + `slog.Default().ErrorContext(...)` idiom.

**VERIFY**:
```bash
go test ./internal/adapters/outbound/pg/... -run TestScanWorktree
go test ./...
```

**Commit**: `fix(pg): return error on corrupt ULID in scanWorktree`

---

### WU-12: Success-path regression test for PR 2

**Target files**: `pg/board_repo_test.go`, `pg/session_repo_test.go`, `pg/worktree_repo_test.go`
**Spec**: success-path-regression (all scenarios for PR 2)
**Depends on**: WU-8, WU-9, WU-10, WU-11

**RED** (should already pass if GREEN is correct — run to confirm):

Write `TestFindGroupsByBoard_AllValidULIDs_NoChangeInBehavior`:
- All rows have valid ULIDs
- Assert: returned slice identical to pre-change behavior
- Assert: zero ERROR log entries emitted

Write `TestFindBoardByPhaseID_ValidULID_ReturnsBoard`:
- Valid ULID in all columns
- Assert: valid board returned, no error

Write equivalents for `scanSession` and `scanWorktree`.

**GREEN**: Fix any regressions found.

**VERIFY**:
```bash
go test ./internal/adapters/outbound/pg/...
go test ./...
```

**Commit**: `test(pg): success-path regression guard for Cluster 4`

---

## Execution Order Summary

```
PR 1 (sequential within branch):
  WU-1 (helper) → WU-2 (teamlead Cluster 2) → WU-3 (teamlead Cluster 3)
                → WU-4 (build_feedback Cluster 3)
                → WU-5 (phase/service Clusters 2+3)
                → WU-6 (success-path regression)

PR 2 (independent branch, sequential within):
  WU-7 (imports) → WU-8 (scan-loop board_repo)
                → WU-9 (hydrator board_repo)
                → WU-10 (hydrator session_repo)
                → WU-11 (hydrator worktree_repo)
                → WU-12 (success-path regression)
```

WU-2 and WU-3 touch the same file (`teamlead.go`) — implement in the same working session to avoid conflicts. WU-8 through WU-11 are sequentially safe; no parallelism recommended within PR 2.

## Spec Coverage Map

| Work Unit | Spec Delta | Requirement(s) Satisfied |
|---|---|---|
| WU-1 | app-layer-transition-errors, app-layer-repo-errors | Audit signal mechanism — foundation |
| WU-2 | app-layer-transition-errors | Structured log + audit + no abort (teamlead Cluster 2) |
| WU-3 | app-layer-repo-errors | Structured log + audit + no abort (teamlead Cluster 3) |
| WU-4 | app-layer-repo-errors | build_feedback.go sites coverage |
| WU-5 | app-layer-transition-errors, app-layer-repo-errors | RecordOutcome + SessionRepo.Save + SpawnGov.Release in phase |
| WU-6 | success-path-regression | No spurious log/audit on success, test suite green (PR 1) |
| WU-7 | pg-adapter-ulid-parse | Log field contract prerequisite |
| WU-8 | pg-adapter-ulid-parse | Scan-loop skip + log (findGroupsByBoard, findTasksByGroup) |
| WU-9 | pg-adapter-ulid-parse | Single-hydrator error return (FindBoardByPhaseID, FindTaskByID) |
| WU-10 | pg-adapter-ulid-parse | Single-hydrator error return (scanSession) |
| WU-11 | pg-adapter-ulid-parse | Single-hydrator error return (scanWorktree) |
| WU-12 | success-path-regression | No behavior change on valid ULIDs, test suite green (PR 2) |

## Out-of-Scope Guard

Do NOT touch: `phase/service.go:1291` (fallbackToMemory bug), `obs/metrics.go`, any public HTTP handler or domain port interface. If your diff includes these files, stop and review scope.
