# Tasks: skill-risk-instrumentation

## Review Workload Forecast

| Repo | Production lines | Test lines | Total |
|------|-----------------|-----------|-------|
| sophia-orchestator (PR1) | ~35 | ~60 | ~95 |
| sophia-memory-engine (PR2) | ~5 | ~30 | ~35 |
| **Combined** | **~40** | **~90** | **~130** |

- **Chained PRs recommended**: N/A (cross-repo, 2 independent PRs)
- **400-line budget risk**: Low (PR1 ~95 lines, PR2 ~35 lines — both well under budget)
- **Decision needed before apply**: Yes (cross-repo sequencing — PR1 must merge before PR2 activates end-to-end, though both are safe to merge independently)

---

## Delivery Sequence

```
PR1 (sophia-orchestator) → merge → PR2 (sophia-memory-engine)
```

PR1 is inert alone: counter populates but ME demoter gate never fires.
PR2 is inert alone: gate reads RollbackCount but always sees zero until PR1 lands.
Neither breaks anything in the intermediate state. Feature is live end-to-end only when both land.

---

## PR1 — sophia-orchestator (rollback signal emission)

### WU-1: Add `ExistsByRevertsRunID` to `ReevalAuditRepository` interface and pg implementation

**Spec**: rollback-signal-emission — Requirement: Rollback delta idempotency per revert run

**Target files**:
- `internal/ports/outbound/repository.go` — extend `ReevalAuditRepository` interface
- `internal/adapters/outbound/pg/reeval_audit_repo.go` — implement method
- `internal/adapters/outbound/pg/reeval_audit_repo_integration_test.go` — integration tests

**RED** — write failing integration tests first:

```
// File: internal/adapters/outbound/pg/reeval_audit_repo_integration_test.go
// Test 1: ExistsByRevertsRunID returns true when a revert run with that reverts_run_id exists
// Test 2: ExistsByRevertsRunID returns false for an unknown originalRunID
// Run: GOWORK=off go test ./internal/adapters/outbound/pg/... — expect compile error
//      (method does not exist on interface yet)
```

**GREEN** — implement:

1. In `repository.go`, add to `ReevalAuditRepository` interface:
   ```go
   ExistsByRevertsRunID(ctx context.Context, originalRunID string) (bool, error)
   ```
2. In `reeval_audit_repo.go`, implement:
   ```go
   func (r *ReevalAuditRepo) ExistsByRevertsRunID(ctx context.Context, originalRunID string) (bool, error) {
       var exists bool
       err := r.db.QueryRow(ctx,
           `SELECT EXISTS(SELECT 1 FROM reeval_run WHERE reverts_run_id = $1)`,
           originalRunID,
       ).Scan(&exists)
       return exists, err
   }
   ```

**VERIFY**:
```bash
GOWORK=off go test ./internal/adapters/outbound/pg/... -run TestReevalAuditRepo_ExistsByRevertsRunID
```

**Commit**: `feat(application): add ExistsByRevertsRunID to ReevalAuditRepository for idempotency`

---

### WU-2: Add `MetricsPatcher` interface and wire into `Reevaluator`

**Spec**: rollback-signal-emission — Requirement: Rollback delta emitted per reverted skill (architecture setup)

**Target files**:
- `internal/application/skill/reeval.go` — new interface + field on `Reevaluator`
- `internal/application/skill/reeval_provider.go` — wire `*Service` as `MetricsPatcher` in `NewReevaluatorWithAudit`

**Depends on**: WU-1 (interface must compile)

**RED** — write failing unit test first:

```
// File: internal/application/skill/reeval_revert_test.go
// TestReevaluator_RevertRun_NilMetricsPatcher
//   Given: Reevaluator built WITHOUT a MetricsPatcher (dry-run constructor path)
//   When:  revertRun executes normally
//   Then:  completes without panic; no PatchMetrics call
// Run: GOWORK=off go test ./internal/application/skill/... — expect compile error
//      (MetricsPatcher type/field does not exist yet)
```

**GREEN** — implement:

1. In `reeval.go`, add below the existing `StatusPatcher` interface:
   ```go
   // MetricsPatcher increments a skill's additive metric counters.
   // Satisfied by *Service.PatchMetrics — no new type required.
   type MetricsPatcher interface {
       PatchMetrics(ctx context.Context, skillID string, delta inbound.MetricsDelta) error
   }
   ```
