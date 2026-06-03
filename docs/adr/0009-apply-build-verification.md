# ADR-0009 — Apply-Phase Group Build Verification

- **Status**: accepted
- **Date**: 2026-06-02
- **Deciders**: Russell Vergara

## Context

The apply phase previously relied entirely on task self-reporting to determine
group completion. A task agent returning a `DONE` envelope was sufficient to
advance the group toward `GroupStatusCompleted`, regardless of whether the
generated code actually compiles or builds.

This created a silent failure mode: a group could be marked completed while its
worktree contained code that does not build. Downstream phases (review, deploy)
would then inherit broken artifacts, and the first build failure surface would
be far removed from the apply agent that introduced it.

Relevant spec scenarios: apply-orchestration §Group Completion Gate,
apply-build-verification §Manifest Auto-Detection and §Build Observability.

## Decision

1. **Group-scoped build gate** — after all tasks in a group report done and
   before `group.Complete()` is called, the orchestrator inspects the group's
   worktree for a recognized build manifest (`go.mod`, `package.json` with a
   `scripts.build` entry, `pubspec.yaml`) and executes the corresponding build
   command via `shell.exec@v1` with `working_dir` equal to
   `group.WorktreePath()`.

2. **Manifest-absent skip path** — when no recognized manifest is present,
   the build gate is bypassed and the group completes on task self-report alone.
   This preserves today's behavior for `WorktreeInit=empty` groups (backward
   compatibility invariant; spec §Backward-Compat Invariant for Empty
   Worktrees).

3. **Group-level build attempts** — build failures trigger a group repair
   re-dispatch (not a per-task retry). The budget is capped at
   `apply.MaxAttempts` (3), the same constant that governs task-level Iron
   Law #5 escalation. When the budget is exhausted the group is marked
   `GroupStatusFailed` and escalated.

4. **`GroupBuildStatus` domain type** — a new orthogonal status field on
   `Group` tracks the build-gate lifecycle independently of `GroupStatus`:

   | Value     | Meaning |
   |-----------|---------|
   | `pending` | Build gate not yet evaluated (initial state) |
   | `skipped` | No recognized manifest; gate bypassed |
   | `passed`  | Build exited with code 0 |
   | `failed`  | Budget exhausted; group failed |

5. **Persistence** — `GroupBuildStatus` and `buildAttempts` are persisted to
   the `groups` table (migration 008). This makes the budget resume-safe: an
   orchestrator restart mid-repair does not reset the attempt counter, so
   `MaxAttempts` is enforced across process restarts.

6. **Task hydration fix** — the V1 board_repo implementation discarded
   persisted task state (status, claimed_by, attempts, envelope) on board
   reload, re-creating fresh `Pending` tasks on every resume. This was
   acknowledged as a V2 ergonomics improvement. Migration 008 ships the fix:
   `board_repo.go` now reads those columns and uses `apply.HydrateTask` to
   reconstruct full task state. This corrects the flagged resume risk without
   requiring a schema change (the columns were already written; only the read
   path was absent).

7. **SSE observability** — three new events bracket every build attempt:
   - `apply.build.started` `{ group_id, manifest, command, args, attempt }`
   - `apply.build.passed` `{ group_id, manifest, command, attempt, duration_ms }`
   - `apply.build.failed` `{ group_id, manifest, command, attempt, exit_code, stderr, truncated }`

   `stderr` in the failure event is truncated to 4 KB (head + tail) to bound
   token usage while preserving the first error and tail context.

## Consequences

### Positive

- Apply phase outcomes are compiler-verified, not purely self-reported. This
  is the first enforcement layer between agent output and downstream phases.
- The `GroupBuildStatus` resume contract means the 3-attempt budget survives
  orchestrator restarts cleanly.
- Task state is now fully restored on resume; previously, a resumed board
  would replay tasks from scratch, which could cause double-execution of
  already-completed work.
- SSE consumers gain structured build telemetry with bounded stderr, which is
  useful for monitoring dashboards and repair-prompt assembly.
- The skip path guarantees no behavioral change for projects without a
  supported build manifest.

### Negative

- Apply runs are longer for groups with a recognized manifest, by roughly one
  build invocation plus up to `MaxAttempts - 1` repair cycles.
- The orchestrator must now carry knowledge of the manifest detection registry
  (application layer, `build_registry.go`), introducing a small coupling
  between the orchestrator and ecosystem-specific build toolchains.
- A new DB migration is required. Rolling back requires `migration 008 down`
  before deploying the prior binary.

### Neutral

- `apply.MaxAttempts` is reused as the build-budget cap. This is a deliberate
  choice: the constant's semantic is "three strikes and escalate", which maps
  directly onto the build-repair loop. If the two budgets ever need to diverge,
  a separate `MaxBuildAttempts` constant can be introduced without breaking the
  domain invariant.
- `HydrateGroup` and `HydrateTask` are new domain constructors. They are
  intentionally not guarded by transition rules — they are a persistence
  primitive, not a domain workflow operation.
- `AttachTaskToGroup` is a package-level helper that bypasses `AddTask`'s
  transition guard. It is visible only within the `apply` package and its
  immediate callers; it must not be used outside the persistence adapter.

## Alternatives considered

- **Per-task builds**: rejected — build failures are group-wide (cross-task
  dependencies compile together); charging a single task with the failure is
  false accounting.
- **Finalize-level build (after all groups)**: rejected — the group's worktree
  is the unit of isolation; building at finalize time loses the per-group
  repair signal and makes stderr attribution ambiguous.
- **Full `GroupBuild` aggregate**: rejected — the minimal `GroupBuildStatus` +
  `buildAttempts` fields capture the necessary budget and resume state.
  The build command is re-derivable from the worktree on every attempt, so
  there is no need to persist it.
- **Reset task attempts on build failure**: rejected — task attempts reflect
  agent implementation quality, not build correctness; conflating them would
  make attempt accounting misleading.

## References

- Spec: `openspec/changes/apply-build-feedback-loop/specs/apply-orchestration/spec.md`
- Spec: `openspec/changes/apply-build-feedback-loop/specs/apply-build-verification/spec.md`
- Design: `openspec/changes/apply-build-feedback-loop/design.md`
- Migration: `migrations/postgres/008_group_build_state.up.sql`
- Domain: `internal/domain/apply/status.go`, `group.go`, `task.go`
- Persistence: `internal/adapters/outbound/pg/board_repo.go`
- ADR-0004: PostgreSQL version target
- ADR-0006: Wire alignment audit (confirmed `shell.exec@v1` working_dir contract)
