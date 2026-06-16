# ADR-0014: Webhook Outbox Transport and Real ApplyAttempts (loop-hardening)

**Status**: Accepted
**Date**: 2026-06-16
**Change**: `loop-hardening`
**Supersedes**: ADR-0013 D-M2-1 (fire-and-forget webhook transport)

---

## Context

The V4.1 learning loop went live in M2 (`consolidation-worker`) with two structural defects:

1. **Lossy webhook transport.** Per ADR-0013 D-M2-1, orch delivered `phase.archived`
   to memory-engine (ME) via a fire-and-forget goroutine POST with no retry. A down
   or erroring ME dropped the event permanently, and ME's `digest/{change_id}`
   idempotency guard means it is never naturally retried — the only unprotected link
   in the live loop. D-M2-1 itself flagged this: "M3 can add it with a dedicated
   outbox table."
2. **Fake promotion metric.** Orch hardcoded `ApplyAttempts: 0` in `GET /usage`, so
   `avg_retry_reduction` was always `0.333`: the promoter `>= 0.20` gate always
   passed and the demoter `< 0.05` branch was dead code. Skills were promoted on a
   constant.

`loop-hardening` hardens delivery and feeds the gates real data, plus two ME-side
slices (digest `unknown` filter, in-memory pipeline benchmark).

Delivered as three independent PRs: orch #97 (outbox + relay), orch #98
(real `apply_attempts` + reeval CLI), ME #19 (digest filter + benchmark). All merged
to `main` (orch `98e3430`, ME `74fc5b7`). Verdict: PASS_WITH_WARNINGS (0 CRITICAL).

---

## Decisions

### D-LH-1 — Transactional outbox replaces fire-and-forget (supersedes D-M2-1)

Migration `012` adds a generic `webhook_outbox` table. The INSERT is enlisted in the
existing change-completion transaction in `advanceChange` (replacing the
`WebhookNotifier.Notify` goroutine). A relay poller (ticker-driven, `FOR UPDATE SKIP
LOCKED`) claims due `pending` rows, calls the existing webhook adapter's synchronous
`Deliver(ctx, payload) error` transport, and marks `delivered` or reschedules with
exponential backoff `min(base*2^attempts, 5m)` (base 10s). At-least-once, no
dead-letter, no expiry; duplicates absorbed by ME `HasTopic`. The relay lifecycle is
owned by `App.Run`/`Close` (same shape as the HTTP server goroutine). The legacy
`Notify` goroutine + `phase_bridge.go` were deleted.

**Rationale**: the INSERT sharing the completion txn closes the only data-loss window.
Reusing the webhook adapter as transport keeps the POST byte-identical (ME receiver
untouched). `SKIP LOCKED` is safe-by-default even with a single orch instance today.

**Rejected**: keep fire-and-forget + retry-in-goroutine (no durability); per-event-type
table (single generic table chosen); message bus / `LISTEN-NOTIFY` (still needs durable
store on consumer crash).

### D-LH-1a — Outbox PK is CHAR(26) ULID, NOT UUID (decision obs #883)

The `webhook_outbox.id` PK is `CHAR(26)` ULID generated via the injectable
`IDGenerator`, not `UUID`. Every prior migration (009, 011) uses CHAR(26) ULID PKs and
repo CLAUDE.md rule 5 forbids `ulid.Make()`/`time.Now()` in domain/application. The repo
convention wins; the original spec UUID wording was reconciled at archive.

### D-LH-1b — Outbox payload is BYTEA, NOT JSONB (decision obs #885)

The `payload` column is `BYTEA`, not `jsonb`. JSONB normalizes whitespace and reorders
object keys on storage, which breaks the byte-identical delivery contract that
`phase-archived-webhook` asserts (and which `TestOutboxRelay_EndToEnd_MEDownThenUp`
caught). An outbox is an opaque-blob carrier; a typed/normalizing column is the wrong
tool for verbatim delivery. BYTEA is the correct type. The original spec JSONB wording
was reconciled at archive.

