# Tasks: loop-hardening

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | PR-A (orch outbox+relay) ~360 · PR-B (orch usage+reeval) ~240 · PR-C (ME filter+bench) ~190 |
| 400-line budget risk | Low (each PR independently within budget) |
| Chained PRs recommended | No |
| Suggested split | 3 independent PRs (PR-A, PR-B, PR-C) — no chain |
| Delivery strategy | ask-on-risk |
| Chain strategy | size-exception (n/a — no chain) |

Decision needed before apply: No
Chained PRs recommended: No
Chain strategy: pending
400-line budget risk: Low

### Suggested Work Units

| Unit | Goal | PR | Notes |
|------|------|----|-------|
| 1 | Outbox migration 012 + repo + txn INSERT + relay poller + wiring; delete fire-and-forget Notify path | PR-A (orch) | base = `main`; independent |
| 2 | Real `apply_attempts` in GET /usage + retroactive reeval CLI subcommand | PR-B (orch) | base = `main`; depends on PR-A only by merge order, not code — keep PR-A first to avoid wire.go conflicts |
| 3 | ME `filterDigestSkills` + golden regen + in-memory `HandlerV2.Handle` benchmark | PR-C (ME) | base = `main`; fully independent repo, any order |

## Resolved Open Questions (design §Open Questions)

- **D-LH-1 relay lifecycle (a)**: `Relay.Start(ctx)` is OWNED by `App.Run`/`Close`. Rationale: `wire.go:491-518` already launches the HTTP server in a `go func()` under the request ctx and stops it on `ctx.Done()`; the relay ticker follows the identical shape (`App` field, started in `Run`, ctx-cancel stops the ticker, `Close()` is the test teardown seam). No new lifecycle primitive.
- **D-LH-1 fire-and-forget (b)**: DELETE the `webhook.Notify` goroutine path entirely. Rationale: relay is now the only delivery path; retaining `Notify` is dead code (repo CLAUDE.md "no dead code"). KEEP the adapter's synchronous HTTP POST method as the transport the relay calls (exposed as `Deliver(ctx, payload) []byte→error`).
- **Reeval reversal path**: do NOT add a rollback command surface. Reversal = existing `PATCH /status` admin transition (6-enum `allowedTransitions`, already validated). Documented as the recovery path in T-B reeval help text; flag for verify (spec skill-retroactive-reevaluation "reverse a confirmed change").

---

## PR-A Task Groups (sophia-orchestator) — Webhook Outbox + Relay

**Branch:** `feat/webhook-outbox` (off `main`) · **Commit prefixes:** `feat(outbox)`, `feat(phase)`, `feat(bootstrap)`, `test(outbox)` · Independent PR. Specs: webhook-outbox, phase-archived-webhook.

### Group A — Migration 012 + outbox domain
Spec: webhook-outbox "Migration 012 — generic outbox table"

- [x] A.1 **RED**: Add migration round-trip test (existing migrate test harness) asserting `012` up creates `webhook_outbox` (all columns, status CHECK `IN ('pending','delivered')`, partial index on `(next_attempt_at) WHERE status='pending'`) and `012` down drops table+index with no residue. Run; confirm FAIL.
- [x] A.2 **GREEN**: Create `migrations/postgres/012_outbox.up.sql` / `.down.sql` per spec (CHAR(26) ULID PK — DEVIATION from spec UUID, see decision obs 883; no dead-letter/expiry columns). Create `internal/domain/outbox/` (`ids.OutboxID` CHAR(26), `Event{ID,EventType,Payload,Status,Attempts,NextAttemptAt,CreatedAt,DeliveredAt}`, status enum). Green (domain unit tests pass).
- [x] A.3 **VERIFY**: `make test-integration` (migration suite) + `make lint`. — GREEN. `TestMigration012_RoundTrip` passes with -race (exit 0); lint 0 issues. (Pre-existing unrelated `TestMigration011_RoundTrip` down-migration leak documented, out of scope.)

### Group B — Outbox repo (txn-bound enqueue + SKIP LOCKED claim)
Spec: webhook-outbox "INSERT shares txn", "Relay claims with FOR UPDATE SKIP LOCKED"

