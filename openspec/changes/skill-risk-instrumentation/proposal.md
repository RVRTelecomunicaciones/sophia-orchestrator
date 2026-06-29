# Proposal: skill-risk-instrumentation

## Intent

The consolidation-worker (M2) ships both a promoter and a demoter gated on `rollback_count`. The promoter already reads `rollback_count == 0` as a pass-through condition (trivially met at zero — no change needed there). The critical gap is twofold and compounded: orch never emits a rollback signal, so the counter stays permanently at zero; AND the ME demoter's `Evaluate` function does not read `snap.Metrics.RollbackCount` at all — the intended branch was never coded, only commented. Feeding a counter into a field nobody reads will never fire the demotion gate.

This change closes both halves of that gap for `rollback_count` only. Orch emits `RollbackDelta=1` per reverted skill at the moment of a `reeval --revert` revert. ME's demoter is edited to read `RollbackCount >= 1 → blocked`. The write path (MetricsDelta.RollbackDelta → orch PATCH /metrics → postgres) and the cross-repo HTTP contract are already complete and verified from M2. No migration, no new endpoint, no new contract.

Without this change: `rollback_count` is permanently zero, the demoter's blocked-on-rollback branch is unreachable code, and the promoter's rollback==0 guard is vacuously true for every skill. Skills that have been explicitly rolled back continue to be treated as healthy.

## Operator Decisions (encoded — do NOT re-open)

| # | Decision | Value |
|---|----------|-------|
| D1 | Scope | `rollback_count` only. `deprecated_api_hits` is an explicit non-goal for this change. |
| D2 | Rollback signal | `reeval --revert` is the canonical rollback signal. A reeval revert is a skill-level rollback. |
| D3 | Signal ownership | Orch owns and emits the signal. ME does not detect rollback (consume-only). |
| D4 | Plumbing | Orch emits `RollbackDelta=1` directly at revert time via existing PATCH /metrics path. No new cross-repo contract. No new webhook. No migration. |
| D5 | Demoter behavior | `RollbackCount >= 1 → blocked`, immediate (one rollback blocks). |
| D6 | Attribution | RollbackDelta attaches only to the `skill_ids` actually reverted in that reeval run, not all change skills. |
| D7 | Idempotency | Repeated or re-run reverts of the same run MUST NOT double-count. Idempotency key = revert run ID. |

## Scope

### In Scope — PR1 sophia-orchestator

1. **Rollback signal emission**: at `internal/application/skill/reeval.go` in the `revertRun` function (~line 279–384), after a skill's lifecycle transition is reversed, call the existing skills service/repo path to increment `RollbackCount=1` for each reverted skill_id via `PATCH /api/v1/skills/{id}/metrics` with `rollback_delta=1`.
2. **Idempotency guard**: key on the `RevertsRunID` to ensure a re-run of the same revert does not double-count. Implement as a check before emitting the delta (e.g., query existing `rollback_count` for that revert run ID, or use a revert-run-scoped flag in the audit record).
3. **No migration required**: `RollbackCount` already exists in the skills table (served by `GET /skills`, written by `PATCH /metrics`).

### In Scope — PR2 sophia-memory-engine

1. **Demoter edit**: at `internal/application/consolidation/demoter.go` in `Evaluate` (~line 47–61), add the branch: `if snap.Metrics.RollbackCount >= 1 { return blocked }`. This branch was planned but never coded.
2. **No other demoter logic changes**: the `deprecated_api_hits` branch and the `avg_retry_reduction` / `failure_rate` branches are untouched.

### Confirmed: Promoter — No Change Needed

`promoter.go` (lines 16–17, 40–41) already reads `rollback_count == 0` as a pass condition. At zero, this condition is trivially true. Once orch starts emitting non-zero values, the existing guard correctly blocks promotion. No promoter edit required.

### Out of Scope (explicit non-goals)

| Non-goal | Why deferred |
|----------|-------------|
| `deprecated_api_hits` instrumentation | Requires: a deprecation definition (none exists), a static-analysis detector running via runtime-adapters/shell.exec@v1, diff plumbing per applied worktree, parsing tool output (staticcheck SA1019 or equivalent), and per-skill attribution. None of these exist. Too design-heavy for this slice. |
| New cross-repo HTTP contract | The existing MetricsDelta.RollbackDelta + PATCH /metrics path is sufficient. No new endpoint, no new webhook, no new field. |
| DB migration | RollbackCount column already exists in the skills table. |
| Change/phase-level rollback attribution | Scope is reeval --revert only. Change-level failure is recorded as `failure`, not rollback. Closed skill_usage Outcome enum (SQL CHECK in migration 011) is not modified. |
| `deprecated_api_hits` demoter branch | The commented-out branch in demoter.go for `deprecated_api_hits` is not activated in this change. |
| LLM involvement | No LLM in any code path. Orch is a coordinator; ME consolidation forbids LLM (D-M2-12). |

## Approach

**Signal source**: `reeval --revert` (internal/application/skill/reeval.go). When `revertRun` reverses a skill's lifecycle transition, it has the skill_id and the RevertsRunID in hand. This is the cleanest, most honest rollback signal: it is orch-owned (D2), already recorded as an audit run with `Mode="revert"`, and requires no new data model.

**Emission path**: orch calls the existing `PATCH /api/v1/skills/{id}/metrics` with `rollback_delta=1` for each reverted skill_id. The MetricsDelta struct and orch handler already accept this field (verified in consolidation-worker/verify.md:86). The call happens inline at revert time — no background goroutine, no outbox, no webhook.