### D-LH-2 — Real ApplyAttempts is per-change SUM(tasks.attempts)

`GET /usage` replaces the hardcoded `0` with `SUM(tasks.attempts)` per change (joined
`tasks→groups→apply_boards→phases→change_id`, `COALESCE(...,0)`), applied uniformly to
every `SkillUsageRow` of that change. ME's `computeDeltas` takes `max(ApplyAttempts)`
per skill and feeds the `(1.5-attempts)/1.5` proxy, so the previously-dead demoter/
promoter gates now activate on real data. JSON contract unchanged.

**Limitation**: `tasks` has no `skill_id`; per-change is the finest honest granularity.
Per-skill attribution needs a schema change — see FOLLOW-UP-2.

### D-LH-3 — Retroactive reeval is a confirm-gated CLI subcommand

`sophia-orchestator reeval` runs a dry-run by default (recompute + promoter/demoter
verdict + projected transition, pure, no mutation). `reeval --apply --confirm` mutates
only gated skills via the existing `Service.PatchStatus` (the validated 6-enum
`allowedTransitions` guard). No admin HTTP endpoint, no bespoke rollback surface.

**Limitation**: reversal is delegated to the existing admin `PATCH /status` multi-hop
chain, not a single `reeval` invocation — see FOLLOW-UP-1.

### D-LH-4 — Digest drops only Outcome="unknown" (ME)

Pure `FilterDigestSkills([]DigestSkill) []DigestSkill` runs before `BuildDigest`,
dropping only `Outcome=="unknown"` (GetSkill failed); never-applied skills stay
(availability is matcher signal). Order preserved; `BuildDigest` stays deterministic.
Golden fixture regenerated deterministically (already byte-identical, no diff).

### D-LH-5 — In-memory consolidation benchmark (ME, test-only)

`BenchmarkHandlerV2_Handle` runs on package-local in-memory fakes (no `integration`
build tag, no Docker), `b.ReportAllocs()` + `b.ResetTimer()`, `b.Run("rows=N")` for
1/10/100/1000. Allocs scale linearly with row count (per-skill loop attributable).
Test-only; `go build ./...` excludes it.

---

## Tracked Follow-Ups (deferred from loop-hardening; operator-chosen)

### FOLLOW-UP-1 — Reeval single-invocation reversal (from verify WARNING #1)

The `skill-retroactive-reevaluation` spec says the command MUST be able to reverse a
confirmed change. The shipped impl delegates reversal to the existing admin
`PATCH /api/v1/skills/{id}/status` multi-hop chain (documented in CLI help + report
footer) — a real, validated recovery path, but multi-hop, not "the same command". The
literal MUST is not satisfied. A future change MUST either:

- add a `reeval --revert` surface (single-invocation undo via a prior-status snapshot), OR
- formally amend the spec MUST→SHOULD to accept the PATCH-chain delegation.

Severity: WARNING (a working reversal path exists). Operator chose to defer at archive.

### FOLLOW-UP-2 — Per-skill ApplyAttempts attribution (from verify WARNING #4)

`apply_attempts` is per-change `SUM(tasks.attempts)` applied to all skills of the change
because `tasks` has no `skill_id`. Per-skill attribution needs a schema change (a
`skill_id` on `tasks` or a usage-join) — an explicit Non-Goal of loop-hardening. The
current per-change basis is honest and activates the gates, but is coarser than ideal.
A future change should add per-skill attribution.

---

## Consequences

- `phase.archived` delivery is now durable and at-least-once; ME-down no longer drops
  events. The fire-and-forget path (D-M2-1) is removed.
- The skill lifecycle gates run on real per-change retry data; mispromotions under the
  old fake metric can be re-evaluated and corrected via `reeval`.
- ME digests no longer carry `unknown` skill noise; the consolidation pipeline has a
  Docker-free benchmark for regression tracking.
- Two follow-ups (reeval reversal, per-skill attribution) are tracked above for a
  future change.