- [x] B.1 **RED**: In `internal/adapters/outbound/pg/outbox_repo_integration_test.go` (testcontainers): `EnqueueTx` inserts inside a caller tx (rollback → no row); `ClaimDue` returns only `status='pending' AND next_attempt_at<=now()`, uses `FOR UPDATE SKIP LOCKED` (two concurrent claimers never double-claim); `MarkDelivered` sets status+`delivered_at`; `Reschedule` bumps `attempts`+`next_attempt_at`; `PendingCount` counts pending. Written (compiles under integration tag).
- [x] B.2 **GREEN**: Create `internal/adapters/outbound/pg/outbox_repo.go` implementing the `OutboxRepository` port (`EnqueueTx`, `ClaimDue` with `LIMIT` + `FOR UPDATE SKIP LOCKED`, `MarkDelivered`, `Reschedule`, `PendingCount`) + `SaveCompletedWithOutbox` (completion-txn enqueuer). Green (builds; vet integration-tag clean).
- [x] B.3 **VERIFY**: `make test-integration` + `make lint`. — GREEN. All `TestOutboxRepo_*` integration tests pass with -race (EnqueueTx rollback, ClaimDue due-only + SKIP-LOCKED no-double-claim, MarkDelivered, Reschedule, PendingCount); exit 0; lint 0 issues.

### Group C — Backoff helper + relay poller
Spec: webhook-outbox "at-least-once with capped backoff", phase-archived-webhook "retried not dropped"

- [x] C.1 **RED (unit)**: In `internal/application/outbox/relay_test.go` table-driven: `Backoff(attempts)` = `min(base*2^attempts, 5m)` with base 10s; ceiling clamps near 5min. RED confirmed (compile fail), then GREEN.
- [x] C.2 **RED (unit)**: Relay one-tick test with fake `Repository` + fake `Deliver` func + injected `Clock`: 2xx → `MarkDelivered`; err → `Reschedule(attempts+1, now+backoff)`, WARN logged, row stays pending; empty claim → no-op; Start stops on ctx cancel. RED then GREEN.
- [x] C.3 **GREEN**: Create `internal/application/outbox/relay.go`: `Relay` + `Deps{Repo, Deliver func(ctx,[]byte) error, Clock, Interval, BatchLimit}` + `Start(ctx)` ticker loop (default 5s, `LIMIT 50`) and `Tick(ctx)`. Green.
- [x] C.4 **VERIFY**: `make test-unit` (-race) + `make lint`. — both GREEN, exit 0.

### Group D — Webhook adapter transport + txn INSERT seam (delete fire-and-forget)
Spec: phase-archived-webhook "payload byte-identical", webhook-outbox "INSERT shares completion txn"

- [x] D.1 **RED**: New `internal/application/phase/service_outbox_test.go` with fake `OutboxEnqueuer` + tracking notifier: reaching archive calls `SaveCompletedWithOutbox` exactly once, `event_type='phase.archived'`, payload byte-identical to `PhaseArchivedPayload`, status pending/attempts 0; legacy `Notify` never called. RED confirmed (unknown field), then GREEN.
- [x] D.2 **GREEN**: In `webhook/adapter.go` exposed synchronous `Deliver(ctx, payload []byte) error` (timeout default 5s, X-API-Key header); DELETED the `Notify` goroutine + `post` + `phase_bridge.go`. In `phase/service.go` `advanceChange` replaced `WebhookNotifier.Notify` with `OutboxEnqueuer.SaveCompletedWithOutbox` (change-save + outbox INSERT in one txn). Green.
- [x] D.3 **VERIFY**: `make test-unit` + `make lint`. — both GREEN, exit 0.

### Group E — Wiring + integration
Spec: webhook-outbox "orch restart resumes", "duplicate absorbed downstream"

- [x] E.1 **GREEN**: In `internal/bootstrap/wire.go`: construct outbox repo + relay (deliver = `webhook.Adapter.Deliver`); store relay on `App`; launch `relay.Start(ctx)` in `App.Run` alongside the HTTP server `go func()`; config key `Outbox.PollInterval` (env `SOPHIA_OUTBOX_POLL_INTERVAL`) default 5s. Injected `OutboxEnqueuer` into `phase.Deps`. `go build ./...` GREEN; bootstrap+config unit tests GREEN.
- [x] E.2 **RED→GREEN (integration)**: testcontainers PG + `httptest`: ME down → row stays pending, attempts increments; ME back up + relay tick → delivered. — GREEN. Wrote `outbox_relay_integration_test.go` (`TestOutboxRelay_EndToEnd_MEDownThenUp`): real `pg.OutboxRepo` + real `webhook.Adapter` → togglable httptest fake ME + mutable clock; ME-down tick → 503 → row stays pending, attempts→1, next_attempt_at pushed by backoff, not re-claimed before due; ME-up + clock advanced past backoff → delivered byte-identically, delivered_at stamped, PendingCount→0. RED proven via ME-stays-down negative control (delivery assertion fails). Channel/clock sync, no sleeps, -race clean. **DEVIATION (bugfix):** test surfaced that `payload JSONB` normalized whitespace/key-order, breaking the spec's byte-identical contract — changed migration 012 `payload` to `BYTEA` (opaque blob carrier; no Go changes; migration_012_test literals updated to `bytea`).
- [x] E.3 **VERIFY (CHECKPOINT)**: `make test-unit` (-race) exit 0 GREEN; `make lint` exit 0 (0 issues); `make test-integration` — full `test/...` + `pg` outbox/migration-012/relay suite GREEN with -race, exit 0 (skipping pre-existing unrelated `TestMigration011_RoundTrip` base-branch failure, which is out of PR-A scope).
- [ ] E.4 **COMMIT+PR**: work-unit commits done locally (see below). PR NOT opened (orchestrator reviews fresh-context, then pushes).