**Idempotency**: the revert audit run's RevertsRunID is the natural idempotency key. Before emitting a delta, orch checks whether a `rollback_delta` has already been recorded for this run ID. This prevents double-counting when a revert is re-run or retried.

**Demoter activation**: ME's demoter.Evaluate must be edited to add `if snap.Metrics.RollbackCount >= 1 { return blocked }` at the top of its evaluation logic (before the failure-rate and retry-reduction checks). This is a targeted, minimal edit to an existing function — not a new file or new pipeline stage.

**Sequencing matters**: PR2 (ME demoter) without PR1 (orch signal) is inert — demoter will never see RollbackCount > 0. PR1 without PR2 populates the counter but the blocked gate never fires. Both PRs are required for the feature to have any observable effect. Recommend PR1 merges first (orch emits signal); PR2 activates the gate. System is safe in the intermediate state: a non-zero RollbackCount with a demoter that doesn't read it is neutral (no regression, no false positives).

## Cross-Repo Impact

| Repo | Change | Criticality |
|------|--------|-------------|
| sophia-orchestator | Emit RollbackDelta=1 per reverted skill in revertRun; idempotency guard | Required for signal to exist |
| sophia-memory-engine | Edit demoter.Evaluate to read RollbackCount >= 1 → blocked | Required for signal to have effect |

No other repos affected. No shared configuration changes. No API contract additions.

## Affected Files

### PR1 — sophia-orchestator

| File | Change |
|------|--------|
| `internal/application/skill/reeval.go` | Modify `revertRun` (~279–384): emit RollbackDelta=1 per reverted skill_id via existing skills client/PATCH path; add idempotency check keyed on RevertsRunID |
| `internal/application/skill/reeval_test.go` | Add: test that revertRun emits RollbackDelta=1 per reverted skill; test that re-run is idempotent (no double-count) |

### PR2 — sophia-memory-engine

| File | Change |
|------|--------|
| `internal/application/consolidation/demoter.go` | Modify `Evaluate` (~47–61): add `if snap.Metrics.RollbackCount >= 1 { return blocked }` branch |
| `internal/application/consolidation/demoter_test.go` | Add: test that RollbackCount >= 1 produces blocked; test that RollbackCount == 0 does not trigger this branch |

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Demoter gap wider than emit gap — must edit demoter.go, not just emit | CRITICAL (already realized) | Feature inert without the edit | Proposal explicitly scopes the demoter edit as a required deliverable in PR2. Verify phase checks both halves. |
| Idempotency failure: re-run reverts double-count RollbackCount | Med | Skill incorrectly accumulates rollback count over identical retried reverts | Idempotency guard keyed on RevertsRunID is in-scope for PR1. Failing test required before implementation. |
| Attribution error: RollbackDelta emitted for all change skills, not just reverted ones | Med | Unreverted skills incorrectly marked as rolled-back, blocked from active | Proposal encodes: RollbackDelta attaches ONLY to skill_ids in the revert set for that run. |
| PR1 merged, PR2 not yet merged: counter non-zero, demoter not reading it | Low | No regression — existing behavior preserved; demoter blind to new counter until PR2 ships | Documented as expected intermediate state. Safe. |
| PR2 merged first (wrong order): demoter reads RollbackCount, always sees 0 | Low | Feature inert until PR1 ships | Sequencing note in delivery section. PR1 must merge first. |
| `reeval --revert` re-architecture in future | Low | Idempotency key or call site may need updates | Scoped to current revertRun path. Any restructure of reeval would revisit this. |

## Delivery

**Two PRs, strict sequencing.**

- **PR1 — sophia-orchestator**: signal emission + idempotency. Ships first. System is in a valid intermediate state (counter can become non-zero; demoter not yet reading it — no regression).
- **PR2 — sophia-memory-engine**: demoter edit. Ships second. Activates the blocked gate.

Both PRs are small and targeted. No `size:exception` expected. Standard review process applies.

Strict TDD: failing test before production code for every behavior (revert emits delta; idempotency holds; demoter reads RollbackCount; RollbackCount=0 does not trigger).

## Success Criteria

### PR1 — sophia-orchestator

- [ ] `revertRun` emits `RollbackDelta=1` for each skill_id in the revert set.
- [ ] Emitting the delta for the same RevertsRunID a second time is a no-op (idempotency).
- [ ] Skills NOT in the revert set for a given run receive no delta.
- [ ] `go test ./...` green, `golangci-lint run` clean.

### PR2 — sophia-memory-engine

- [ ] `demoter.Evaluate` returns `blocked` when `snap.Metrics.RollbackCount >= 1`.
- [ ] `demoter.Evaluate` does NOT return `blocked` on this branch when `RollbackCount == 0`.
- [ ] Existing demoter behavior (failure rate, avg_retry_reduction) is unchanged.
- [ ] `go test ./...` green, `golangci-lint run` clean.

### End-to-End Acceptance

- [ ] A skill that has been reverted via `reeval --revert` accumulates `RollbackCount >= 1`.
- [ ] On next consolidation cycle, that skill transitions `active → blocked`.
- [ ] A skill with `RollbackCount == 0` is not affected by this branch.

## Open Questions

None. All operator decisions are locked (D1–D7 above). `deprecated_api_hits` is explicitly deferred. Cross-repo sequencing is defined. Idempotency strategy is defined. Demoter behavior is defined.

## Dependencies

- consolidation-worker (M2) merged and verified. ✅ (Skills table has RollbackCount column; PATCH /metrics accepts rollback_delta; MetricsDelta carries RollbackDelta; demoter.go exists at known path.)
- reeval --revert merged. ✅ (revertRun path exists at internal/application/skill/reeval.go.)
