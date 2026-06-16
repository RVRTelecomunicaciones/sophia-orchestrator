# Design: loop-hardening

## Technical Approach

Four independent slices harden the live V4.1 loop without any orch↔ME contract change (proposal §Scope, §Out of Scope). **Orch**: a generic transactional outbox (migration `012`) replaces the fire-and-forget webhook goroutine; a relay poller delivers `phase.archived` at-least-once via the *unchanged* webhook adapter; `GET /usage` stops hardcoding `apply_attempts=0` and aggregates real `tasks.attempts`; an admin command re-evaluates skills promoted under the old fake metric. **ME**: a pure filter drops `Outcome="unknown"` digest entries before persistence; an in-memory benchmark of `HandlerV2.Handle` is added. Strict-TDD seams (injectable `Clock`, poller ticker, existing fake skills client) are reused. Decisions follow the prior `D-M2-x` ADR format as `D-LH-x`.

## Architecture Decisions

### D-LH-1: Webhook outbox (orch)

**Choice**: New additive migration `012_outbox` with a generic table; the INSERT is enlisted in the existing change-completion transaction in `advanceChange` (`phase/service.go:1207-1237`), replacing the `WebhookNotifier.Notify` goroutine call. A new relay poller (ticker-driven, `FOR UPDATE SKIP LOCKED`) claims due rows, calls the *existing* `webhook.Adapter.post` **synchronously**, and marks delivered or reschedules with exponential backoff capped ~5 min. At-least-once, no dead-letter; duplicates absorbed by ME `HasTopic` guard.

**Alternatives considered**: keep fire-and-forget + retry-in-goroutine (no durability — the defect); per-event-type table (proposal locks single generic table); message bus (no bus exists); `LISTEN/NOTIFY` push (still needs durable store on consumer crash).