---

## PR-B Task Groups (sophia-orchestator) — Real ApplyAttempts + Reeval CLI

**Branch:** `feat/skill-reeval` (off `main`) · **Commit prefixes:** `feat(skill)`, `feat(pg)`, `feat(cmd)`, `test(skill)` · Independent code; merge AFTER PR-A only to avoid `wire.go` conflict. Specs: skill-usage-tracking, skill-retroactive-reevaluation.

### Group F — SumApplyAttemptsByChange query
Spec: skill-usage-tracking "apply_attempts reflects real tasks.attempts"

- [x] F.1 **RED (integration)**: In `internal/adapters/outbound/pg/skill_repo_integration_test.go` (or tasks repo): seed `tasks.attempts` linked `tasks→groups→apply_boards→phases→change`; `SumApplyAttemptsByChange(changeID)` returns `SUM(tasks.attempts)`; no rows → 0. Confirm FAIL. — RED via compile failure (method undefined).
- [x] F.2 **GREEN**: Add `SumApplyAttemptsByChange(ctx, changeID)` with the design D-LH-2 SQL (`SUM` joined to `change_id`, `COALESCE(...,0)`). Green. — Added to `SkillUsageRepository` port + `SkillUsageRepo`; fakes in apply/phase tests stubbed.
- [x] F.3 **VERIFY**: `make test-integration` + `make lint`. — GREEN. `TestSkillUsageRepo_SumApplyAttemptsByChange{,_NoRows}` pass with -race (exit 0); lint 0 issues.

### Group G — GetUsage enrichment
Spec: skill-usage-tracking "apply_attempts reflects real tasks.attempts", "filters", "401"

- [x] G.1 **RED**: In `skill/service_usage_test.go` with fake repo returning a per-change attempts sum: `GetUsage` sets each `SkillUsageRow.ApplyAttempts` to the real per-change sum (not 0); JSON shape unchanged. Confirm FAIL. — RED: assertion expected 4, got 0.
- [x] G.2 **GREEN**: In `skill/service.go` replace `ApplyAttempts: 0` with the per-change sum from F.2 (one `SumApplyAttemptsByChange` call) applied to every row of that change. Green.
- [x] G.3 **VERIFY**: `make test-unit` + `make lint`. — GREEN. Full `make test-unit` exit 0; lint 0 issues.

### Group H — Reeval recompute + verdict
Spec: skill-retroactive-reevaluation "metric recomputed", "dry-run reports deltas"

- [x] H.1 **RED**: Create `internal/application/skill/reeval_test.go` table-driven: recompute `avg_retry_reduction=(1.5-applyAttempts)/1.5` from real attempts; promoter `>=0.20` / demoter `<0.05` verdict; report row carries current status, projected status, old/new metric + attempts basis; no-op state → zero projected changes; recomputed basis matches enriched GetUsage. Confirm FAIL. — RED via undefined symbols, then assertion mismatch (verdict).
- [x] H.2 **GREEN**: Create `internal/application/skill/reeval.go` — `DryRun` builds the per-skill report (recompute + actionable gate verdict + proposed transition); pure, no mutation. Plus `reeval_provider.go` (`RepoEvidenceProvider` reads skills+skill_usage, derives max per-change apply_attempts; `Service.Reevaluator()` wires it). Green.
- [x] H.3 **VERIFY**: `make test-unit` + `make lint`. — GREEN. Full unit exit 0; lint 0 issues.

### Group I — Reeval apply (confirm-gated, transition-validated)
Spec: skill-retroactive-reevaluation "apply mutates only on confirmation"