2. Add `metricsPatcher MetricsPatcher` field to the `Reevaluator` struct (adjacent to existing `patcher StatusPatcher`).
3. In `reeval_provider.go:89`, pass `s` (which satisfies `MetricsPatcher` via `service.go:65`) into `NewReevaluatorWithAudit`. Confirm `NewReevaluator` (dry-run constructor) does NOT receive it.

**VERIFY**:
```bash
GOWORK=off go test ./internal/application/skill/... -run TestReevaluator_RevertRun_NilMetricsPatcher
GOWORK=off go build ./...
```

**Commit**: `feat(application): add MetricsPatcher interface to Reevaluator and wire via provider`

---

### WU-3: Emit `RollbackDelta=1` per reverted skill with idempotency guard

**Spec**: rollback-signal-emission — all emission and idempotency scenarios

**Target files**:
- `internal/application/skill/reeval.go` — idempotency check at line ~305, emission at line ~364
- `internal/application/skill/reeval_revert_test.go` — full unit test suite

**Depends on**: WU-1, WU-2

**RED** — write all failing unit tests first:

```
// File: internal/application/skill/reeval_revert_test.go
//
// fakeMetricsPatcher: captures (skillID, delta) pairs per call
// fakeAuditRepo (existing): extend with ExistsByRevertsRunID(originalRunID string) bool stub
//
// Test 1: TestRevertRun_EmitsDeltaPerRevertedSkill
//   GIVEN fake revert run reverting skills [A, B]
//   WHEN  revertRun executes
//   THEN  fakeMetricsPatcher has exactly 2 calls: (A, RollbackDelta=1) and (B, RollbackDelta=1)
//   SPEC: "Multiple reverted skills each receive exactly one delta"
//
// Test 2: TestRevertRun_SkipsNonRevertedSkills
//   GIVEN change has skills [A, B, C]; revert set is {A} only
//   WHEN  revertRun executes
//   THEN  fakeMetricsPatcher has exactly 1 call: (A, RollbackDelta=1)
//   AND   no call for B or C
//   SPEC: "Non-reverted skills in the same change are not incremented"
//
// Test 3: TestRevertRun_Idempotency_SameRunIDSkipsEmission
//   GIVEN fakeAuditRepo.ExistsByRevertsRunID returns true for run.ID
//   WHEN  revertRun executes
//   THEN  fakeMetricsPatcher receives 0 calls
//   AND   status walks still execute (revert audit loop runs to completion)
//   SPEC: "Repeated execution of the same run is a no-op"
//
// Test 4: TestRevertRun_DifferentRunIDs_EmitIndependently
//   GIVEN R1 already exists (ExistsByRevertsRunID returns true for R1)
//   AND   R2 is new (ExistsByRevertsRunID returns false for R2), reverts skill A
//   WHEN  revertRun executes for R2
//   THEN  fakeMetricsPatcher called once: (A, RollbackDelta=1)
//   SPEC: "Different revert runs are independent"
//
// Run: GOWORK=off go test ./internal/application/skill/... — expect failures (logic not yet coded)
```

**GREEN** — implement in `reeval.go`:

1. Add `skipMetrics bool` local variable in `revertRun` before the per-item loop.
2. Insert idempotency check at ~line 305 (after `revRunID` is minted, before the loop):
   ```go
   if r.audit != nil && r.metricsPatcher != nil {
       exists, err := r.audit.ExistsByRevertsRunID(ctx, run.ID)
       if err != nil {
           return result, fmt.Errorf("skill.Reevaluator.revertRun: idempotency check: %w", err)
       }
       if exists {
           skipMetrics = true
       }
   }
   ```
3. Insert metric emission at ~line 364, inside the `row.Reverted = true` branch after `result = append(result, row)`:
   ```go
   if !skipMetrics && r.metricsPatcher != nil {
       if pErr := r.metricsPatcher.PatchMetrics(ctx, item.SkillID,
           inbound.MetricsDelta{RollbackDelta: 1}); pErr != nil {
           return result, fmt.Errorf("skill.Reevaluator.revertRun: patch metrics %s: %w",
               item.SkillID, pErr)
       }
   }
   ```

