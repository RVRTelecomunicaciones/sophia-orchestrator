# apply-error-handling-hardening — Proposal

Make today-invisible failures observable in the apply hot path and the pg adapter layer, without changing control flow or public API signatures.

---

## Problem statement

Three clusters of silently-discarded errors exist across the apply pipeline. Together they violate the documented design invariant **D1.2** ("Every phase produces a validated Envelope before any caller-visible state change") and the repo's explicit "Never do this" rule: "Persist after returning — every phase persists Envelope BEFORE caller-visible state change."

**What happens today:**

| Failure mode | Symptom |
|---|---|
| Domain state-machine transition refused (`group.Fail()`, `task.Complete()`, etc.) discarded with `_ =` | In-memory state diverges from persisted state; no audit trail; operator has zero signal |
| `BoardRepo.SaveGroup`, `SaveTask`, `SessionRepo.Save` errors discarded with `_ =` in apply hot path | Transient Postgres error silently loses board / task / session state; D1.2 violated |
| ULID parse errors discarded with `_ =` in pg repos | Corrupt/migrated column yields a zero-value domain ID that flows downstream silently, corrupting lookups and audit entries |

All three clusters are currently invisible to operators. No log line, no metric, no audit entry is produced.

---

## Scope

### In-scope

**Cluster 2 — domain-transition errors (~20 sites)**

Application layer: `_ =` discards on `RecordOutcome`, `group.Fail()`, `group.Complete()`, `task.Complete()`, `MarkRunning()`, `task.Release()`.

| File | Lines |
|---|---|
| `internal/application/apply/teamlead.go` | 152, 159, 161, 188, 318, 407, 460, 462, 515, 590, 592 |
| `internal/application/phase/service.go` | 585, 600, 612, 619 |

**Cluster 3 — repo Save errors (~18 sites)**

Apply hot path: `_ =` discards on `BoardRepo.SaveGroup`, `BoardRepo.SaveTask`, `SessionRepo.Save`, `SpawnGov.Release`.

| File | Lines |
|---|---|
| `internal/application/apply/teamlead.go` | 164, 168, 189, 271, 295, 319, 339, 461, 463, 591, 593 |
| `internal/application/apply/build_feedback.go` | 153, 179, 192 |
| `internal/application/phase/service.go` | 583, 586, 601, 613, 620, 1079, 1308 |

**Cluster 4 — ULID parse errors (~15 sites)**

Adapter layer: `_ =` discards inside `ids.Parse*()` calls during row scanning.

| File | Lines |
|---|---|
| `internal/adapters/outbound/pg/board_repo.go` | 106, 107, 139, 140, 143, 206, 207, 211, 252, 253, 257 |
| `internal/adapters/outbound/pg/session_repo.go` | 113, 114, 115, 118 |
| `internal/adapters/outbound/pg/worktree_repo.go` | 85, 88 |

### Out-of-scope (explicit non-goals)

- `phase/service.go:1291` `fallbackToMemory` bug (returns `[]byte("")` instead of `[]byte(rec.Content)`) — separate change, different risk profile.
- Unemitted Prometheus metrics gap (`obs/metrics.go`) — separate change, no behavior dependency.
- Half-wired skill-risk instrumentation (`rollback_count`, `deprecated_api_hits`) — already at explore stage as a separate SDD change.
- Public API signature changes.
- Database schema changes.

---

## Operator decisions (encoded — not open for re-discussion)

### Clusters 2 & 3 — application layer policy: log + audit, then continue

Every `_ =` discard site receives:

1. A structured `slog.Error(...)` call using the existing idiom already established in `phase/service.go:1338–1354` (keys: operation name, `change_id` or `phase_id`, `error`).
2. An audit-trail entry appended via the existing audit mechanism (design phase will confirm exact call — reuse, do not invent).
3. Control flow is **not changed**. The phase is not aborted. The goal is observability, not new failure modes.

Rationale: this is the lowest-risk first hardening step. Making failures observable is the prerequisite to deciding later whether any of them warrant escalation or abort logic.

### Cluster 4 — adapter layer policy: log + skip/surface the bad row, never emit a zero-value ID

Every `_ =` discard on a ULID parse inside a scan loop or single-row hydration receives:

1. A structured `slog.Error(...)` call identifying the repo, the column, and the raw value.
2. The row is **skipped** (scan loops) or the error is **returned to the caller** (single-row hydrators) — whichever the design phase determines is correct per call site.
3. A zero-value domain ID is **never passed downstream**.

The exact mechanics (return the scan error vs. skip-with-log) are deferred to the design phase, which will decide per call site.

---

## Approach

1. **Reuse existing logging idiom.** `slog.Error(op, slog.String("error", err.Error()), slog.String("change_id", ...))` is already established in `internal/application/phase/service.go:1338–1354`. No new logging infrastructure.
2. **Reuse existing audit mechanism.** The design phase will identify the exact audit-append call in the codebase and replicate it at each Cluster 2/3 site.
3. **Strict TDD discipline throughout.** Every site change lands RED → GREEN. Test runner: `go test ./...` from repo root.
4. **Conventional commits only.** Scope tags: `apply`, `phase`, `pg`.

---

## Risk and impact

| Dimension | Assessment |
|---|---|
| Runtime behavior change | Low. No control-flow changes in Clusters 2/3. Cluster 4 stops zero-value ID propagation, which is a correctness fix. |
| Hot-path touch surface | High. `teamlead.go` and `phase/service.go` are the apply orchestration core. Any mistake here can silently alter apply behavior. Mitigated by strict TDD. |
| Observability payoff | High. Operators gain structured ERROR logs for failures that are currently completely invisible. |
| Regression risk | Low-to-medium. The change adds log + audit calls around existing `_ =` sites; it does not add branches or early returns in Clusters 2/3. Cluster 4 adds early-return / skip-row logic, which is the highest-risk sub-cluster. |
| Line count | **Flag for delivery decision.** ~53 confirmed `_ =` sites across 7 files. Each site requires at minimum 3–5 lines (log call + optional audit entry). Estimated touched lines: **200–350+**. This approaches the 400-line PR threshold. The design phase should output a line-count estimate; the tasks phase will carry the delivery decision. Consider splitting Clusters 2+3 (application layer) from Cluster 4 (adapter layer) into separate PRs if the estimate exceeds 400 lines. |

---

## First-slice boundary recommendation

If a PR split is needed, the natural seam is the hexagonal boundary:

- **PR 1** — Clusters 2 + 3: application layer (`teamlead.go`, `build_feedback.go`, `phase/service.go`). All sites get log + audit, no control-flow change. Lower risk.
- **PR 2** — Cluster 4: adapter layer (`board_repo.go`, `session_repo.go`, `worktree_repo.go`). Introduces skip/return logic. Higher-precision per-site decisions, isolatable from the application layer.

The delivery decision is deferred to the tasks phase pending a concrete line-count forecast from the design phase.

---

## Success criteria

- Zero `_ = someCall()` at the 53 confirmed sites.
- Every previously-invisible error produces a structured `slog.Error` line in production.
- No existing test suite regressions (`go test ./...` green).
- New unit tests confirm log + audit behavior at representative sites per cluster.
- No zero-value domain ID emitted by any pg repo scan path.

---

## Next steps

- **sdd-spec**: document the behavioral contract for each cluster (log fields, audit entry shape, skip/return semantics for Cluster 4).
- **sdd-design**: enumerate every call site, confirm audit-append API, produce a line-count estimate, and finalize the PR split decision.

Both can proceed in parallel once this proposal is approved.
