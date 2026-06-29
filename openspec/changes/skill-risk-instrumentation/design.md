# Design: skill-risk-instrumentation

## Technical Approach

Two targeted edits to close the rollback_count instrumentation gap: (1) `revertRun` in orch emits `RollbackDelta=1` per reverted skill via the existing `PatchMetrics` service call, guarded by a query-before-emit idempotency check on `RevertsRunID`; (2) `demoter.Evaluate` in ME adds `RollbackCount >= 1 → blocked` as the first branch inside the existing blocked-condition block. No new contracts, no migration, no new types.

## Architecture Decisions

### Decision: Idempotency via query-before-emit (Option 3 rejected: COUNT of distinct revert runs)

| Option | Tradeoff | Decision |
|--------|----------|----------|
| Query-before-emit: extend `ReevalAuditRepository` with `ExistsByRevertsRunID` | One extra SELECT per revert run; zero schema change; idempotency key is `reverts_run_id` already stored | **CHOSEN** |
| Flag/marker on revert audit row (new column `rollback_emitted bool`) | Schema change (migration 014); simpler logic | Rejected — violates "no migration" constraint |
| COUNT(DISTINCT revert runs) in ME computeDeltas | Wrong layer (D3: ME is consume-only); couples to consolidation timing | Rejected — ownership violation |

**Rationale**: the `reeval_run` table already has `reverts_run_id` (non-null on revert runs). A `ExistsByRevertsRunID(ctx, originalRunID)` query is a single indexed lookup on an already-present column. No migration, no new struct field. The check runs once per revert run invocation before any `PatchMetrics` call.

### Decision: Emit site — after `row.Reverted == true` inside `revertRun`, using Service.PatchMetrics directly

| Option | Tradeoff | Decision |
|--------|----------|----------|
| Call `s.patcher` which is `*Service` — but `patcher` is `StatusPatcher`, not `MetricsPatcher` | Would require widening the `Reevaluator` interface | Rejected |
| Add a `MetricsPatcher` port to `Reevaluator`, satisfied by `*Service.PatchMetrics` | Clean DI; follows existing StatusPatcher pattern exactly | **CHOSEN** |
| Call PATCH /metrics over HTTP from within revertRun | Cross-layer; orch calling itself over the network | Rejected |

**Rationale**: `Reevaluator` already accepts `StatusPatcher` as an interface. Adding a parallel `MetricsPatcher` interface (one method: `PatchMetrics`) follows the exact same pattern. `*Service` satisfies both. `NewReevaluatorWithAudit` gains one optional parameter; the dry-run constructor (`NewReevaluator`) does not need it since metrics emission only happens on confirmed reverts.

### Decision: Demoter branch placement — first check inside `shouldBlock` block, before failure-ratio

**Rationale**: `RollbackCount >= 1` is an unconditional block signal (D5). It should short-circuit before any ratio arithmetic. Inserting it at `demoter.go:43` (before the `failureRatio` computation) keeps blocked-condition logic co-located and respects the existing precedence rule ("blocked > deprecated"). No new precedence tier is introduced.

### Decision: Promoter — confirmed no change needed

`promoter.go:79` already gates on `snap.Metrics.RollbackCount > t.RollbackCount` where `t.RollbackCount = 0` for medium/high/critical. At zero this is `0 > 0 = false` — promotion proceeds. Once RollbackCount is non-zero, promotion is blocked by the existing guard. No edit required.

## Data Flow

```
reeval --revert (CLI)
  └─ runRevert (main.go:182)
       └─ Reevaluator.Revert / RevertLast
            └─ revertRun (reeval.go:299)
                 │
                 ├─ [idempotency check]
                 │   audit.ExistsByRevertsRunID(ctx, run.ID)
                 │   → if true: skip all PatchMetrics calls
                 │
                 ├─ for each item where row.Reverted == true:
                 │   metrics.PatchMetrics(ctx, item.SkillID, MetricsDelta{RollbackDelta:1})
                 │   → Service.PatchMetrics → skillRepo.PatchMetrics → pg UPDATE
                 │
                 └─ audit.Save(revert run with Mode="revert", RevertsRunID=run.ID)

[next consolidation cycle — ME]
  └─ demoter.Evaluate(snap)
       ├─ if snap.Status != "active" → false              (unchanged)
       ├─ if snap.Metrics.RollbackCount >= 1 → "blocked"  (NEW — demoter.go:43)
       ├─ if failureRatio > 0.15 → "blocked"              (unchanged, now second)
       └─ if AvgRetryReduction < 0.05 → "deprecated"      (unchanged)
```

