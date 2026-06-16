# Proposal: loop-hardening

## Intent

The V4.1 learning loop is live but has two structural defects. (1) The orch→ME `phase.archived` webhook is fire-and-forget with no retry; a down/erroring ME drops the event permanently, and the `digest/{change_id}` idempotency guard means it is never naturally retried — the only unprotected link in the live loop. (2) The learning loop promotes skills on a constant fake metric: orch hardcodes `ApplyAttempts: 0`, so `avgRetryReduction` is always `0.333` — the promoter `>= 0.20` gate always passes and the demoter `< 0.05` branch is dead code. We harden delivery and feed the gates real data now, before more skills are mispromoted.

## Scope

### In Scope
- **(1) Webhook outbox** [orch]: transactional outbox (migration `012`) + relay poller replacing fire-and-forget delivery of `phase.archived`.
- **(2) Digest filter** [ME]: drop only `Outcome="unknown"` digest entries before semantic-memory persistence.
- **(3) Full-pipeline benchmark** [ME]: in-memory benchmark of `HandlerV2.Handle`.
- **(4) Real ApplyAttempts + retroactive re-evaluation** [orch]: enrich `ApplyAttempts` in `GET /usage` from `tasks.attempts`; admin dry-run/apply command to re-evaluate skills promoted under the fake metric.

### Out of Scope (Non-Goals)
- **Item 5 deferred** (rollback / `deprecated_api_hits` instrumentation): signal-source decision pending (orch apply vs agent-mcp); item 4 is its prerequisite. Carved into a follow-up change.
- No generalization of the outbox to events other than `phase.archived` (single producer for now).
- Webhook POST payload stays byte-identical; ME receiver untouched.
- No historical baseline across changes for retry reduction (per-change `tasks.attempts` only).
- No automatic mass demotion — re-evaluation only mutates status on explicit operator confirmation.

## Capabilities

### New Capabilities
- `webhook-outbox`: transactional outbox table + relay delivering `phase.archived` at-least-once.
- `consolidation-pipeline-benchmark`: in-memory perf benchmark of the ME consolidation pipeline.
- `skill-retroactive-reevaluation`: admin dry-run/apply command recomputing promotion/demotion from real `ApplyAttempts`.

### Modified Capabilities
- `phase-archived-webhook`: delivery moves from fire-and-forget to outbox-backed at-least-once.
- `change-digest-deterministic`: `unknown`-outcome entries filtered before persistence (golden fixture updated).
- `skill-usage-tracking`: `GET /usage` `apply_attempts` becomes real (sourced from `tasks.attempts`), no longer hardcoded `0`.

## Approach

- **(1) Outbox**: New migration `012` generic table (`event_type`, `payload`, status, attempts, `next_attempt_at`). INSERT shares the change-completion transaction. Relay poller delivers with exponential backoff capped ~5 min; at-least-once, retry until delivered, no dead-letter expiry; duplicates absorbed by ME `HasTopic` guard. *Tradeoff*: generic schema, single producer — pays small upfront design cost to avoid a future migration.
- **(2) Filter**: Drop only `Outcome="unknown"` (GetSkill failed) in `handler.go` digest build; never-applied skills STAY (availability is matcher signal). Update `testdata/digest_golden.yaml`. *Tradeoff*: conservative filter, keeps low-but-real signal.
- **(3) Benchmark**: Mirror `memory_pg_bench_test.go` (integration tag, `b.ReportAllocs()`), built on `fakeOrchServer`, varying `skill_usage` row count. In-memory by default. *Tradeoff*: avoids Docker/CI flakiness over end-to-end fidelity.
- **(4) ApplyAttempts + re-eval**: Enrich `ApplyAttempts` in orch GetUsage from `tasks.attempts` for the change. Re-evaluation = admin command/endpoint that FIRST reports which skills would change status, then applies only on explicit confirmation (dry-run + operator command). *Tradeoff*: operator-gated over automatic, trading speed for safety against mass demotion.

## Affected Areas

| Area | Impact | Description |
|------|--------|-------------|
| `sophia-orchestator/migrations/postgres/012_*` | New | Generic outbox table (additive) |
| `internal/application/phase/service.go` | Modified | Outbox INSERT in change-completion txn (replaces goroutine POST) |
| orch relay poller | New | Backoff delivery of `phase.archived` |
| `internal/application/skill/service.go` | Modified | Real `ApplyAttempts` from `tasks.attempts` in GetUsage |
| orch admin re-eval command/endpoint | New | Dry-run report + confirm-to-apply promotion/demotion |
| `sophia-memory-engine/internal/application/consolidation/handler.go` | Modified | Drop `unknown` digest entries |
| `sophia-memory-engine/.../testdata/digest_golden.yaml` | Modified | Golden fixture reflecting filter |
| ME consolidation benchmark test | New | In-memory `HandlerV2.Handle` benchmark |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| Real metrics shift live skill lifecycle (demote/promote previously-dead gates) | High | Dry-run reports before any status change; operator confirms; verify with integration test + fixtures |
| Outbox INSERT not in change-completion txn → new lost-update window | Med | INSERT MUST share the completion transaction; assert in test |
| Migration risk | Low | `012` is additive-only, net-new table |
| Benchmark CI flakiness | Low | In-memory variant default, no Docker |
| Duplicate deliveries from at-least-once | Low | Absorbed by existing ME `HasTopic` idempotency |

## Rollback Plan

- (1) Revert relay + `service.go` change to restore fire-and-forget POST; run `012` down migration (drops empty/idle outbox table).
- (2) Revert filter + golden fixture (pure function, no state).
- (3) Delete benchmark test (no production code).
- (4) Revert GetUsage enrichment → `apply_attempts` returns to `0`; re-eval is operator-gated so no auto-applied changes to undo; any confirmed status changes are reversed via the same admin command.

## Dependencies

- Item 5 (deferred) depends on item 4 landing first.
- Items 1, 2, 3, 4 are mutually independent.
- orch and ME changes share no contract change in this scope → **separate, non-stacked PRs** (no chaining required).

## Success Criteria

- [ ] (1) ME down during `phase.archived` → event persisted in outbox, delivered on recovery; integration test proves no event loss and txn-shared INSERT.
- [ ] (2) Digest excludes `Outcome="unknown"` entries; never-applied skills retained; golden fixture passes.
- [ ] (3) Benchmark runs in CI without Docker, reports allocs, exposes per-skill loop cost across row counts.
- [ ] (4) `GET /usage` returns real `apply_attempts` from `tasks.attempts`; dry-run lists skills that would change status; apply mutates status only after explicit confirmation; demoter/promoter gates observably exercised by integration test.
