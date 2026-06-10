# Exploration — consolidation-worker (M2)

**Strategy ref:** V4.1 §6 (promotion policy), §9 (governance), §11 (worker responsibilities), §13 (change_digest), §16 milestone M2.
**Mode:** SDD explore. NO production code changes; investigation only.
**Scope:** **Cross-repo**: sophia-orchestator + sophia-memory-engine + (potentially) agent-governance-core.
**Engram artifact:** `sdd/consolidation-worker/explore`.

---

## 1. Critical architectural discoveries

### 🔴 Discovery 1: orch SSE stream is per-phase-id, NOT global

orch's `publishEvent` feeds `EventStream.Publish(ctx, phaseID, event)` — an **in-process pub/sub keyed by phase_id**. The only SSE endpoint is `GET /api/v1/phases/{phase_id}/events`. There is **NO global event bus**, NO global SSE stream, NO NATS/Redis/Kafka.

**Consequence**: Option A (worker as SSE client) is **NOT viable** without architectural changes to orch (new global SSE endpoint).

### 🔴 Discovery 2: agent-governance-core has ZERO HTTP surface

`/Users/russell/Documents/2026/agent-governance-core` has only `internal/domain/shared/` stubs. No HTTP handlers, no inbound adapters, no SkillActivationProposal endpoint.

**Consequence**: SkillActivationProposal cannot POST to governance-core today. M2 stores proposals in memory-engine pending.

### 🟡 Discovery 3: V4.1 Q1-Q5 still open

Per V4.1 §18, 5 open questions must close before M2:
- Q1: governance proposal threshold N (default 5 proposed)
- Q2: governance sync vs async (async proposed)
- Q3: LLM critic advisory runner (OFF for M2 proposed)
- Q4: demotion window last 10 uses (proposed)
- Q5: tenant_id enforcement (metadata-only in M2 proposed)

**Operator must close these before proposal.**

---

## 2. Current state — worker skeleton (post-PRE-0)

### `/Users/russell/Documents/2026/sophia-memory-engine/cmd/workers/main.go`

Currently:
```go
var subscriber consolidation.EventSubscriber // nil — see TODO above
if subscriber == nil {
    log.WarnContext(ctx, "worker started with no EventSubscriber wired; awaiting M2 transport decision")
    <-ctx.Done()
    return
}
```

### `/Users/russell/Documents/2026/sophia-memory-engine/internal/application/consolidation/`

- `subscriber.go` — `EventSubscriber` interface, `EventHandler` func type, `PhaseArchivedReceived` struct, `PhaseArchivedEventType = "phase.archived"` constant
- `handler.go` — `Handler` struct with log+clock; `Handle()` stubs as log-and-return-nil
- `fake_subscriber.go` — `FakeSubscriber` for tests
- `handler_test.go` — happy path + negative case

---

## 3. phase.archived emission in orch

`/Users/russell/Documents/2026/sophia-orchestator/internal/application/phase/service.go` lines 1007-1035:

```go
if err := c.MarkCompleted(s.d.Clock.Now()); err == nil {
    if saveErr := s.d.ChangeRepo.Save(ctx, c); saveErr == nil {
        // ... lookup archive phase ID ...
        s.publishEvent(ctx, archivePhaseID, inbound.EventPhaseArchived, inbound.PhaseArchivedPayload{
            ChangeID:   c.ID().String(),
            ChangeName: c.Name(),
            PhaseType:  string(phase.PhaseArchive),
            ArchivedAt: s.d.Clock.Now(),
        })
    }
}
```

**Iron Law D1.2 COMPLIANT**: Change is durable BEFORE event fires.

---

## 4. THE BIG DECISION — Worker transport

