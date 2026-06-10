# ADR-0013: Webhook Transport and Skill Usage (M2 PR1 Foundation)

**Status**: Accepted  
**Date**: 2026-06-10  
**Change**: `consolidation-worker` (M2, PR1)

---

## Context

M2 PR1 adds three capabilities on top of the M1 lifecycle-matcher foundation:

1. **Skill usage tracking** — persist a `skill_usage` row per injection and update
   its outcome when the apply phase completes, enabling usage-based ranking.
2. **Skills write API** — HTTP endpoints so memory-engine can push metric deltas
   and status transitions back to the orchestrator after consolidation.
3. **Webhook transport** — after a phase is archived, POST a `phase.archived`
   event to memory-engine so it can trigger consolidation workers.

M2 also closes three M1 lint warnings: the `MaxRiskLevel` query filter, the
`usage_count desc` tertiary sort key, and the `PGSkillMatcher` pool parameter
type.

---

## Decisions

### D-M2-1 — Fire-and-forget webhook: goroutine-per-notification, no retry

The orchestrator POSTs `PhaseArchivedWebhookPayload` to memory-engine from a
background goroutine spawned inside `Adapter.Notify`. The caller's context is
**not** forwarded to the HTTP request — the caller's request cancellation must
not abort delivery. Failures (network, timeout, non-2xx) are logged at `WARN`
with `change_id`; the error is never returned to the orchestrator.

**Rationale**: memory-engine consolidation is a best-effort side-channel.
Blocking the orchestrator on webhook delivery or propagating transient delivery
errors would violate D1.1 (coordinate, do not execute side effects). Retry
introduces state management complexity outside scope for M2.

**Rejected**: synchronous delivery — couples orch latency to memory-engine
availability. Structured retry queue — premature; M3 can add it with a
dedicated outbox table.

### D-M2-2 — skill_usage table: explicit row per injection per change phase

Migration 011 creates `skill_usage(id, change_id, skill_id, phase_type,
injected_at, outcome, outcome_at)`. One row is written at skill injection with
`outcome = NULL`, and updated with `outcome = 'success' | 'failure'` when the
apply phase settles. `ON CONFLICT DO NOTHING` makes the write idempotent.

**Rationale**: a join table (vs. a JSON column on skills) supports range queries
for usage analytics (D-M2-3 port contract) and avoids lock contention on the
skill row during parallel apply phases.

**Rejected**: embedding usage events in the `skills.metrics` JSONB — prevents
per-change querying and creates hot-spot update contention under parallel apply.

### D-M2-3 — SkillUsageRepository outbound port: three operations

The `outbound.SkillUsageRepository` port exposes `Create`, `UpdateOutcome`, and
`ListByChangeID`. The application layer (phase.Service, apply.TeamLead) depends
on this interface, not the concrete `pg.SkillUsageRepo`. The PG adapter
implements it. Domain/application packages never import the adapter.

**Rationale**: standard hexagonal pattern — keeps the application layer
testable with fakes and the PG adapter swappable (e.g., in-memory for tests).

### D-M2-10 — Skills write API: three endpoints, inbound port SkillService

Three endpoints are registered under `/api/v1/skills`:

- `PATCH /api/v1/skills/{id}/metrics` — apply a signed delta to all metric
  counters atomically via `SELECT FOR UPDATE` in a single Postgres transaction.
- `PATCH /api/v1/skills/{id}/status` — transition the skill status; only
  transitions defined in `allowedTransitions` are accepted (returns 422 on
  forbidden transitions).
- `GET /api/v1/skills/usage?change_id=` — return skill usage rows for a change.

The handler validates input (negative deltas → 422, unknown enum → 422) before
calling `inbound.SkillService`. `ErrNotFound` from the application layer maps to
404; `ErrForbiddenStatusTransition` maps to 422.

**Rationale**: memory-engine consolidation workers POST metric updates after
evaluating skill performance. A dedicated write API separates the concerns of
reading skills (existing GET) from updating them (new PATCH), consistent with
command/query separation.

**Rejected**: direct DB writes from memory-engine — violates the orchestrator's
ownership boundary over skill state. Single combined upsert endpoint — loses
type safety on the caller side and makes partial-update semantics ambiguous.

### D-M2-13 — M1 WARNING fixes: MaxRiskLevel filter + usage_count sort + pool type

Three items backlogged from M1 review:

**W1 — MaxRiskLevel filter**: `SkillsForContext` now applies the
`q.MaxRiskLevel` upper bound after the `appliesWhen` check. Skills whose
`risk_level` exceeds the bound are appended to the skipped list with
`SkipReasonRiskExceeded`. The `applyRiskFilter` pure function enables white-box
unit testing without a DB.

**S1 — usage_count desc tertiary sort**: `sortSkills` tertiary key changed from
`id asc` (incorrect) to `usage_count desc, NULL/zero last`. Zero is treated as
`NULL` (sorts last), consistent with SQL `NULLS LAST` semantics.

**S3 — pool parameter type**: `NewPGSkillMatcher` parameter changed from
`interface{}` to `*pgxpool.Pool` for type safety. The parameter remains unused
in M1 (in-memory filtering) but is accepted for M2+ SQL push-down readiness.

**Rationale**: these were known gaps documented as warnings in the M1 verify
report. Fixing them in M2 PR1 before new functionality lands prevents technical
debt accumulation.

### D-M2-14 — Webhook failures: WARN logging with structured fields

`Adapter.post` logs failures at `slog.Warn` with `change_id` and
`webhook.delivery_status=failed` fields. Marshal errors, request-build errors,
network errors, and non-2xx responses each produce a distinct WARN log. On
success, a `DEBUG` log records the status code.

`context.Background()` is used deliberately inside the background goroutine
(`//nolint:gosec // G118`): the caller's context must not cancel an in-flight
delivery started before the request ended.

**Rationale**: WARN-level structured logging enables alerting without coupling
orchestrator availability to memory-engine availability. DEBUG on success avoids
log noise in production while preserving traceability in verbose mode.

---

## Consequences

- `skill_usage` table is live; the PG adapter's `SkillUsageRepo` is the only
  writer (phase service + apply teamlead) and reader (GET usage API).
- Memory-engine consolidation workers can push metric updates via
  `PATCH /api/v1/skills/{id}/metrics` after evaluating each consolidated phase.
- Phase archival triggers a best-effort POST to `SOPHIA_MEMORY_WEBHOOK_URL`; if
  unset, the adapter is silently disabled.
- `sortSkills` now ranks by `usage_count desc` as tertiary key; skills with
  zero usage sort last, enabling organic promotion of proven skills.
- All M1 WARNINGS are closed; `golangci-lint` passes with 0 issues on PR1 HEAD.