**VERIFY**:
```bash
GOWORK=off go test ./internal/application/skill/... -run TestRevertRun
GOWORK=off go test ./internal/adapters/outbound/pg/...
GOWORK=off go test ./...
```

**Commit**: `feat(application): emit RollbackDelta=1 per reverted skill with idempotency guard`

---

### PR1 Summary

| WU | Files touched | Spec scenarios covered | Sequential |
|----|--------------|------------------------|-----------|
| WU-1 | `repository.go`, `reeval_audit_repo.go`, integration test | Idempotency (repo layer) | First |
| WU-2 | `reeval.go`, `reeval_provider.go` | Architecture wiring, nil-safe path | After WU-1 |
| WU-3 | `reeval.go`, `reeval_revert_test.go` | All emission + idempotency scenarios | After WU-1 + WU-2 |

---

## PR2 — sophia-memory-engine (demoter gate + promoter regression lock)

> PR2 is safe to develop in parallel with PR1 but MUST be merged AFTER PR1 in production.
> The WU-4/WU-5 split within PR2 is sequential.

### WU-4: Activate `rollback_count >= 1 → blocked` branch in demoter

**Spec**: skill-demoter — Requirement: active → blocked transition (rollback axis)
**Spec**: skill-demoter — Requirement: rollback axis evaluated before deprecated axes

**Target files**:
- `internal/application/consolidation/demoter.go` — insert branch at line 43
- `internal/application/consolidation/demoter_test.go` — new test cases

**RED** — write failing tests first:

```
// File: internal/application/consolidation/demoter_test.go
//
// Test 1: TestDemoter_RollbackCount_Blocked_SingleCount
//   GIVEN active skill, RollbackCount=1, failure_count=0, usage_count=10
//   WHEN  Evaluate(snap)
//   THEN  returns ("blocked", true)
//   SPEC: "rollback_count >= 1 triggers blocked"
//
// Test 2: TestDemoter_RollbackCount_Zero_NotBlocked
//   GIVEN active skill, RollbackCount=0, failure ratio <= 0.15
//   WHEN  Evaluate(snap)
//   THEN  returns ("", false)
//   SPEC: "rollback_count = 0 does not trigger blocked on this axis"
//
// Test 3: TestDemoter_RollbackCount_Blocked_MultiCount
//   GIVEN active skill, RollbackCount=2, failure_count=0
//   WHEN  Evaluate(snap)
//   THEN  returns ("blocked", true)
//   SPEC: threshold is >=1, not >1
//
// Test 4: TestDemoter_RollbackCount_ShortCircuitsDeprecatedEval
//   GIVEN active skill, RollbackCount=1, avg_retry_reduction=0.03 (would trigger deprecated)
//   WHEN  Evaluate(snap)
//   THEN  returns ("blocked", true) — NOT ("deprecated", true)
//   SPEC: "rollback_count short-circuits deprecated evaluation"
//
// Test 5: TestDemoter_RollbackCount_PrecedenceOverFailureRatio (regression)
//   GIVEN active skill, RollbackCount=2, failure ratio > 0.15 (both blocked conditions)
//   WHEN  Evaluate(snap)
//   THEN  returns ("blocked", true) exactly once (not double-blocked)
//   SPEC: "rollback_count >= 1 takes precedence — blocked applied"
//
// Existing tests that MUST still pass (no regression):
//   TestDemoter_FailureRatio
//   TestDemoter_LowRetryReduction_Deprecated
//   TestDemoter_BothConditions_BlockedTakesPrecedence
//
// Run: GOWORK=off go test ./internal/application/consolidation/... — expect failures
```

**GREEN** — implement in `demoter.go`:

Insert at line 43 (between `if snap.Status != "active"` guard at ~line 39 and the `failureRatio` computation block at current line 43):

```go
// RollbackCount >= 1 → immediate block (M3-reachable via skill-risk-instrumentation).
if snap.Metrics.RollbackCount >= 1 {
    return "blocked", true
}
```

Also update the package-level comment (or function-level comment) to mark this path as M3-reachable.

**VERIFY**:
```bash
GOWORK=off go test ./internal/application/consolidation/... -run TestDemoter
GOWORK=off go test ./internal/application/consolidation/...
```