## File Changes

### PR1 — sophia-orchestator

| File | Action | Description |
|------|--------|-------------|
| `internal/application/skill/reeval.go` | Modify | Add `MetricsPatcher` interface (1 method). Add `metricsPatcher` field to `Reevaluator`. Wire in `NewReevaluatorWithAudit`. Emit `RollbackDelta=1` per `row.Reverted` skill in `revertRun` after idempotency check. |
| `internal/ports/outbound/repository.go` | Modify | Add `ExistsByRevertsRunID(ctx, originalRunID string) (bool, error)` to `ReevalAuditRepository` interface. |
| `internal/adapters/outbound/pg/reeval_audit_repo.go` | Modify | Implement `ExistsByRevertsRunID`: `SELECT EXISTS(SELECT 1 FROM reeval_run WHERE reverts_run_id=$1)`. |
| `internal/application/skill/reeval_provider.go` | Modify | Pass `s` (which satisfies `MetricsPatcher`) when building `NewReevaluatorWithAudit`. |
| `internal/application/skill/reeval_revert_test.go` | Modify | Add TDD tests (see Testing Strategy). |
| `internal/adapters/outbound/pg/reeval_audit_repo_integration_test.go` | Modify | Add integration test for `ExistsByRevertsRunID`. |

### PR2 — sophia-memory-engine

| File | Action | Description |
|------|--------|-------------|
| `internal/application/consolidation/demoter.go` | Modify | Add `RollbackCount >= 1 → blocked` branch at line 43, before `failureRatio` computation. Update package-level comment to mark path as M3-reachable. |
| `internal/application/consolidation/demoter_test.go` | Modify | Add TDD tests (see Testing Strategy). |

## Interfaces / Contracts

```go
// PR1 — new interface in internal/application/skill/reeval.go
// MetricsPatcher increments a skill's additive metric counters.
// Satisfied by *Service.PatchMetrics — no new type required.
type MetricsPatcher interface {
    PatchMetrics(ctx context.Context, skillID string, delta inbound.MetricsDelta) error
}

// PR1 — new method on ReevalAuditRepository (internal/ports/outbound/repository.go)
// ExistsByRevertsRunID returns true when any revert run already names originalRunID
// as its reverts_run_id. Used to enforce idempotency in revertRun.
ExistsByRevertsRunID(ctx context.Context, originalRunID string) (bool, error)
```

No changes to the HTTP wire format. No new JSON fields. No migration.

## Exact Insertion Points

### orch — `revertRun` (reeval.go)

Current structure at `reeval.go:299`:
- Line 299: func declaration
- Lines 300–303: allocate result + revertItems slices, mint revRunID
- Lines 306–370: per-item loop (walk, audit item collection)
- Lines 372–383: save revert audit run, return

**Insert idempotency check at line 305** (after `revRunID` is minted, before the per-item loop):
```go
if r.audit != nil && r.metricsPatcher != nil {
    exists, err := r.audit.ExistsByRevertsRunID(ctx, run.ID)
    if err != nil {
        return result, fmt.Errorf("skill.Reevaluator.revertRun: idempotency check: %w", err)
    }
    if exists {
        // This revert run was already emitted. Still execute the status walks
        // (they are idempotent via the from==to no-op guard) but skip metric emission.
        skipMetrics = true
    }
}
```

