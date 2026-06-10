# Design: consolidation-worker (M2)

## Approach

Two PRs cross-repo deliver the M2 learning loop. **PR1 (sophia-orchestator)** ships first and lays the foundation: migration `011_skill_usage`, `skill_usage` writes at injection sites, three new HTTP endpoints (`PATCH /skills/{id}/metrics`, `PATCH /skills/{id}/status`, `GET /skills/usage`), an outbound webhook posted from `advanceChange` after the existing `publishEvent`, and the M1 WARNINGS fixes (`MaxRiskLevel` filter + `SkipReasonRiskExceeded`, `usage_count desc` tertiary sort, `*pgxpool.Pool` typing). **PR2 (sophia-memory-engine)** replaces the nil subscriber and PRE-0 `Handler` stub with the real pipeline: webhook receiver → idempotency guard on `digest/{change_id}` → fetch envelope/usage via `SkillsClient` → compute metrics deltas → risk-level-aware Promoter → Demoter → SkillActivationProposer to memory-engine pending → deterministic YAML `ChangeDigest`. LLM critic is OFF in M2 (no LLM imports allowed in PR2). Webhook is fire-and-forget; M3 adds outbox. All 18 operator decisions are locked (proposal §Open Questions). Strict TDD applies — RED first per `strict-tdd.md`.

## Decisions (ADR-style)

### D-M2-1: Webhook transport (orch → memory-engine)
**Choice**: orch outbound HTTP `POST /api/v1/worker/phase-archived` after `publishEvent` in `advanceChange`. Fire-and-forget; failure logged with span attribute `webhook.delivery_status=failed`. **Alternatives considered**: global SSE client (no global SSE endpoint in orch; high effort), polling (latency + cursor mgmt), message bus (no bus exists). **Rationale**: simplest path that reuses chi HTTP base + API-key auth. M3 adds outbox for at-least-once.

### D-M2-2: skill_usage table (orch migration 011)
**Choice**: explicit table `skill_usage(id, change_id, phase_type, skill_id, skill_version, injected_at, outcome)` with `UNIQUE(change_id, phase_type, skill_id, skill_version)` for injection idempotency. **Alternatives considered**: extending phase envelope with skills array (untyped today, schema churn), memory-engine tags (untyped, out of M2 scope). **Rationale**: explicit + queryable + JOIN-able + GIN-indexed; matches V4.1 §13 worker needs.

### D-M2-3: HTTP API (orch PATCH endpoints)
**Choice**: typed JSON delta bodies (see Component design §2), API-key middleware reused, domain validation at handler boundary, `400` on invalid input, `404` on missing skill, `409` on invalid status transition, `200` on success. **Alternatives considered**: PUT with full resource (race-prone), event sourcing (too complex). **Rationale**: PATCH semantics match the delta-update use case; idempotent at the metrics level via additive math.

### D-M2-4: Idempotency via digest existence check
**Choice**: Worker calls `memory.HasTopic("digest/"+change_id)`; if present, log + return nil. Otherwise process and write digest LAST. **Alternatives considered**: dedicated `processed_change_ids` table (new infra), `ON CONFLICT` on metric updates (delta semantics tricky). **Rationale**: reuses existing memory-engine infra; explicit. M3 may add a Postgres advisory lock for strict atomicity.

### D-M2-5: ChangeDigest deterministic YAML
**Choice**: Go struct + `gopkg.in/yaml.v3` encoder; phases sorted by `phase_type`, `skills_used` sorted by `skill_id`; persist at `topic_key=digest/{change_id}` (`type=semantic`, tags `["change_digest"]`). LLM-assisted variant explicitly FORBIDDEN in M2 code paths. **Alternatives considered**: JSON (V4.1 §13.1 specifies YAML), LLM-assisted summary (Q3 OFF for M2). **Rationale**: byte-stable digest enables snapshot tests + replay diffing.

### D-M2-6: Promoter algorithm (V4.1 §6.1 per risk level)
**Choice**: thresholds table keyed by `RiskLevel`; low-risk relaxed (`success>=1, tests_passed>=1`); medium/high/critical SAME (`success>=2, tests_passed>=2, failure==0, rollback==0, deprecated_api_hits==0, avg_retry_reduction>=0.20`). Promoter walks each skill in `skills_used` POST metrics-update, looks up thresholds, evaluates, calls `PatchStatus(validated)`. `last_validated_at` is set via `PatchMetrics` in the same round-trip. **Alternatives considered**: single-threshold-for-all (violates V4.1 §6.1), config-file thresholds (over-engineered for M2). **Rationale**: V4.1 §6.1 verbatim; high/critical NEVER relaxed.