- [x] I.1 **RED**: In `reeval_test.go`: `Apply` mutates ONLY gated skills via `Service.PatchStatus`; default (no confirm) never mutates; forbidden transition → fake returns `ErrForbiddenStatusTransition` → reported skipped, never forced; reversal path is `PatchStatus` (documented), no rollback surface added. Confirm FAIL. — RED via undefined symbols.
- [x] I.2 **GREEN**: Add `Apply(confirm bool)` to `reeval.go` reusing `Service.PatchStatus` (6-enum `allowedTransitions` guard unchanged); confirm=false → dry-run; skip+report forbidden transitions. Green.
- [x] I.3 **VERIFY**: `make test-unit` + `make lint`. — GREEN. Full unit exit 0; lint 0 issues.

### Group J — CLI subcommand + wiring
Spec: skill-retroactive-reevaluation "default invocation never mutates"

- [x] J.1 **RED**: In `cmd/sophia-orchestator` test: arg dispatch — `reeval` (no flag / `--dry-run`) runs dry-run, exits 0, no mutation; `reeval --apply` without `--confirm` → dry-run; `reeval --apply --confirm` calls `Apply(true)`; non-`reeval` args → existing `run()`. Confirm FAIL. — RED via undefined `dispatch`.
- [x] J.2 **GREEN**: In `cmd/sophia-orchestator/main.go` add subcommand dispatch before `run()` (pure `dispatch(ctx,args,runner)` with injected runner for testability); reuse `bootstrap.Wire` → `App.SkillService()` → `Service.Reevaluator()`; help text + report footer document reversal via admin `PATCH /status`. `App` now stores+exposes `skillSvc`; `main` refactored to `mainWithExit` (no os.Exit-after-defer). Green.
- [x] J.3 **VERIFY (CHECKPOINT)**: `make test-unit` (-race) + `make test-integration` + `make lint` all green. — GREEN. Unit exit 0; integration exit 0 (test/contract, test/integration, pg suite incl. new SumApplyAttemptsByChange, all -race); lint 0 issues.
- [~] J.4 **COMMIT**: work-unit commits done locally (`feat(pg): sum apply attempts by change`, `feat(skill): enrich usage with real apply attempts`, `feat(skill): retroactive reevaluation dry-run and confirm-gated apply`, `feat(cmd): reeval subcommand`). PR NOT opened (orchestrator reviews fresh-context, then pushes).

---

## PR-C Task Groups (sophia-memory-engine) — Digest Filter + Benchmark

**Branch:** `feat/digest-filter-benchmark` (off `main`) · **Commit prefixes:** `feat(consolidation)`, `test(consolidation)` · Fully independent repo — any order. Specs: change-digest-deterministic, consolidation-pipeline-benchmark.

### Group K — Pure filterDigestSkills
Spec: change-digest-deterministic "Unknown-outcome dropped", "Never-applied retained"

- [x] K.1 **RED**: Create `internal/application/consolidation/digest_filter_test.go` table-driven: `filterDigestSkills` drops ONLY `Outcome=="unknown"`; `success`/`failure`/never-applied (non-unknown) retained; order preserved; empty/all-unknown handled. Confirm FAIL.
- [x] K.2 **GREEN**: Create `internal/application/consolidation/digest_filter.go` — pure `filterDigestSkills([]DigestSkill) []DigestSkill`. Green.
- [x] K.3 **GREEN (wire)**: In `handler.go:231` apply `filterDigestSkills(digestSkills)` immediately BEFORE `BuildDigest`. `BuildDigest` untouched (stays deterministic).
- [x] K.4 **VERIFY**: `make test` + `make lint`. — GREEN. K.1 RED confirmed (build failed: `undefined: consolidation.FilterDigestSkills`). Exported as `FilterDigestSkills` (cross-package test). Wired in `handler.go` step 9 before `BuildDigest`. `make test` exit 0; consolidation `-race` exit 0; `make lint` 0 issues.

### Group L — Golden fixture regen
Spec: change-digest-deterministic "Digest YAML matches updated golden fixture"