**Insert metric emission at line 364** (inside the `row.Reverted = true` branch, after `result = append(result, row)`):
```go
if !skipMetrics && r.metricsPatcher != nil {
    if pErr := r.metricsPatcher.PatchMetrics(ctx, item.SkillID,
        inbound.MetricsDelta{RollbackDelta: 1}); pErr != nil {
        return result, fmt.Errorf("skill.Reevaluator.revertRun: patch metrics %s: %w",
            item.SkillID, pErr)
    }
}
```

### ME — `demoter.Evaluate` (demoter.go)

Current line 43 starts the `failureRatio` computation block. Insert before it:

```go
// RollbackCount >= 1 → immediate block (M3-reachable via skill-risk-instrumentation).
if snap.Metrics.RollbackCount >= 1 {
    return "blocked", true
}
```

This sits between `if snap.Status != "active"` guard (line 39) and the `usage` / `failureRatio` computation (current line 43). It short-circuits before any ratio math, consistent with blocked-takes-precedence semantics.

## Testing Strategy

### PR1 — sophia-orchestator (Strict TDD, runner: `GOWORK=off go test ./...`)

| Layer | RED test first | Assertion |
|-------|---------------|-----------|
| Unit | `fakeMetricsPatcher` captures calls; fake revert run with 2 reverted skills → assert `PatchMetrics` called twice with `RollbackDelta=1` each | Signal emission per reverted skill |
| Unit | Same revert run ID re-submitted → `fakeAuditRepo.ExistsByRevertsRunID` returns true → assert `PatchMetrics` NOT called | Idempotency holds |
| Unit | Revert run where skill is skipped (already at prior status) → assert NO `PatchMetrics` call for that skill | Attribution: only `row.Reverted==true` skills |
| Unit | `metricsPatcher` is nil (dry-run constructor) → revertRun completes without panic | Nil-safe path |
| Integration | `ExistsByRevertsRunID` on pg repo: save a revert run → returns true; query with unknown ID → returns false | Repo correctness |

### PR2 — sophia-memory-engine (Strict TDD, runner: `GOWORK=off go test ./...`)

| Layer | RED test first | Assertion |
|-------|---------------|-----------|
| Unit | active skill, `RollbackCount=1`, failure=0 → `Evaluate` returns `("blocked", true)` | RollbackCount>=1 fires |
| Unit | active skill, `RollbackCount=0`, failure=0, normal AvgRetryReduction → `Evaluate` returns `("", false)` | RollbackCount=0 does not trigger |
| Unit | active skill, `RollbackCount=2` → `("blocked", true)` | Counter threshold is >=1 |
| Unit | active skill, `RollbackCount=1` AND `failureRatio > 0.15` → `("blocked", true)` — both conditions met, still blocked | Precedence unchanged |
| Unit | existing `TestDemoter_FailureRatio`, `TestDemoter_LowRetryReduction_Deprecated`, `TestDemoter_BothConditions_BlockedTakesPrecedence` → all pass | No regression on existing paths |

## Line-Count Estimate

| Repo | Production lines | Test lines | Total |
|------|-----------------|-----------|-------|
| sophia-orchestator (PR1) | ~35 (interface + field + idempotency guard + emit + repo method) | ~60 (4 unit tests + 1 integration) | ~95 |
| sophia-memory-engine (PR2) | ~5 (3-line branch + comment update) | ~30 (3 new test cases) | ~35 |

Both PRs are well under the 400-line review budget. No `size:exception`.

## Migration / Rollout

No migration required. `RollbackCount` column exists in the skills table. `reverts_run_id` column exists in `reeval_run`. No new JSON fields on any HTTP contract.

Delivery sequence:
1. PR1 (orch): merges first. Counter starts accumulating on reverts. Demoter is blind — no regression.
2. PR2 (ME): merges second. Demoter activates the gate. Feature is live end-to-end.

**Each PR is inert alone** (PR1: counter populates but gate never fires; PR2: gate reads but always sees zero). Neither breaks anything in the intermediate state.

## Open Questions

None. All operator decisions (D1–D7) are locked in the proposal and reproduced above. No unresolved technical questions.