**Commit**: `feat(consolidation): activate rollback_count >= 1 blocked branch in demoter`

---

### WU-5: Add promoter regression-lock tests

**Spec**: skill-promoter-regression — Requirement: Skill with rollback_count >= 1 is not promoted

**Target files**:
- `internal/application/consolidation/promoter_test.go` — 3 regression test cases (NO production code change)

**Depends on**: WU-4 (same PR, same test run)

**RED** — write tests that already pass against existing promoter code (these are regression-lock tests; they should go GREEN immediately after being written, confirming the existing guard is in place):

```
// File: internal/application/consolidation/promoter_test.go
//
// Test 1: TestPromoter_Regression_MediumRisk_BlockedByRollbackCount
//   GIVEN candidate skill, risk_level=medium, success_count=2, failure_count=0,
//         tests_passed_count=2, avg_retry_reduction=0.25, deprecated_api_hits=0,
//         rollback_count=1
//   WHEN  Evaluate(snap)
//   THEN  PatchStatus NOT called (rollback_count==0 gate fails)
//   SPEC: "Medium-risk skill blocked from promotion by rollback_count"
//
// Test 2: TestPromoter_Regression_MediumRisk_PromotesWhenZeroRollback
//   GIVEN same skill with rollback_count=0 (all other thresholds satisfied)
//   WHEN  Evaluate(snap)
//   THEN  PatchStatus called with status="validated"
//   SPEC: "Medium-risk skill promotes when rollback_count is zero"
//
// Test 3: TestPromoter_Regression_LowRisk_NotGatedOnRollbackCount
//   GIVEN candidate skill, risk_level=low, success_count=1, failure_count=0,
//         tests_passed_count=1, rollback_count=1
//   WHEN  Evaluate(snap)
//   THEN  PatchStatus called with status="validated" (low-risk path unaffected)
//   SPEC: "Low-risk skill is not gated on rollback_count"
//
// These tests should be GREEN immediately (regression contract, no new impl needed).
// If any go RED, the promoter guard has been accidentally removed — STOP and investigate.
//
// Run: GOWORK=off go test ./internal/application/consolidation/... -run TestPromoter_Regression
```

**GREEN** — no production code change. Tests pass on first run (regression lock behavior).

**VERIFY**:
```bash
GOWORK=off go test ./internal/application/consolidation/... -run TestPromoter
GOWORK=off go test ./...
```

**Commit**: `test(consolidation): regression lock for promoter rollback_count gate`

---

### PR2 Summary

| WU | Files touched | Spec scenarios covered | Sequential |
|----|--------------|------------------------|-----------|
| WU-4 | `demoter.go`, `demoter_test.go` | All demoter rollback scenarios + no-regression for existing paths | First |
| WU-5 | `promoter_test.go` only | All 3 promoter regression scenarios | After WU-4 |

---

## Full Task Ordering

```
WU-1 (orch, repo layer)
  └─ WU-2 (orch, wiring)
       └─ WU-3 (orch, emission + idempotency logic)  ← PR1 complete

[parallel branch — safe to develop concurrently with PR1, but merge after]

WU-4 (ME, demoter gate)
  └─ WU-5 (ME, promoter regression lock)            ← PR2 complete
```

WU-1 → WU-2 → WU-3 are strictly sequential (each builds on the previous).
WU-4 → WU-5 are sequential within PR2.
PR1 and PR2 development can proceed in parallel; production merge is PR1 first.

---

## Test Runner Reference

| Repo | Command |
|------|---------|
| sophia-orchestator | `GOWORK=off go test ./...` from repo root |
| sophia-memory-engine | `GOWORK=off go test ./...` from repo root |

---

## Conventional Commit Messages

| WU | Message |
|----|---------|
| WU-1 | `feat(application): add ExistsByRevertsRunID to ReevalAuditRepository for idempotency` |
| WU-2 | `feat(application): add MetricsPatcher interface to Reevaluator and wire via provider` |
| WU-3 | `feat(application): emit RollbackDelta=1 per reverted skill with idempotency guard` |
| WU-4 | `feat(consolidation): activate rollback_count >= 1 blocked branch in demoter` |
| WU-5 | `test(consolidation): regression lock for promoter rollback_count gate` |

No `Co-Authored-By`. No AI attribution.