### D-M2-7: Demoter algorithm (V4.1 §6.3) — M2 reachability table
| Transition | Condition | Reachability in M2 |
|---|---|---|
| `active→deprecated` | `deprecated_api_hits >= 1` | UNREACHABLE (always 0) |
| `active→deprecated` | `avg_retry_reduction < 0.05` over last 10 | REACHABLE via proxy |
| `active→deprecated` | `last_stack_version` mismatch | UNREACHABLE (NULL in M2) |
| `active→blocked` | `rollback_count >= 1` | UNREACHABLE (always 0) |
| `active→blocked` | `failure_count / max(usage,1) > 0.15` | REACHABLE |
**Rationale**: M2 metrics gap is intentional + documented in ADR-0013. M4+ instrumentation closes it.

### D-M2-8: Proposer destination (memory-engine pending)
**Choice**: when `validated.usage_count >= 5`, emit `SkillActivationProposal` to memory-engine at `topic_key=governance/skill-proposal/{skill_id}`, `type=semantic`, tags `["governance","skill_proposal","pending"]`, `proposed_by="archive_worker"`. Idempotent: re-emit appends to `evidence_changes`. **Alternatives considered**: HTTP to governance-core (no surface today), orch proxy (adds proxy layer). **Rationale**: no new infra; contract documented for future governance consumer.

### D-M2-9: SkillsClient outbound port (memory-engine)
**Choice**: `SkillsClient` port with `PatchMetrics`, `PatchStatus`, `GetSkill`, `GetUsage`. HTTP adapter retries 3x with exponential backoff (100ms → 500ms → 2.5s). Env: `SOPHIA_ORCH_BASE_URL`, `SOPHIA_ORCH_API_KEY`. **Alternatives considered**: shared DB (violates bounded context), no retries (transient failures kill the whole change). **Rationale**: bounded-context preserving; retries handle transient orch restarts.

### D-M2-10: Metrics deltas atomic at orch handler
**Choice**: orch handler opens transaction, `SELECT FOR UPDATE` on `skills.metrics` JSONB row, applies delta arithmetic, writes back, commits. **Alternatives considered**: optimistic CAS (delta semantics make CAS retries non-trivial), no locking (race condition under concurrent worker batches). **Rationale**: correctness over latency; promoter/demoter rely on the post-delta read.

