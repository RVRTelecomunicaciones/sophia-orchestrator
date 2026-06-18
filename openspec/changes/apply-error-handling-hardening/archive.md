# Archive Report: apply-error-handling-hardening

**Change**: apply-error-handling-hardening
**Archived**: 2026-06-18
**Mode**: openspec (files) + Engram (`sdd/apply-error-handling-hardening/archive-report`, project 2026)
**Verification verdict**: PASS-WITH-WARNINGS (0 CRITICAL across both PRs) — PR1 obs #940, PR2 obs #941
**Operator decision**: ARCHIVE (both PRs MERGED to main — #110 + #111)
**Ecosystem**: sophia-orchestator only

## Intent

Three clusters of silently-discarded errors existed across the apply pipeline and the pg
adapter layer, violating design invariant **D1.2** ("Every phase produces a validated Envelope
before any caller-visible state change") and the repo's "Never do this" rule.

- **Clusters 2+3 (PR #110)** — application layer: `_ =` discards on domain state-machine
  transition calls and repository persistence calls. Made observable via structured log + audit
  trail; control flow unchanged (log-and-continue policy, severity-by-failure-class).
- **Cluster 4 (PR #111)** — pg adapter layer: `_ =` discards on `ids.Parse*()` calls during
  row scanning. Fixed by logging + skipping (scan loops) or logging + returning error (single
  hydrators). Zero-value domain IDs can no longer propagate downstream.

## Capabilities delivered (2)

| Capability | Type | PR | Where |
|---|---|---|---|
| apply-error-observability | New | PR #110 (`fix/apply-error-handling-app-layer`) | orch main, merged 2026-06-18 |
| pg-adapter-ulid-parse | New | PR #111 (`fix/apply-error-handling-pg-adapter`) | orch main, merged 2026-06-18 |

## PRs landed (2)

| PR | Branch | Scope | Estimated lines | Verdict |
|---|---|---|---|---|
| #110 | `fix/apply-error-handling-app-layer` | Clusters 2+3: apply + phase application layer | ~230 | PASS-WITH-WARNINGS |
| #111 | `fix/apply-error-handling-pg-adapter` | Cluster 4: pg adapter ULID parse | ~95 | PASS-WITH-WARNINGS |

Both PRs well under 400-line threshold. Independent branches; no code dependency between them.
Delivery strategy: stacked-to-main.

## Specs synced to source of truth

Capability granularity decision: the 4 delta specs were consolidated into 2 canonical capability
specs, aligned with the hexagonal boundary that drove the PR split. The `success-path-regression`
delta is a cross-cutting negative contract — its requirements were absorbed into the two
capability specs rather than creating a third standalone spec file.

| Domain | Action | Detail |
|---|---|---|
| apply-error-observability | Created | Full spec (new capability). Merges `app-layer-transition-errors` + `app-layer-repo-errors` + app-layer portion of `success-path-regression`. Includes severity-by-failure-class table and implementation notes. |
| pg-adapter-ulid-parse | Created | Full spec (new capability). Merges `pg-adapter-ulid-parse` delta + pg portion of `success-path-regression`. Includes scan-loop vs single-hydrator policy and implementation notes. |

Main specs:
- `openspec/specs/apply-error-observability/spec.md`
- `openspec/specs/pg-adapter-ulid-parse/spec.md`

## Test / build evidence

### PR1 (obs #940)

- `GOWORK=off go test ./internal/application/...` — 13/13 packages PASS (apply: 3.7s, phase: 0.02s)
- `GOWORK=off go test ./internal/...` — ALL packages PASS, 0 FAIL
- `GOWORK=off go build ./...` — clean, no output
- Commit attribution: 7 commits, no `Co-Authored-By`, all conventional commits (`fix(apply)`, `test(apply)`)

### PR2 (obs #941)

- `GOWORK=off go test ./internal/adapters/outbound/pg/... -count=1` — 18 PR2-specific tests + pre-existing tests: ALL PASS (0.009s)
- `GOWORK=off go build ./...` — clean, no output
- `golangci-lint run ./internal/adapters/outbound/pg/...` — 0 issues
- Commit attribution: 1 commit (accepted by operator), no `Co-Authored-By`, `fix(pg)` prefix

## Accepted follow-ups (non-blocking)

### PR1 WARNING-2 (weak test isolation for build_feedback)

`TestBuildFeedback_SaveGroupErr_*` tests use `saveGroupErrAfterN=3` and assert
`op == "BoardRepo.SaveGroup"`. The assertion would pass even if `build_feedback.go:154` were
not instrumented, because `teamlead.go:186` would still fire with the same op name. The
implementation IS correct (confirmed by code inspection). Remediation: add a distinct op marker
(e.g., `"build_feedback.SaveGroup.skip"`) at build_feedback sites, or assert a minimum event
count that requires both sites to fire. Low urgency.

### PR2 WARNING-1 (loop-level skip not tested end-to-end)

The spec scenario "One bad ULID row among valid rows → returned slice contains N-1 rows" has no
integration-level test. The `continue` branch in `findGroupsByBoard` / `findTasksByGroup` is
covered by a mutation guard (replacing `continue` with `return nil, err` makes 6/6 corrupt-row
tests fail), confirming the branch is load-bearing. A testcontainers-based integration test
driving `findGroupsByBoard` with a real mixed valid/invalid row set would provide full
end-to-end confidence. Recommended as a follow-up before this code path is exercised heavily in
production.

### PR2 SUGGESTION-1 (context-less slog in scan helpers)

`scanGroupRow` and `scanTaskRow` use `slog.Default().Error(...)` (no context) because they do
not receive `ctx`. Log entries from these helpers omit trace/request IDs. Not a spec violation.
Remediation: pass `ctx` to the helpers in a future change.

### PR2 SUGGESTION-2 (policy-doc tests for FindBoardByPhaseID)

`TestBoardRepo_ParseBoardID_ZeroValuePolicy` and siblings test `ids.Parse*` behavior directly
rather than driving the adapter's error-return path via a fake scannable. They document the
contract but do not execute `FindBoardByPhaseID` end-to-end. Accept as supplementary
documentation, or add `fakeScannable`-based tests in a follow-up.

## Task completion gate (archive-time reconciliation)

The `tasks.md` file has WU-7 through WU-12 unchecked (`- [ ]`) — the checkbox state was not
updated after `sdd-apply` completed PR2. This is a stale artifact.

Per the sdd-archive Strict-vs-OpenSpec policy, archive-time reconciliation is permitted when
apply-progress + verify-report prove every task is complete. That proof exists here:

- `apply-progress` (obs #939): "SDD change apply-error-handling-hardening fully implemented
  across 2 independent PRs to main. PR #110 MERGED (all green). PR #111 CI running → subsequently
  MERGED." All WU-1 through WU-12 are confirmed complete.
- PR1 verify report (obs #940): "WU-1 through WU-6 marked [x] in tasks.md. WU-7 through WU-12
  correctly NOT marked (PR2 pending)" — this confirms the checkbox gap is in `tasks.md`, not in
  the implementation.
- PR2 verify report (obs #941): "WU-7 through WU-12: all complete per apply-progress artifact
  #939. All 17 sites implemented (verified from code)."
- Both PRs are MERGED to main (confirmed by orchestrator context: branch
  `chore/archive-apply-error-handling-hardening` off latest main, PRs #110+#111 merged).

**Reconciliation reason**: the unchecked `- [ ]` items for WU-7 through WU-12 reflect a stale
tasks artifact — `sdd-apply` did not re-sync checkboxes after PR2 implementation. Apply-progress
(#939), verify reports (#940, #941), and merged PR status prove all 12 work units are complete.
No incomplete implementation work blocks this archive.

## SDD cycle complete

Explore → Propose → Spec → Design → Tasks → Apply → Verify → Archive ✅

## Artifact references (traceability)

**Engram observations (project 2026)**:
- Proposal: `sdd/apply-error-handling-hardening/proposal` — obs #935 (inferred; search topic key to confirm)
- Spec: `sdd/apply-error-handling-hardening/spec` — obs #936 (inferred)
- Design: `sdd/apply-error-handling-hardening/design` — obs #937 (inferred)
- Tasks: `sdd/apply-error-handling-hardening/tasks` — obs #938 (inferred)
- Apply-progress: `sdd/apply-error-handling-hardening/apply-progress` — obs #939
- Verify-report PR1: `sdd/apply-error-handling-hardening/verify-report` — obs #940
- Verify-report PR2: `sdd/apply-error-handling-hardening/verify-report-pr2` — obs #941
- Archive-report: `sdd/apply-error-handling-hardening/archive-report` (this file, persisted to engram)

**OpenSpec files**:
- Change folder: `openspec/changes/apply-error-handling-hardening/` (archived in place via this
  `archive.md`, per repo convention — changes are not moved to an `archive/` dir)
- Main specs (2 new capabilities):
  - `openspec/specs/apply-error-observability/spec.md`
  - `openspec/specs/pg-adapter-ulid-parse/spec.md`
