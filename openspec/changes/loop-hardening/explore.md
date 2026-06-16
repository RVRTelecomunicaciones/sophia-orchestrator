# Exploration: loop-hardening (V4.1 learning loop — 5 backlog items)

> SDD explore artifact. Engram topic: `sdd/loop-hardening/explore` (obs #877, project 2026).
> Source: M2 consolidation-worker verify suggestions + live loop verification follow-ups.

## Current State (cross-cutting)

- Webhook is pure fire-and-forget: `internal/adapters/outbound/webhook/adapter.go` (goroutine, `context.Background`, X-API-Key, NO retry, empty-URL disabled). Wired in `internal/application/phase/service.go:1227-1234` inside `advanceChange`, only when `completed == phase.PhaseArchive`, after `publishEvent(EventPhaseArchived)`.
- A durable event log already exists — `migrations/postgres/006_phase_events.up.sql` — but it is the **SSE replay buffer**, NOT a delivery outbox. Highest migration is `011`. **No outbox/jobs/queue table exists.**
- ME pipeline: `internal/application/consolidation/handler.go` `HandlerV2.Handle` (9 steps), dispatched async (202) in `worker_handlers.go:57`. **No timing instrumentation anywhere.**

## Per-item findings (file:line evidence)

### 1. Webhook outbox (S→M)

`adapter.go` has no durability; a down/erroring ME means the event is lost forever, and the `digest/{change_id}` idempotency guard means it is *never naturally retried*. ADR-0013 D-M2-1 explicitly deferred this to M3 with "a dedicated outbox table." Recommend a transactional outbox (new migration `012`) + relay poller; the outbox INSERT must share the change-completion transaction. No external contract change; possibly a new relay-interval env.

### 2. Digest filter (S)

`handler.go:232-240` writes EVERY skill (including `Outcome="unknown"` when GetSkill failed) plus a single hardcoded phase stub into long-term semantic memory. `digest.go` BuildDigest is deterministic with a golden fixture. "Filter" = drop low-signal entries (unknown/never-applied) before persisting. ME-only, pure function, update `testdata/digest_golden.yaml`. No contract change.

### 3. Full-pipeline benchmark (S)

No benchmark of `HandlerV2.Handle`. Pattern to mirror: `internal/adapters/outbound/persistence/memory_pg_bench_test.go` (integration build tag, `b.ReportAllocs()`, ≥3× assertion). Build on the existing `fakeOrchServer` in `test/integration/consolidation_pipeline_test.go`. Recommend an in-memory benchmark first (no Docker in CI), varying skill_usage row count to expose the per-skill loop. ME-only.

### 4. Retry baseline (M, cross-repo)

The proxy `avgRetryReduction = (1.5 - applyAttempts)/1.5` (`handler.go:304-309`) is fed garbage: orch `internal/application/skill/service.go:117` **hardcodes `ApplyAttempts: 0`**, so every skill gets `0.333` always. This deadens the demoter `active→deprecated` (`< 0.05`) path and makes the promoter `>= 0.20` gate always-pass. The real data EXISTS: `migrations/postgres/003_apply.up.sql` `tasks.attempts INT`. Minimal real fix = enrich `ApplyAttempts` in orch GetUsage from the apply tasks for the change. Backward-compatible (the `apply_attempts` field is already in the GET /usage contract, currently always 0). A true *historical* baseline across changes is heavier — defer.

### 5. Rollback / deprecated_api_hits instrumentation (M, cross-repo)

The full write path ALREADY exists: `MetricsDelta.RollbackDelta`/`DeprecatedAPIHitsDelta` (`ports/outbound/skills_client.go:16-17`) → orch PATCH applies them (`skill/service.go:62`, `pg/skill_repo.go:346`) → GetSkill returns them. The only gaps: ME `computeDeltas` never SETS those deltas (`handler.go:318-327`), and orch emits no rollback/deprecated event. **Coupling:** `promoter.go:16-17` ALSO gates `RollbackCount==0 && DeprecatedAPIHits==0`, so this affects promotion too. `deprecated_api_hits` source is undecided (orch apply vs agent-mcp). Recommend deferring; prerequisite is item 4.

## Dependencies

- Item 4 unblocks item 5's deprecated path AND the avg_retry_reduction gates — do 4 first.
- Items 4 and 5 both touch orch GetUsage/skill_usage — adjacent code.
- Items 1, 2, 3 are mutually independent.

## Recommendation (scope for loop-hardening)

- **Include:** 1 (outbox, closes the only data-loss window), 2 (digest filter), 3 (benchmark), 4 (stop hardcoding apply_attempts=0).
- **Defer:** 5 (rollback/deprecated_api_hits) — needs a product/architecture decision on detection source, likely agent-mcp changes, and a live-table migration. Carve into a follow-up change with item 4 as prerequisite.

## Cross-repo contract / migration flags (PR chaining)

- Item 1: NEW orch migration `012` (outbox), net-new/low-risk; no external JSON change.
- Item 4: orch GetUsage enrichment; `apply_attempts` goes non-zero (backward compatible); no migration; ME untouched.
- Item 5 (deferred): additive `skill_usage` column on a LIVE table + possible agent-mcp contract — chain-worthy.
- Items 2/3: ME-only, no contract; item 2 updates the golden fixture.

## Risks

- **Data-loss window** (item 1) persists until the outbox lands; the outbox INSERT must be transactional with change completion to avoid a new lost-update window.
- **Behavioral shift** (item 4): real apply_attempts ACTIVATES previously-dead demote/promote gates — skills promoted under the always-0.333 proxy may now be gated/demoted. Desired, but it changes live skill lifecycle; verify with the integration test + new fixtures.
- **Live migration**: `012` outbox is additive/safe; any item-5 `skill_usage` alter is on a live table (additive column only) — flagged.
- **Webhook backward compat** (item 1): keep the POST payload identical; the outbox is internal to orch delivery, ME receiver unchanged.
- **Benchmark CI** (item 3): prefer the in-memory variant by default to avoid Docker flakiness.