**Rationale**: the outbox INSERT sharing the completion txn closes the only data-loss window (proposal Risk #2). `SKIP LOCKED` is safe-by-default even though a single orch instance runs today. Reusing the webhook adapter as transport keeps the POST payload byte-identical (ME receiver untouched).

| Aspect | Decision |
|---|---|
| Table | `outbox(id, event_type, payload JSONB, status, attempts, next_attempt_at, created_at, delivered_at)`; index on `(status, next_attempt_at)` |
| INSERT seam | Inside the `c.MarkCompleted` → `ChangeRepo.Save` txn in `advanceChange`; `event_type='phase.archived'`, payload = current `ArchivedWebhookPayload` JSON |
| Poller | `time.Ticker` (interval env `SOPHIA_OUTBOX_POLL_INTERVAL`, default 5s); batch `LIMIT 50`; `SELECT ... WHERE status='pending' AND next_attempt_at<=now() FOR UPDATE SKIP LOCKED` |
| Delivery | Synchronous call into existing `webhook.Adapter`; 2xx → `status='delivered', delivered_at=now()`; else `attempts++`, `next_attempt_at=now()+backoff` |
| Backoff | `min(base*2^attempts, 5m)`, base 10s; no expiry (retry forever) |
| Observability | slog fields `outbox.event_id`, `outbox.attempts`, `outbox.delivery_status`; periodic `outbox.pending_count` gauge log |

### D-LH-2: Real ApplyAttempts (orch)

**Choice**: Replace the hardcoded `ApplyAttempts: 0` (`skill/service.go:117`) with a **per-change** aggregate of `tasks.attempts`, joined `tasks→groups→apply_boards→phases` filtered by `change_id`. Semantics: `SUM(tasks.attempts)` across all apply tasks of the change, applied identically to every `SkillUsageRow` of that change.

**Alternatives considered**: per-skill aggregation (tasks have **no** `skill_id` column — impossible without schema change, out of scope); `AVG` (loses total-effort signal and rounds toward the always-passing proxy); historical cross-change baseline (proposal Non-Goal).

**Rationale**: `tasks` link to a change only through `group_id→board_id→phase_id→change_id`; there is no skill linkage, so per-change is the finest honest granularity. The ME `computeDeltas` consumer already takes `max(ApplyAttempts)` per skill and feeds the `(1.5-attempts)/1.5` proxy (`handler.go:295-309`), so a real non-zero per-change value immediately activates the previously-dead `<0.05` demoter and `>=0.20` promoter gates. Field already in the contract — backward compatible.

```sql
SELECT COALESCE(SUM(t.attempts), 0)
FROM   tasks t
JOIN   groups g       ON g.id = t.group_id
JOIN   apply_boards b ON b.id = g.board_id
JOIN   phases p       ON p.id = b.phase_id
WHERE  p.change_id = $1;
```

### D-LH-3: Retroactive re-evaluation (orch)

**Choice**: A CLI subcommand on the existing `cmd/sophia-orchestator` binary (`reeval --dry-run` | `reeval --apply --confirm`), wired through `bootstrap.Wire` to reuse the live skill service. Dry-run prints a per-skill report; apply mutates status **only** with explicit `--confirm`, reusing `Service.PatchStatus` so the 6-enum `allowedTransitions` / `ErrForbiddenStatusTransition` validation (`skill/service.go:81-97`) is enforced unchanged.

**Alternatives considered**: admin HTTP endpoint (orch exposes no admin HTTP surface today — `main.go`→`Wire`→`Run` is the only entrypoint; a new authed route is more attack surface for a rare operator op); auto-apply (proposal Non-Goal — no mass demotion without confirmation); standalone binary (duplicates wiring).

**Rationale**: a CLI subcommand is the smallest seam that reuses existing wiring and transition validation. Operator-gated apply matches the locked decision (dry-run + explicit confirmation).

| Report column | Source |
|---|---|
| skill_id, current_status | `GetSkill` |
| recomputed_avg_retry_reduction | recompute from real `tasks.attempts` per evidence change |
| gate_verdict | promoter `>=0.20` / demoter `<0.05` outcome |
| proposed_transition | e.g. `active→deprecated`, or `none` |

Apply walks proposed transitions and calls `PatchStatus`; forbidden transitions surface `ErrForbiddenStatusTransition` and are reported as skipped, never forced.

### D-LH-4: Digest filter (ME)

**Choice**: A pure function `filterDigestSkills([]DigestSkill) []DigestSkill` dropping only `Outcome=="unknown"`, applied to `digestSkills` immediately **before** `BuildDigest` in `handler.go:231`. Never-applied / low-but-real entries stay.

**Alternatives considered**: filter inside `BuildDigest` (couples the deterministic serializer to business policy; harder to unit-test in isolation); drop at append site (scatters the rule across the loop branches at `handler.go:173,184,227`).

**Rationale**: a single pure function before serialization is the most testable seam and keeps `BuildDigest` deterministic. Golden fixture `testdata/digest_golden.yaml` is regenerated once and committed; a unit test asserts `unknown` excluded and `success`/`failure` retained.

### D-LH-5: Consolidation benchmark (ME)

**Choice**: `BenchmarkHandlerV2_Handle` (no `integration` build tag → runs by default, no Docker) in `internal/application/consolidation/`, built on the existing `fakeOrchServer` + in-memory fakes. Measures `ns/op` and `allocs/op` via `b.ReportAllocs()`; scaling dimension = `skill_usage` row count (`b.Run("rows=N")` sub-benchmarks, e.g. 1/10/100/1000) to expose per-skill loop cost.

**Alternatives considered**: testcontainers Postgres benchmark (Docker/CI flakiness — proposal Non-Goal default); benchmarking `BuildDigest` only (misses the dominant per-skill GetSkill/Patch loop).

**Rationale**: in-memory mirrors `memory_pg_bench_test.go`'s `ReportAllocs` discipline without its Docker dependency; row-count scaling surfaces the O(skills) hot path the proposal targets.

## Data Flow

### Outbox (D-LH-1)

```
advanceChange (single txn)              relay poller (ticker)
┌───────────────────────────┐          ┌──────────────────────────────┐
│ MarkCompleted             │          │ SELECT ... FOR UPDATE         │
│ ChangeRepo.Save           │          │   SKIP LOCKED (status=pending,│
│ publishEvent (SSE)        │   outbox │   next_attempt_at<=now)       │
│ INSERT outbox(pending) ───┼──table──▶│   ├─ webhook.Adapter.post ────┼──HTTP──▶ ME /worker/phase-archived
└───────────────────────────┘          │   ├─ 2xx → delivered          │           (HasTopic dedupe)
                                        │   └─ err → attempts++, backoff │
                                        └──────────────────────────────┘
```

### Re-eval (D-LH-3)

```
CLI reeval ──▶ skill.Service ──▶ GetUsage (real ApplyAttempts, D-LH-2)
                    │                   │
              recompute proxy ──▶ gate verdict ──▶ report
                    │                                   │ (--apply --confirm)
                    └────────────── PatchStatus (allowedTransitions guard) ─┘
```

## File Changes

| File | Action | Description |
|---|---|---|
| `migrations/postgres/012_outbox.{up,down}.sql` | Create | Generic outbox table + `(status,next_attempt_at)` index (additive) |
| `internal/domain/outbox/` | Create | Outbox event domain + ULID-typed ID, status enum |
| `internal/adapters/outbound/pg/outbox_repo.go` | Create | `EnqueueTx` (txn-bound), `ClaimDue` (SKIP LOCKED), `MarkDelivered`, `Reschedule`, `PendingCount` |
| `internal/application/phase/service.go` | Modify | Replace `WebhookNotifier.Notify` with txn-bound outbox INSERT in `advanceChange` |
| `internal/application/outbox/relay.go` | Create | Ticker poller; synchronous delivery via existing webhook adapter; backoff |
| `internal/adapters/outbound/webhook/adapter.go` | Modify (minimal) | Expose a synchronous `Deliver(ctx, payload) error` used by relay; keep `Notify` or remove caller |
| `internal/application/skill/service.go` | Modify | `GetUsage` aggregates real `tasks.attempts` (D-LH-2 SQL) instead of `0` |
| `internal/adapters/outbound/pg/` (skill or tasks repo) | Modify | Add `SumApplyAttemptsByChange(changeID)` query |
| `cmd/sophia-orchestator/main.go` | Modify | Subcommand dispatch (`reeval`) before `run()` |
| `internal/application/skill/reeval.go` | Create | Dry-run report + confirm-gated apply reusing `PatchStatus` |
| `internal/bootstrap/wire.go` | Modify | Wire outbox repo + relay (start ticker on Run); expose service for CLI |
| `sophia-memory-engine/.../consolidation/handler.go` | Modify | `filterDigestSkills` before `BuildDigest` |
| `sophia-memory-engine/.../consolidation/digest_filter.go` | Create | Pure `filterDigestSkills` |
| `sophia-memory-engine/.../testdata/digest_golden.yaml` | Modify | Regenerated without `unknown` entries |
| `sophia-memory-engine/.../consolidation/handler_bench_test.go` | Create | In-memory `BenchmarkHandlerV2_Handle` row-count scaling |

## Interfaces / Contracts

```go
// orch — outbox port (outbound)
type OutboxRepository interface {
    EnqueueTx(ctx context.Context, tx pgx.Tx, ev outbox.Event) error           // shares completion txn
    ClaimDue(ctx context.Context, limit int, now time.Time) ([]outbox.Event, error) // FOR UPDATE SKIP LOCKED
    MarkDelivered(ctx context.Context, id outbox.ID, at time.Time) error
    Reschedule(ctx context.Context, id outbox.ID, attempts int, next time.Time) error
    PendingCount(ctx context.Context) (int, error)
}

// orch — relay (application), Clock + Ticker injectable for strict TDD
type Relay struct { repo OutboxRepository; deliver func(context.Context, []byte) error; clock Clock /* ... */ }
```
No new JSON contract: outbox `payload` is the existing `phase.archived` body verbatim.

## Testing Strategy

| Layer | What to Test | Approach |
|---|---|---|
| Unit | Backoff schedule + 5min ceiling | Table-driven over `attempts` |
| Unit | `filterDigestSkills` drops only `unknown` | Table-driven; success/failure retained |
| Unit | Re-eval verdict math from real attempts | Table-driven; promoter/demoter gate boundaries |
| Unit | Re-eval apply skips forbidden transitions | Fake skill svc returns `ErrForbiddenStatusTransition` |
| Integration | ME down → outbox pending → delivered on recovery; INSERT shares completion txn | testcontainers PG + `httptest` toggled-down then up |
| Integration | Relay `SKIP LOCKED` claim | Concurrent claimers; no double-delivery |
| Integration | `GET /usage` returns real per-change `apply_attempts` | testcontainers PG seeded `tasks.attempts` |
| Golden | Digest excludes `unknown` | `testdata/digest_golden.yaml` snapshot |
| Bench | `HandlerV2.Handle` ns/op + allocs across row counts | In-memory `b.ReportAllocs()`, no Docker |
| Strict TDD | Every behavior | RED first per `strict-tdd.md` |

## Migration / Rollout

`012_outbox` is additive net-new. Rollout: deploy migration + relay; existing fire-and-forget call removed in same orch PR. Re-eval CLI is dormant until an operator runs it. ME slices (filter, benchmark) ship in a separate, non-stacked PR (no shared contract). Rollback per proposal §Rollback Plan: revert relay + restore goroutine, run `012` down (drops empty/idle table); revert filter+fixture; delete benchmark; revert `GetUsage` enrichment.

## Open Questions

- [ ] Relay lifecycle ownership: started inside `app.Run` goroutine vs a dedicated `outbox.Relay.Start(ctx)` — confirm with bootstrap conventions during tasks.
- [ ] Whether `webhook.Notify` (goroutine) is deleted outright or retained behind the relay; tasks phase decides to avoid dead code.
