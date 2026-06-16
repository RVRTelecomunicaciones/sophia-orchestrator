# Verification Report: loop-hardening

- **Change**: loop-hardening (Sophia ecosystem — sophia-orchestator + sophia-memory-engine)
- **Mode**: openspec (file) + engram (`sdd/loop-hardening/verify-report`)
- **Verified at tips**: orch `98e3430` (main, PR #97 + #98 merged) · ME `74fc5b7` (main, PR #19 merged)
- **Strict TDD**: active. RED→GREEN→VERIFY discipline checked per group.
- **Overall verdict**: PASS_WITH_WARNINGS
- **Ready for sdd-archive**: YES
- **Counts**: CRITICAL 0 · WARNING 2 · SUGGESTION 2

## Completeness

| PR | Groups | State |
|----|--------|-------|
| PR-A (orch outbox+relay) | A–E | All impl tasks checked. E.4 = COMMIT+PR (satisfied: PR #97 merged) |
| PR-B (orch apply_attempts+reeval) | F–J | All impl tasks checked. J.4 = COMMIT (satisfied: PR #98 merged) |
| PR-C (ME digest filter+benchmark) | K–M | All impl tasks checked. M.3 COMMIT satisfied: PR #19 merged |

The only unchecked/`[~]` task boxes (E.4, J.4) are commit/PR boundary tasks the orchestrator owns; all three PRs are merged to main, so no implementation task is incomplete. No CRITICAL for unchecked tasks.

## Spec Compliance Matrix

### webhook-outbox — PASS
- Migration 012 generic table: all columns + status CHECK('pending','delivered') + partial index on `(next_attempt_at) WHERE status='pending'`; down drops table+index. Evidence: `migrations/postgres/012_outbox.up.sql:32-46`, `.down.sql:1-5`. Test: `TestMigration012_RoundTrip/PreState/PostUp` PASS (-race).
- INSERT shares completion txn: `internal/adapters/outbound/pg/outbox_repo.go:37-65` (`SaveCompletedWithOutbox` upsert+EnqueueTx in one tx); seam wired at `internal/application/phase/service.go:1265-1289`. Test: `TestOutboxRepo_EnqueueTx_RollbackLeavesNoRow` PASS (rollback → no row).
- Relay at-least-once + capped backoff: `internal/application/outbox/relay.go:32-49` (Backoff `min(base*2^attempts,5m)`, base 10s, clamp), `:135-185` (Tick: 2xx→MarkDelivered, err→Reschedule+WARN, row stays pending). Tests: relay unit + `TestOutboxRelay_EndToEnd_MEDownThenUp` PASS.
- SKIP LOCKED no double-claim: `outbox_repo.go:93-100` (`FOR UPDATE SKIP LOCKED`, due-only filter). Test: `TestOutboxRepo_ClaimDue_SkipLockedNoDoubleClaim`, `_OnlyDuePending` PASS.
- Restart resumes / duplicate absorbed: relay started in `wire.go` Run (`:536-540`), awaited before pool teardown (`:548-551`); duplicate absorbed by ME HasTopic (out of orch scope, covered by ME idempotency tests). Covered.

### phase-archived-webhook — PASS
- Payload byte-identical: outbox stores the marshaled `inbound.PhaseArchivedPayload` body verbatim (`service.go:1254-1282`); relay delivers via `webhook.Adapter.Deliver` with `bytes.NewReader(payload)` (`adapter.go:69-96`) — no re-serialization. BYTEA column preserves bytes. Test: `TestOutboxRelay_EndToEnd_MEDownThenUp` asserts received bytes == enqueued payload.
- Retried not dropped + WARN + no caller error: `relay.go:154-170` (WARN log, Reschedule, row stays pending; Tick swallows). Adapter timeout default 5s + X-API-Key (`adapter.go:42-43,79-81`). PASS.
- Fire-and-forget removed: `phase_bridge.go` deleted; active path uses `OutboxEnqueuer`; legacy `Notify` retained only as nil-tolerant deprecated field, never invoked in production path (`service.go:139-151`). PASS.

### change-digest-deterministic — PASS
- Unknown dropped, never-applied retained, order preserved: `internal/application/consolidation/digest_filter.go:13-22` (`FilterDigestSkills`, pure, drops only `OutcomeUnknown`). Wired before BuildDigest at `handler.go:240`. Tests: `TestFilterDigestSkills` (all 6 cases) + `_DoesNotMutateInput` PASS (-race).
- YAML matches updated golden, deterministic, sorts: `TestBuildDigest_GoldenFixture_OmitsUnknown` + `TestBuildDigest_DeterministicYAML` PASS. Golden byte-stable across two UPDATE_GOLDEN runs.

### skill-usage-tracking — PARTIAL (see deviation #4 WARNING)
- apply_attempts real, not 0: `internal/application/skill/service.go:115-124` applies `SumApplyAttemptsByChange` to every row. SQL `internal/adapters/outbound/pg/skill_usage_repo.go:86-100` matches D-LH-2 exactly. Test: `TestSkillUsageRepo_SumApplyAttemptsByChange` (SUM=5) + `_NoRows` (0) PASS.
- Filters (change_id/skill_id) + 401: pre-existing endpoint behavior unchanged; JSON shape identical. PASS for the modified field.
- Per-skill granularity not achievable (tasks has no skill_id) → per-change sum applied uniformly. Documented limitation (deviation #4 below).

### skill-retroactive-reevaluation — PARTIAL (see deviation #1 WARNING)
- Dry-run reports deltas, no mutation: `reeval.go:133-137,108-131` (current/proposed status, old/new metric, attempts basis). Tests in `reeval_test.go` PASS.
- Apply confirm-gated, forbidden skipped: `reeval.go:142-168` (confirm=false→dry-run; PatchStatus reuse; `ErrForbiddenStatusTransition`→Skipped). CLI: `cmd/.../main.go:60-95` (`--dry-run` wins, `--apply --confirm` mutates). PASS.
- Metric recompute `(1.5-attempts)/1.5`: `reeval.go:86-88` matches GetUsage basis. PASS.
- **Reverse a confirmed change (spec MUST, line 29)**: NO reverse command surface; reversal delegated to admin PATCH /status multi-hop chain, documented in CLI help+footer (`main.go:74-81,159-161`). See deviation #1.

### consolidation-pipeline-benchmark — PASS
- In-memory, no Docker, ReportAllocs+ResetTimer, row-scaling: `handler_bench_test.go:71-92` (no integration tag; `b.Run("rows=N")` 1/10/100/1000). Bench ran without Docker: rows=1 11561 ns/op 83 allocs · 1000 2969750 ns/op 15135 allocs (linear → per-skill loop attributable).
- Test-only isolated: `go build ./...` exit 0 (file is `_test.go`, excluded from prod). PASS.

## Flagged Deviation Verdicts

### 1. Reeval reversal delegated to PATCH /status — **WARNING**
Spec skill-retroactive-reevaluation:29 says "The same command MUST be able to reverse a confirmed change (rollback path)". The implementation adds NO reverse surface to the `reeval` command; reversal is the existing admin `PATCH /status` multi-hop chain (deprecated→blocked→candidate→validated→active), documented in help text + report footer. Rationale: this is a real, validated, operator-usable recovery path (the 6-enum allowedTransitions guard), and adding a bespoke rollback surface was an explicit design decision (tasks "Resolved Open Questions", obs #882 NIT-2 deferred). However, it does NOT satisfy the literal spec MUST ("the same command"): an operator cannot undo a mass shift with one reeval invocation. This is a genuine spec-vs-impl gap, not a no-op. Severity WARNING (not CRITICAL) because a working reversal path exists and is documented; it does not break any passing scenario. **Action: follow-up** — either add a `reeval --revert` surface or amend the spec MUST to accept the PATCH-chain delegation. The spec text should be reconciled before archive-as-spec-of-record.

### 2. Outbox PK CHAR(26) ULID vs spec UUID — **ACCEPT (spec drift to reconcile, SUGGESTION severity)**
Spec webhook-outbox:11 says `id (UUID PK)`; impl uses `CHAR(26)` ULID with injectable IDGenerator (`012_outbox.up.sql:33`). Decision obs #883: every existing migration (009, 011) uses CHAR(26) ULID PKs, and repo CLAUDE.md (rule 5) forbids `ulid.Make()`/`time.Now()` in domain/application. The spec text was written without inheriting the repo's PK convention. Repo convention correctly wins for consistency and the injectable-ID law. Functionally equivalent (opaque unique PK). **Verdict ACCEPT** — implementation is correct; this is spec text that should be corrected to CHAR(26) ULID. Logged as SUGGESTION (reconcile spec wording at archive).

### 3. Outbox payload BYTEA vs spec JSONB — **ACCEPT (spec drift to reconcile, SUGGESTION severity)**
Spec webhook-outbox:11 says `payload (jsonb NOT NULL)`; impl uses `BYTEA` (`012_outbox.up.sql:35`). Decision obs #885: JSONB normalizes whitespace and reorders object keys on storage, which broke the byte-identical delivery contract that phase-archived-webhook:18-24 explicitly asserts (and `TestOutboxRelay_EndToEnd_MEDownThenUp` caught). A transactional outbox is a generic opaque-blob carrier; a typed/normalizing column is the wrong tool for a verbatim-delivery payload. **Verdict ACCEPT** — BYTEA is the CORRECT choice and is mandated by the byte-identity spec; the JSONB wording is internally inconsistent with phase-archived-webhook. Logged as SUGGESTION (reconcile spec wording at archive). Note: this deviation resolves a conflict BETWEEN two specs in favor of the stronger (byte-identity) requirement.

### 4. ApplyAttempts per-change SUM applied to all skills — **ACCEPT as documented limitation (WARNING severity)**
Spec skill-usage-tracking:13 says `apply_attempts` MUST be "the real tasks.attempts basis for that change". Impl computes `SUM(tasks.attempts)` per change and applies it uniformly to every SkillUsageRow of that change (`service.go:115-124`); the ME consumer takes `max(ApplyAttempts)` per skill across changes. Design D-LH-2: `tasks` link to a change only through group→board→phase→change_id; there is NO `skill_id` on tasks, so per-skill attribution is impossible without a schema change (explicit Non-Goal / Out of Scope). Per-change is the finest honest granularity and the field is no longer the fake constant 0, so the previously-dead promoter/demoter gates now activate on real data. The spec scenario "apply_attempts equals the real tasks.attempts basis for that change" IS satisfied (it asks for the per-change basis, not per-skill). **Verdict ACCEPT as a documented limitation** — WARNING severity because the per-change-applied-to-all-skills semantics is a coarser-than-ideal signal that should be a tracked follow-up (per-skill attribution needs a `skill_id` on tasks or a usage-join). Does not break any spec scenario.

## Test Evidence (commands run, exit codes — NOT CI-only)
| Suite | Command | Exit |
|-------|---------|------|
| orch unit (-race) | `make test-unit` | 0 |
| orch integration (testcontainers PG) | `make test-integration` | 0 (test/contract, test/integration, pg suite) |
| orch targeted pg | `go test -race -tags=integration -run 'TestOutbox\|TestMigration012\|TestSkillUsageRepo_SumApplyAttemptsByChange\|TestOutboxRelay' -v ./internal/adapters/outbound/pg/` | 0 — 10 named tests PASS |
| orch lint | `make lint` | 0 (0 issues) |
| ME suite | `make test` | 0 |
| ME consolidation (-race) | `go test -race -run 'Digest\|Filter' -v ./internal/application/consolidation/` | 0 |
| ME benchmark (no Docker) | `go test -bench=BenchmarkHandlerV2_Handle -benchmem -benchtime=50x -run='^$' ./internal/application/consolidation/` | 0 |
| ME build isolation | `go build ./...` | 0 (bench is _test.go) |
| ME lint | `make lint` | 0 (0 issues) |

All suites re-run locally (Docker up); NOT relying on CI-green.

## Operator-Invariant Checks
- **Conventional commits**: PASS. All 15 orch + 3 ME loop-hardening commits use `feat(scope)`/`fix(scope)`/`test(scope)`/`docs(scope)`/`refactor(scope)`.
- **No Co-Authored-By / AI attribution**: PASS. Scanned `87b9eae^..98e3430` (orch) and `540d090^..74fc5b7` (ME) bodies for co-authored/claude/anthropic/generated/opus/sonnet — NONE FOUND.
- **Strict-TDD RED-first structure**: PASS. Every feat commit pairs impl + test in the same work-unit (e.g. 6439efe outbox_repo.go + integration_test.go; d6e3cbd relay.go + relay_test.go; dc4675b reeval.go + reeval_test.go). Apply-progress (obs #882, #905) documents RED-confirmed-before-GREEN per group.
- **No time.Now()/ulid.Make() in domain/application**: PASS. Relay uses injected `shared.Clock`; outbox IDs via injected `IDGen.NewID()` (`service.go:1274`); lint forbidigo clean.

## Issues
- CRITICAL: none.
- WARNING (2): [1] reeval has no reverse command surface — literal spec MUST delegated to PATCH /status (reconcile spec or add `--revert`). [4] apply_attempts per-change granularity applied to all skills — documented limitation, per-skill needs schema follow-up.
- SUGGESTION (2): [2] reconcile webhook-outbox spec PK wording UUID→CHAR(26) ULID. [3] reconcile webhook-outbox spec payload wording JSONB→BYTEA (consistency with byte-identity spec).

## Verdict
**PASS_WITH_WARNINGS** — all 6 capabilities implemented and proven by re-run tests; 0 CRITICAL. Two WARNINGs are tracked follow-ups (reeval reversal MUST, per-skill attempts attribution); two SUGGESTIONs are benign spec-text reconciliations where the implementation is correct. **Ready for sdd-archive: YES** (archive should record the 2 follow-ups and reconcile the 2 spec-text drifts).