- [x] L.1 **RED**: Add/extend golden test asserting digest YAML for a known envelope (incl. one `unknown` skill) matches `testdata/digest_golden.yaml` and OMITS the unknown entry; phases sorted by phase_type, skills sorted by skill_id. Confirm FAIL against the stale fixture.
- [x] L.2 **GREEN (regen)**: Regenerate `testdata/digest_golden.yaml` deterministically (documented one-time `-update` run) without `unknown` entries; commit the regenerated fixture in the SAME commit as K.2/K.3. Green.
- [x] L.3 **VERIFY**: `make test` + `make lint`; confirm byte-stability across two runs. — GREEN. Golden test `TestBuildDigest_GoldenFixture_OmitsUnknown` runs an envelope with one `unknown` skill through `FilterDigestSkills`+`BuildDigest`, asserts the unknown entry is absent and YAML matches `testdata/digest_golden.yaml` byte-for-byte. DEVIATION (benign): the pre-existing golden already contained exactly the 3 non-unknown skills, so regen (gated by `UPDATE_GOLDEN=1`) produced a byte-identical fixture — no fixture diff to commit. Determinism proven: two `UPDATE_GOLDEN=1` runs are identical. `make test`/`make lint` exit 0.

### Group M — In-memory HandlerV2.Handle benchmark
Spec: consolidation-pipeline-benchmark "in-memory benchmark", "test-only isolated"

- [x] M.1 **RED→GREEN**: Create `internal/application/consolidation/handler_bench_test.go`: `BenchmarkHandlerV2_Handle` (NO `integration` build tag → default path, no Docker) on in-memory fakes; `b.ReportAllocs()` + `b.ResetTimer()`; `b.Run("rows=N")` for 1/10/100/1000 `skill_usage` rows. Each sub-bench produces distinct ns/op + allocs/op. Test-only. — GREEN. DEVIATION (benign): the consolidation package has no `fakeOrchServer` (that is an integration-test helper elsewhere); used the package's existing in-memory fake pattern instead — added bench-local `benchMemoryClient` + `benchSkillsClient` (configurable N rows) matching `MemoryClient`/`SkillsClient` ports. No production code touched.
- [x] M.2 **VERIFY (CHECKPOINT)**: `go test -bench=BenchmarkHandlerV2_Handle -benchmem ./internal/application/consolidation/` runs without Docker; `make test` + `make lint` green. — GREEN. Bench exit 0 (benchtime=200x): rows=1 11120 ns/op 83 allocs/op · rows=10 30584 ns/op 231 allocs/op · rows=100 228725 ns/op 1599 allocs/op · rows=1000 2821244 ns/op 15135 allocs/op (allocs scale linearly → per-skill loop attributable). `make test` exit 0, consolidation `-race` exit 0, `make lint` 0 issues. Isolation: file is `_test.go` (excluded from production build); `go build ./...` exit 0.
- [x] M.3 **COMMIT**: work-unit commits done locally — `540d090 feat(consolidation): drop unknown-outcome skills from digest`, `c7862c3 test(consolidation): in-memory handler benchmark with row scaling` on branch `feat/digest-filter-benchmark`. PR NOT opened (orchestrator reviews fresh-context, then pushes).

---

## Dependency Notes

- **PR-A, PR-B, PR-C are independent** (no shared contract; ME and orch never co-change). Implement in any order.
- Soft order **PR-A before PR-B** within orch: both touch `wire.go`; sequencing avoids a merge conflict, not a code dependency.
- PR-C (ME repo) is fully parallel to all orch work.
- No chaining: each PR is < 400 lines and lands independently to `main`.

## Strict TDD + Verification Gates

- Every group is RED → GREEN → VERIFY; test named before implementation. No GREEN before its RED test FAILS (exception: A.2 SQL files paired with A.1 migration test; M.1 bench is test-only).
- **orch (PR-A, PR-B)**: `make test-unit` (-race), `make test-integration` (testcontainers PG), `make lint` (forbidigo/wrapcheck/errorlint) clean at each CHECKPOINT.
- **ME (PR-C)**: `make test`, `make lint`, golden fixture regen is a deliberate one-time `-update` with a reviewed diff.
- No `time.Now()`/`ulid.Make()` in application/domain — injected `Clock`/`IDGenerator` (relay clock, outbox IDs).
- Conventional commits; NEVER Co-Authored-By / AI attribution; tests+code in the same work-unit commit.

## Out of Scope — apply MUST NOT touch

- ME receiver `/worker/phase-archived` — payload byte-identical, zero ME-side change.
- Dead-letter / expiry columns on outbox (spec forbids).
- Per-skill `apply_attempts` (no `skill_id` on tasks — out of scope, per-change only).
- Admin HTTP reeval endpoint (CLI subcommand only).
- Rollback command surface (reversal = existing `PATCH /status`).
- Any DB migration in ME; any JSON contract change in either repo.
