# Archive Report: skill-risk-instrumentation

**Change**: skill-risk-instrumentation
**Archived**: 2026-06-29
**Mode**: openspec (files) + Engram (`sdd/skill-risk-instrumentation/archive-report`, project 2026)
**Verification verdict**: PASS WITH WARNINGS (0 CRITICAL, 1 WARNING, 2 SUGGESTION) — engram obs #972
**Operator decision**: ARCHIVE NOW — both PRs MERGED (orch #115, ME #20); W-1 accepted limitation documented
**Ecosystem**: sophia-orchestator (orch) + sophia-memory-engine (ME)

## Intent

Close the `rollback_count` instrumentation gap that was left open after
consolidation-worker (M2). The gap was twofold and compounded: orch never
emitted a rollback signal (so `rollback_count` stayed permanently at zero), AND
the ME demoter's `rollback_count >= 1 → blocked` branch was written as a comment
but never coded — the path was unreachable regardless of the counter.

Without this change: skills explicitly rolled back via `reeval --revert` continued
to be treated as healthy by the consolidation worker. The demoter's blocked branch
was dead code; the promoter's `rollback_count == 0` guard was vacuously true.

## What shipped

**PR1 — sophia-orchestator #115** (branch `feat/rollback-signal-emission`, merged main 2026-06-29):

- `revertRun` in `internal/application/skill/reeval.go` now emits `RollbackDelta=1`
  for each skill_id whose lifecycle transition was actually reversed — precise
  attribution, not change-scoped.
- Idempotency guard (`ExistsByRevertsRunID`) keyed on the revert run's `RevertsRunID`:
  a single indexed `SELECT EXISTS(...)` before the emit loop. Re-running the same
  revert is a no-op. The `reeval_run` table's existing `reverts_run_id` column is
  the durable marker — no migration required.
- New `MetricsPatcher` interface in `reeval.go`; satisfied by `*Service` via
  compile-time assertion. Optional nil-able field on `Reevaluator`; dry-run
  constructor receives nil (nil-safe throughout).
- New `ExistsByRevertsRunID(ctx, originalRunID string) (bool, error)` method on
  `ReevalAuditRepository` port and its pg implementation.
- 4 unit tests (TestRevertRun_*) + 1 integration test — all green.

**PR2 — sophia-memory-engine #20** (branch `feat/demoter-rollback-gate`, merged main 2026-06-29):

- `demoter.Evaluate` in `internal/application/consolidation/demoter.go` now reads
  `snap.Metrics.RollbackCount >= 1 → return "blocked", true` as the first branch
  before failure-ratio arithmetic (short-circuit per D5). The previously-commented
  dead branch is now live and M3-reachable.
- Promoter: NO production code change. Regression-lock tests added to
  `promoter_test.go` to confirm the existing generic guard blocks promotion at all
  risk levels when `rollback_count >= 1` (including low — see Policy Decision below).
- All CI checks green: GitGuardian, migrate-and-test (postgres-16), validate.

## Policy decision: rollback gates promotion at every risk level (including low)

During WU-5, `sdd-apply` surfaced a divergence: the delta spec for
`skill-promoter-regression` originally contained a scenario claiming
"Low-risk skill is NOT gated on rollback_count." The actual promoter code
contradicts this — the generic check `rollback_count > t.RollbackCount` applies
at ALL risk levels because all thresholds use zero-value `RollbackCount = 0`
(including low).

**Operator decision**: AMEND THE SPEC, not the promoter. Rationale:

- A rollback is an equally-strong negative signal as `failure_count > 0` (which
  low-risk IS already gated on, per D-M2-6). Promoting a just-reverted skill
  regardless of risk level is unsafe.
- Changing the promoter to exempt low-risk skills from the rollback gate would
  introduce new, more permissive behavior that is out of scope and rejected.
- The "low not gated" scenario was an unfounded claim — it was never true in code.

The corrected scenario in `openspec/changes/skill-risk-instrumentation/specs/skill-promoter-regression/spec.md` reads: "Rollback gates promotion at every risk level (including low)" — this is the canonical, operator-approved policy.

## Non-goal: deprecated_api_hits (explicitly deferred)

`deprecated_api_hits` instrumentation is explicitly out of scope (D1 of proposal).
It is served by the API but always zero. Activating it requires: a deprecation
definition, a static-analysis detector (staticcheck SA1019 or equivalent) running
via runtime-adapters/shell.exec, diff plumbing per applied worktree, per-skill
output parsing, and a runtime-adapters-level pipeline stage. None of these exist.
This remains a **future milestone** — it is not tracked as a deficiency of this
change.

## Accepted limitation: W-1 (partial-failure double-count window)

The idempotency guarantee is exactly-once only for revert runs that complete
emission AND persist their audit record. Because emission precedes the audit
save, a `PatchMetrics` failure mid-loop returns before persistence. On retry,
`ExistsByRevertsRunID` returns false (no audit row saved), so skills already
incremented in the partial run receive a second `+1`.

This is **intentionally accepted** and documented in the canonical spec:

- The window is operationally narrow (requires `PatchMetrics` to fail mid-loop
  on a multi-skill revert, then a subsequent retry).
- Both consumers gate on a threshold, not a precise counter: demoter
  `rollback_count >= 1`; promoter `> 0`. An inflated count never changes a
  decision.
- The alternative (emit AFTER audit save) was rejected: same failure would
  under-count and leave a reverted skill un-blocked — a false-negative on a
  safety signal, which is the more dangerous outcome.

## Specs synced to source of truth

| Domain | Action | Detail |
|---|---|---|
| rollback-signal-emission | Created | Full spec (new capability, no prior canonical spec). Covers 3 requirements: delta emitted per reverted skill, idempotency per revert run, existing revert behavior unchanged. W-1 accepted limitation documented inline. |
| skill-demoter | Updated | Replaced M2-era "rollback_count path unreachable" scenario with the 5 M3-active scenarios from the delta. Added "rollback axis evaluated before deprecated axes" requirement. All M2 requirements preserved; failure-ratio and deprecated transition requirements unchanged. |
| skill-promoter | Updated | Corrected the threshold table (low-risk `rollback_count` was `—`, now `== 0`). Added "Skill with rollback_count >= 1 is not promoted" requirement covering all risk levels with 3 regression-lock scenarios. Clarification header added with policy rationale. No existing requirements dropped. |

Main spec paths:

- `openspec/specs/rollback-signal-emission/spec.md` — NEW
- `openspec/specs/skill-demoter/spec.md` — UPDATED
- `openspec/specs/skill-promoter/spec.md` — UPDATED

## Task completion gate

All PR1 tasks (WU-1, WU-2, WU-3) confirmed complete in verify-report (obs #972).
All PR2 tasks (WU-4, WU-5) confirmed complete in apply-progress (obs #971) with
all ME CI checks green and both PRs MERGED. The `tasks.md` checkboxes in the
change folder are task-template format (not tracked completion state); completion
authority is apply-progress (#971) + verify-report (#972) + merged PR evidence.

## Final delivery (all MERGED to main)

| PR | Repo | Scope | Status |
|---|---|---|---|
| #115 | sophia-orchestator | Rollback signal emission + idempotency guard (WU-1, WU-2, WU-3) | MERGED |
| #20 | sophia-memory-engine | Demoter rollback gate + promoter regression lock (WU-4, WU-5) | MERGED |

Both PRs were well under the 400-line review budget (PR1 ~95 lines, PR2 ~35
lines). No `size:exception`. Strict TDD (RED→GREEN→VERIFY) per work unit.
Conventional commits; no AI attribution in any commit.

## SDD cycle complete

Explore → Propose → Spec → Design → Tasks → Apply → Verify → Archive ✅

## Artifact references (traceability)

**Engram observations (project 2026)**:

- Explore: `sdd/skill-risk-instrumentation/explore` — obs #932
- Proposal: `sdd/skill-risk-instrumentation/proposal` — obs #960
- Spec: `sdd/skill-risk-instrumentation/spec` — obs #967
- Design: `sdd/skill-risk-instrumentation/design` — obs #968
- Tasks: `sdd/skill-risk-instrumentation/tasks` — obs #970
- Apply-progress (PR1 + PR2 + policy decision): `sdd/skill-risk-instrumentation/apply-progress` — obs #971
- Verify-report (PR1 scope): `sdd/skill-risk-instrumentation/verify-report-pr1` — obs #972
- Archive-report: `sdd/skill-risk-instrumentation/archive-report` (this file, persisted to engram)

**OpenSpec files (change folder — stays in place per repo convention)**:

- `openspec/changes/skill-risk-instrumentation/proposal.md`
- `openspec/changes/skill-risk-instrumentation/design.md`
- `openspec/changes/skill-risk-instrumentation/tasks.md`
- `openspec/changes/skill-risk-instrumentation/specs/rollback-signal-emission/spec.md`
- `openspec/changes/skill-risk-instrumentation/specs/skill-demoter/spec.md`
- `openspec/changes/skill-risk-instrumentation/specs/skill-promoter-regression/spec.md`
- `openspec/changes/skill-risk-instrumentation/archive.md` (this file)

**Canonical specs updated**:

- `openspec/specs/rollback-signal-emission/spec.md` — NEW
- `openspec/specs/skill-demoter/spec.md` — UPDATED
- `openspec/specs/skill-promoter/spec.md` — UPDATED