### D-M2-11: avg_retry_reduction proxy
**Choice**: `(1.5 - attempts) / 1.5` with fixed baseline `1.5` (median historical). Negative when `attempts > 1.5` (worse than baseline). **Alternatives considered**: real rolling baseline (requires historical data we don't have yet), omit metric (kills medium/high/critical promotion). **Rationale**: enables promotion path in M2; ADR documents M4+ replacement.

### D-M2-12: LLM critic OFF — implementation discipline
**Choice**: NO LLM client imports anywhere under `internal/application/consolidation/` or `internal/adapters/outbound/llm/` in PR2. ADR-recorded rule + golangci-lint `forbidigo` enforces. **Rationale**: Q3 locked OFF; M3 PR reactivates with `ProposedBy: "llm_critic_advisory"` enforcement and budget guard.

### D-M2-13: M1 WARNINGS fixes piggyback on PR1
**Choice**: include in PR1 — `MaxRiskLevel` filter wired in scope loop with new `SkipReasonRiskExceeded`; reinstate `ORDER BY usage_count DESC` tertiary sort; cast pool parameter in `NewPGSkillMatcher` to `*pgxpool.Pool`. **Rationale**: same files touched; coherent unit of work.

### D-M2-14: Webhook failure handling
**Choice**: orch outbound adapter wraps HTTP error with `slog.ErrorContext(ctx, "webhook delivery failed", "error", err)`. No panic, no retry in M2. OTel span attribute `webhook.delivery_status` ∈ {`success`,`failed`}. **Rationale**: aligns with V4.1 fire-and-forget; M3 outbox adds persistence + retry.

## Data Flow

```
                   sophia-orchestator                                sophia-memory-engine
                   ┌─────────────────────────┐                       ┌─────────────────────────┐
phase.archived ────▶│ advanceChange()         │                       │                         │
                   │  ├─ publishEvent (SSE)  │                       │                         │
                   │  └─ webhook POST  ──────┼───── HTTP fire-fwd ───▶│ worker_handlers (chi)   │
                   │                         │                       │  └─ Handler.Handle      │
                   │                         │                       │      │                  │
                   │                         │                       │      ├─ memory.HasTopic │
                   │                         │                       │      │  (idempotency)   │
                   │                         │                       │      │                  │
                   │  GET  /skills/usage  ◀──┼─── SkillsClient ──────┤      ├─ GetUsage        │
                   │  PATCH /skills/metrics ◀┼─── SkillsClient ──────┤      ├─ deltas + apply  │
                   │  PATCH /skills/status ◀─┼─── SkillsClient ──────┤      ├─ Promoter/Demoter│
                   │                         │                       │      ├─ Proposer ───────┼───▶ memory pending
                   │                         │                       │      │                  │     governance/skill-proposal/*
                   │                         │                       │      └─ Digest (YAML)──┼───▶ digest/{change_id}
                   │                         │                       │                         │
   skill_usage    ◀┤ teamlead / phase svc    │                       │                         │
   (PG migration   │ writes rows on injection│                       │                         │
    011)           └─────────────────────────┘                       └─────────────────────────┘
```

## Component design

### 1. skill_usage table SQL (migration 011)

```sql
-- 011_skill_usage.up.sql
BEGIN;

CREATE TABLE skill_usage (
    id            CHAR(26) PRIMARY KEY,
    change_id     CHAR(26) NOT NULL,
    phase_type    TEXT     NOT NULL,
    skill_id      CHAR(26) NOT NULL,
    skill_version TEXT     NOT NULL,
    injected_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    outcome       TEXT     NOT NULL DEFAULT 'pending'
        CHECK (outcome IN ('pending','success','failure','blocked')),
    UNIQUE(change_id, phase_type, skill_id, skill_version)
);

CREATE INDEX idx_skill_usage_change         ON skill_usage(change_id);
CREATE INDEX idx_skill_usage_skill_injected ON skill_usage(skill_id, injected_at DESC);

COMMIT;
```

```sql
-- 011_skill_usage.down.sql
BEGIN;
DROP INDEX IF EXISTS idx_skill_usage_skill_injected;
DROP INDEX IF EXISTS idx_skill_usage_change;
DROP TABLE IF EXISTS skill_usage;
COMMIT;
```

### 2. HTTP API contracts (orch)

`PATCH /api/v1/skills/{id}/metrics` (additive deltas; floats overwrite):
```json
{
  "success_delta": 1,
  "failure_delta": 0,
  "tests_passed_delta": 1,
  "rollback_delta": 0,
  "deprecated_api_hits_delta": 0,
  "avg_retry_reduction": 0.25
}
```

`PATCH /api/v1/skills/{id}/status`:
```json
{ "status": "validated", "reason": "promoter: candidate→validated thresholds met" }
```

`GET /api/v1/skills/usage?change_id=...&skill_id=...` → array of `SkillUsage`.

### 3. PhaseArchivedWebhookPayload (mirrors `inbound.PhaseArchivedPayload`)

```go
type PhaseArchivedWebhookPayload struct {
    ChangeID   string    `json:"change_id"`
    ChangeName string    `json:"change_name"`
    PhaseType  string    `json:"phase_type"`
    ArchivedAt time.Time `json:"archived_at"`
}
```

### 4. Promoter Go shape

```go
type promotionThresholds struct {
    SuccessCount      int
    TestsPassedCount  int
    FailureCount      int     // must equal
    RollbackCount     int     // must equal
    DeprecatedAPIHits int     // must equal
    AvgRetryReduction float64 // minimum
}

func thresholdsForRisk(rl skill.RiskLevel) promotionThresholds {
    switch rl {
    case skill.RiskLow:
        return promotionThresholds{SuccessCount: 1, TestsPassedCount: 1}
    case skill.RiskMedium, skill.RiskHigh, skill.RiskCritical:
        return promotionThresholds{
            SuccessCount: 2, TestsPassedCount: 2,
            FailureCount: 0, RollbackCount: 0, DeprecatedAPIHits: 0,
            AvgRetryReduction: 0.20,
        }
    }
    return promotionThresholds{}
}

func (p *Promoter) Evaluate(ctx context.Context, sk *skill.Skill) (newStatus skill.Status, transition bool)
```

### 5. ChangeDigest YAML shape (V4.1 §13.1)

```go
type ChangeDigest struct {
    ChangeID        string         `yaml:"change_id"`
    ProjectID       string         `yaml:"project_id"`
    DurationSeconds int64          `yaml:"duration_seconds"`
    Phases          []DigestPhase  `yaml:"phases"`           // sort.Slice by PhaseType
    SkillsUsed      []DigestSkill  `yaml:"skills_used"`      // sort.Slice by SkillID
    ErrorsResolved  []DigestError  `yaml:"errors_resolved"`
}
```
Deterministic via `sort.Slice` + `gopkg.in/yaml.v3` encoder with fixed indent.

### 6. SkillActivationProposal Go shape (V4.1 §9 verbatim)

```go
type SkillActivationProposal struct {
    SkillID         string    `yaml:"skill_id"`
    SkillVersion    string    `yaml:"skill_version"`
    ProposedBy      string    `yaml:"proposed_by"`   // "archive_worker"
    ProposedAt      time.Time `yaml:"proposed_at"`
    EvidenceChanges []string  `yaml:"evidence_changes"`
    Metrics         skill.Metrics `yaml:"metrics_snapshot"`
}
```

### 7. SkillsClient interface + HTTP adapter (memory-engine)

```go
type SkillsClient interface {
    PatchMetrics(ctx context.Context, skillID string, delta MetricsDelta) error
    PatchStatus(ctx context.Context, skillID string, status, reason string) error
    GetSkill(ctx context.Context, skillID string) (*SkillSnapshot, error)
    GetUsage(ctx context.Context, changeID string) ([]SkillUsage, error)
}
```
HTTP adapter: retries 3, backoff 100ms → 500ms → 2.5s, OTel span per call.

### 8. Handler.Handle pipeline (PR2)

```go
func (h *Handler) Handle(ctx context.Context, payload consolidation.PhaseArchivedReceived) error {
    if exists, _ := h.memory.HasTopic(ctx, "digest/"+payload.ChangeID); exists {
        h.log.InfoContext(ctx, "change already processed; skip", "change_id", payload.ChangeID)
        return nil
    }
    usage, err := h.skills.GetUsage(ctx, payload.ChangeID)
    if err != nil { return err }

    deltas := computeDeltas(usage)
    for _, d := range deltas {
        if err := h.skills.PatchMetrics(ctx, d.SkillID, d.Delta); err != nil {
            h.log.ErrorContext(ctx, "patch metrics failed; continue", "skill_id", d.SkillID, "error", err)
            continue
        }
        sk, err := h.skills.GetSkill(ctx, d.SkillID)
        if err != nil { continue }
        switch sk.Status {
        case "candidate":
            if newStatus, ok := h.promoter.Evaluate(ctx, sk); ok {
                _ = h.skills.PatchStatus(ctx, d.SkillID, string(newStatus), "promoter")
            }
        case "active":
            if newStatus, ok := h.demoter.Evaluate(ctx, sk); ok {
                _ = h.skills.PatchStatus(ctx, d.SkillID, string(newStatus), "demoter")
            }
        }
        if sk.Status == "validated" && sk.Metrics.UsageCount >= 5 {
            _ = h.proposer.Emit(ctx, sk, payload.ChangeID)
        }
    }

    digest := buildDigest(payload, usage)
    yamlBytes, _ := yaml.Marshal(digest)
    return h.memory.Ingest(ctx, ingest.Request{
        TopicKey: "digest/" + payload.ChangeID,
        Type:     "semantic",
        Content:  string(yamlBytes),
        Tags:     []string{"change_digest"},
    })
}
```

## File Changes

### PR1 — sophia-orchestator

| File | Action | Description |
|---|---|---|
| `migrations/postgres/011_skill_usage.{up,down}.sql` | Create | Table + UNIQUE + 2 indexes |
| `internal/domain/skill_usage/` | Create | Domain package + ULID-typed ID |
| `internal/adapters/outbound/pg/skill_usage_repo.go` | Create | Repo with `Insert`, `FindByChange`, `UpdateOutcome` |
| `internal/application/phase/service.go` | Modify | Webhook emission post-`publishEvent` at L1028 + skill_usage outcome update on completion |
| `internal/application/apply/teamlead.go` | Modify | `skill_usage` insert at injection sites |
| `internal/adapters/inbound/http/skills_handlers.go` | Create | PATCH metrics + PATCH status + GET usage |
| `internal/adapters/inbound/http/router.go` | Modify | Wire 3 new routes under existing API-key middleware |
| `internal/adapters/outbound/pg/skill_matcher.go` | Modify | M1 W1+S1+S3 fixes (MaxRiskLevel, sort, pool typing) |
| `internal/adapters/outbound/webhook/` | Create | Outbound HTTP POST adapter |
| `internal/bootstrap/wire.go` | Modify | Wire webhook adapter + skill_usage repo |
| `docs/adr/ADR-0013-*.md` | Create | Webhook + skill_usage + cross-repo HTTP contract |

### PR2 — sophia-memory-engine

| File | Action | Description |
|---|---|---|
| `internal/adapters/inbound/http/worker_handlers.go` | Create | `POST /api/v1/worker/phase-archived` + API-key auth |
| `internal/application/consolidation/handler.go` | Modify | Real pipeline replacing PRE-0 stub |
| `internal/application/consolidation/digest.go` | Create | YAML generation + deterministic sorts |
| `internal/application/consolidation/promoter.go` | Create | V4.1 §6.1 thresholds + Evaluate |
| `internal/application/consolidation/demoter.go` | Create | V4.1 §6.3 transitions |
| `internal/application/consolidation/proposer.go` | Create | SkillActivationProposal emission + idempotent append |
| `internal/ports/outbound/skills_client.go` | Create | Port (interface) |
| `internal/adapters/outbound/http/skills_client.go` | Create | HTTP adapter + retry/backoff |
| `cmd/workers/main.go` | Modify | Wire real subscriber (replace nil) |
| Memory-engine ADR | Create | Worker pipeline + governance pending contract |

## Testing Strategy

| Layer | What to Test | Approach |
|---|---|---|
| Unit | Promoter thresholds (low / med / high / critical) | Table-driven against `thresholdsForRisk` |
| Unit | Demoter reachable conditions (avg_retry_reduction, failure_rate) | Table-driven; assert unreachable branches return `(_, false)` |
| Unit | Digest YAML determinism | Marshal same input twice; assert byte equality; snapshot golden file |
| Unit | Metrics delta arithmetic | Apply delta to known JSONB; assert post-state |
| Integration (PR1) | PATCH endpoints | `testcontainers` Postgres + chi router |
| Integration (PR1) | Migration 011 up/down | `golang-migrate` round-trip on testcontainers PG |
| Integration (PR1) | Webhook outbound fires + logs failure | `httptest.Server` capturing payloads |
| Integration (PR2) | Worker pipeline end-to-end | testcontainers PG (memory) + fake orch test server + assert digest YAML + metrics PATCH order |
| Integration (PR2) | Idempotency | Re-fire same `change_id`; second call short-circuits |
| Snapshot | Digest YAML byte exact | `testdata/digest_*.yaml` golden file |
| Strict TDD | Every behavior | RED test FIRST per `strict-tdd.md` |

## Migration / Rollout

PR1 ships first. PR1's webhook URL points at PR2's not-yet-existing endpoint — failures logged, system stays at M1 functionally. PR2 ships endpoint + worker; once deployed, the loop closes. Rollback order: PR2 first (consumer), then PR1 (migration 011 down). Both PRs use `size:exception` (operator-approved).

## Risks revisited (mitigations)

| Risk (from proposal) | Design mitigation |
|---|---|
| Cross-repo coupling | Strict PR sequencing; PR2 CI uses `httptest` fake orch; ADR documents contract |
| Webhook fire-and-forget loses events | Accepted for M2; OTel span attribute on every attempt; M3 outbox planned |
| Incomplete M2 metrics (`rollback_count`, `deprecated_api_hits` == 0) | D-M2-7 reachability table; ADR-0013 documents M4+ instrumentation |
| `avg_retry_reduction` proxy | D-M2-11 fixed baseline 1.5; ADR documents M4+ rolling baseline |
| Governance-core empty | D-M2-8 pending storage at known `topic_key`; future consumer reads same key |
| LLM advisory boundary | D-M2-12 — no LLM imports allowed in PR2; lint-enforced |
| `size:exception` on both PRs | Operator-approved; precedent INIT-0 PR2, M1 |
| Race in metrics PATCH under concurrent batches | D-M2-10 SELECT FOR UPDATE in transaction |

## Open Questions

None — all 18 operator decisions locked in proposal §Open Questions.

## Out of Scope (reaffirmed)

LLM critic (M3); webhook outbox (M3); `rollback_count` + `deprecated_api_hits` instrumentation (M4+); `last_stack_version` wiring (M3 via StructuralContext); `tenant_id` enforcement (M3); agent-governance-core HTTP surface (future); skill_usage SQL pushdown (M1 S2, only if scale demands); skills migration into `PriorContext.Skills` (M3); episodes/digests/business-rules retrieval into PriorContext (M3); LLM-assisted digest (V4.1 §13.2, M3 opt-in).