| Approach | Pros | Cons | Effort | Recommended? |
|---|---|---|---|---|
| **A. Global SSE client** | Resume semantics | Requires new global SSE endpoint in orch (doesn't exist) | High | ❌ NOT viable |
| **B. Webhook receiver** ⭐ | Simplest; reuses existing HTTP base; API-key auth ready | Fire-and-forget; no outbox (M3 adds) | Low | ✅ **YES** |
| **C. Polling** | No new endpoints | Latency; cursor mgmt | Medium | Backup |
| **D. Message bus** | Reliable+ordered | No bus exists, ops complex | Very High | ❌ |
| **E. DB polling cross-repo** | Simple | Violates bounded context | — | ❌ |

**Recommendation**: **Option B (webhook)**. orch adds `POST` to memory-engine's new `POST /api/v1/worker/phase-archived` endpoint inside `advanceChange` after existing `publishEvent`. M3 adds outbox for at-least-once.

---

## 5. Cross-repo skills update

| Approach | Recommendation |
|---|---|
| **A. orch HTTP API** ⭐ | Clean bounded context. New: `PATCH /api/v1/skills/{id}/metrics`, `PATCH /api/v1/skills/{id}/status` |
| B. Shared DB | ❌ violates bounded context |
| C. Event-sourced | Too complex for M2 |

**Recommendation**: **Option A**. Memory-engine gets new `SkillsClient` outbound port + HTTP adapter.

---

## 6. skill_usage tracking (new orch migration 011)

To compute metrics deltas, worker MUST know which skills were used per change. Options:

| Approach | Recommendation |
|---|---|
| A. Phase envelope extends with skills | Untyped today; requires schema change |
| **C. `skill_usage` table in orch (migration 011)** ⭐ | Explicit; queryable; JOIN-able; GIN-indexed |
| D. Memory-engine tags | Untyped; out of M2 scope |

**Recommendation**: **Option C**. New table `skill_usage(id, change_id, phase_type, skill_id, skill_version, injected_at, outcome)`. Orch writes rows in `phase/service.go` + `apply/teamlead.go` at skill injection sites.

---

## 7. Change envelope — what data does worker need?

Map V4.1 §5.5 Metrics to orch data:

| Metric | Source | Available in M2? |
|---|---|---|
| `usage_count` | count(skill_usage) | ✅ |
| `success_count` | envelope.status == done per skill | ✅ |
| `failure_count` | envelope.status == blocked per skill | ✅ |
| `tests_passed_count` | verify phase test results | ⚠️ requires verify envelope shape audit |
| `deprecated_api_hits` | Context7/verify output | ❌ NOT INSTRUMENTED M2 — defer to M4+ |
| `rollback_count` | runtime-adapters? | ❌ NOT INSTRUMENTED M2 — defer to M4+ |
| `avg_retry_reduction` | change.attempts vs baseline | ⚠️ M2 uses fixed proxy (baseline = 1.5) |
| `last_stack_version` | from INIT-0 StructuralContext | ⚠️ M3 (when StructuralContext wired) |

**Consequence**: M2 promotion thresholds will use **partially-satisfiable** thresholds. `rollback_count == 0` and `deprecated_api_hits == 0` are automatically met (both 0). Documented as M4+ instrumentation gap.

---

## 8. SkillActivationProposal destination

| Approach | Recommendation |
|---|---|
| Governance HTTP | ❌ governance-core has no HTTP today |
| **Memory-engine pending record** ⭐ | No new infra; documented topic_key contract |
| orch governance endpoint proxy | Adds proxy complexity |

**Recommendation**: **Memory-engine at `governance/skill-proposal/{skill_id}`**, type `semantic`, tags `["governance", "skill_proposal", "pending"]`. Governance-core reads when ready (future milestone).

---

## 9. change_digest deterministic format

V4.1 §13.1 — YAML structured:

```yaml
change_id: <id>
project_id: <id>
duration_seconds: <n>
phases:
  - phase: explore
    status: done
    attempts: 1
  - phase: apply
    status: done_with_concerns
    attempts: 3
    retry_reasons: [build_fail, lint_fail]
skills_used:
  - skill_id: <id>
    outcome: success
errors_resolved:
  - error_class: <class>
    resolved_in_phase: apply
```

Persisted at `topic_key: digest/{change_id}` in memory-engine semantic memory.

**LLM-assisted digest OUT OF M2 SCOPE** — V4.1 §13.2 makes it opt-in for complex changes only. M2 ships deterministic only.

---

## 10. M1 WARNINGS to address in PR1

From M1 archive:
- **W1**: SkillQuery.MaxRiskLevel dead field → PR1 wires filter logic + SkipReasonRiskExceeded
- **W2**: tasks.md checkboxes — process hygiene, not M2 concern
- **S1**: sort tertiary key `usage_count desc` → PR1 reinstates now that metrics populated
- **S2**: SQL pushdown when rows > 50 → defer until proven necessary
- **S3**: `NewPGSkillMatcher` pool param typing → PR1 types as `*pgxpool.Pool` when wiring SQL

---

## 11. Idempotency strategy

Worker MUST be idempotent against duplicate phase.archived events.

| Approach | Recommendation |
|---|---|
| **Worker checks `digest/{change_id}` topic_key exists in memory-engine** ⭐ | Reuses existing infra; explicit |
| Separate processed_change_ids table | New table |
| ON CONFLICT in metric updates | Complex; metric delta semantics tricky |

**Recommendation**: Worker queries memory-engine for `topic_key=digest/{change_id}`. If exists, skip (idempotent). If not, process + write digest.

---

## 12. Promotion guard for high-risk skills

V4.1 §6.1: low-risk relaxed, high/critical NEVER relaxed.

Encode in promoter:
```go
type promotionThresholds struct {
    successCount, testsPassedCount int
    failureCount, rollbackCount    int
    deprecatedAPIHits              int
    avgRetryReduction              float64
}

func thresholdsForRiskLevel(rl skill.RiskLevel) promotionThresholds {
    switch rl {
    case skill.RiskLevelLow:
        return promotionThresholds{successCount: 1, testsPassedCount: 1, ...} // relaxed
    case skill.RiskLevelMedium:
        return promotionThresholds{successCount: 2, testsPassedCount: 2, avgRetryReduction: 0.20, ...} // V4.1 §6.1 base
    case skill.RiskLevelHigh, skill.RiskLevelCritical:
        return promotionThresholds{successCount: 2, testsPassedCount: 2, avgRetryReduction: 0.20, ...} // same as base — NEVER relaxed
    }
}
```

---

## 13. Affected areas

### sophia-orchestator (PR1)
| File | Action |
|---|---|
| `migrations/postgres/011_skill_usage.{up,down}.sql` | NEW |
| `internal/domain/skill_usage/` | NEW package |
| `internal/adapters/outbound/pg/skill_usage_repo.go` | NEW |
| `internal/application/phase/service.go` | MODIFIED (instrument skill_usage writes) |
| `internal/application/apply/teamlead.go` | MODIFIED (instrument skill_usage writes) |
| `internal/adapters/inbound/http/skills_handlers.go` | NEW (PATCH metrics + PATCH status + GET usage) |
| `internal/adapters/inbound/http/router.go` | MODIFIED |
| `internal/adapters/outbound/pg/skill_matcher.go` | MODIFIED (M1 W1+S1+S3 fixes) |
| `internal/adapters/outbound/webhook/` | NEW (webhook POST to memory-engine) |
| `internal/bootstrap/wire.go` | MODIFIED |
| Tests + ADR | NEW |

### sophia-memory-engine (PR2)
| File | Action |
|---|---|
| `internal/adapters/inbound/http/worker_handlers.go` | NEW (`POST /api/v1/worker/phase-archived`) |
| `internal/application/consolidation/handler.go` | MODIFIED (real pipeline replacing stub) |
| `internal/application/consolidation/digest.go` | NEW (YAML generation) |
| `internal/application/consolidation/promoter.go` | NEW (V4.1 §6 thresholds) |
| `internal/application/consolidation/demoter.go` | NEW (active → deprecated/blocked) |
| `internal/application/consolidation/proposer.go` | NEW (SkillActivationProposal emission) |
| `internal/ports/outbound/skills_client.go` | NEW (port to orch API) |
| `internal/adapters/outbound/http/skills_client.go` | NEW (HTTP adapter) |
| `cmd/workers/main.go` | MODIFIED (wire real subscriber) |
| Tests + ADR | NEW |

---

## 14. PR delivery — 2 PRs mandatory

**PR1 (sophia-orchestator)** — foundation:
1. Migration 011: `skill_usage` table
2. Instrument skill_usage writes at injection sites
3. New HTTP API: PATCH metrics, PATCH status, GET usage
4. Webhook POST to memory-engine on `phase.archived`
5. M1 fixes: MaxRiskLevel filter + usage_count sort + pool typing
6. ADR-0013

**PR2 (sophia-memory-engine)** — worker:
1. Webhook receiver
2. Real Handler.Handle pipeline (idempotency + fetch + compute + transitions + digest + proposal)
3. ChangeDigest YAML generator
4. SkillsClient outbound + HTTP adapter
5. Promoter (V4.1 §6 thresholds per risk level)
6. Demoter (auto active → deprecated/blocked)
7. Proposer (SkillActivationProposal pending storage)
8. Wire in cmd/workers/main.go
9. ADR-XX (memory-engine ADR numbering)

**Sequencing**: PR1 lands FIRST. PR2 depends on PR1 endpoints existing.

**Forecast**: PR1 ~1500 LoC, PR2 ~2000 LoC. Both size:exception.

---

## 15. Risks

1. **Cross-repo coupling**: PR1 must merge before PR2. CI in memory-engine must use a stub/mock for orch API during dev.
2. **Webhook fire-and-forget**: If orch can't reach memory-engine at archive time, event lost. Acceptable for M2; M3 adds outbox.
3. **Incomplete M2 metrics**: rollback_count + deprecated_api_hits == 0 by default (not instrumented). Promotion thresholds REQUIRE == 0 → trivially met. Documented as M4+ instrumentation gap.
4. **avg_retry_reduction baseline**: no historical data. M2 uses fixed proxy (baseline = 1.5 attempts). Document clearly.
5. **governance-core empty**: proposals stored in memory-engine pending. Topic_key contract documented for future governance build.
6. **LLM advisory boundary**: implementation discipline risk. ALL LLM calls in worker MUST use `ProposedBy: "llm_critic_advisory"` and NEVER write `status='active'` directly.
7. **V4.1 Q1-Q5 still open**: proceed with defaults but **operator confirmation required before proposal**.

---

## 16. Ready for proposal?

**Conditional YES** — pending operator confirmation of:
- Q1: governance proposal threshold N (default 5)
- Q2: governance sync vs async (proposed async)
- Q3: LLM critic advisory runner (proposed OFF for M2)
- Q4: demotion window last 10 uses (proposed)
- Q5: tenant_id enforcement (proposed metadata-only)

Plus transport decision (webhook).
Plus PR sequencing decision (PR1 orch → PR2 memory-engine).

---

## 17. Skill resolution

Standard SDD skills. Apply will need:
- persistence-postgres (migration 011)
- go-testing (testcontainers + cross-repo HTTP mocks)
- api-contracts (typed PATCH + Proposal contracts)

`skill_resolution: none`.
