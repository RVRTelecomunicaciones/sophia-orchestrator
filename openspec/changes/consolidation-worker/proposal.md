# Proposal: consolidation-worker (M2)

## Intent

M2 closes the learning loop — implements V4.1 §6 promotion policy + §11 worker responsibilities. The worker in `sophia-memory-engine` consumes `phase.archived` events via webhook from `sophia-orchestator`, computes skill metrics deltas, applies risk-level-aware promotion/demotion thresholds (V4.1 §6.1 / §6.3), emits a deterministic `change_digest` (V4.1 §13.1), and proposes `validated → active` activations to governance pending storage. Without this change, M1's lifecycle infrastructure has **no consumer**: skills stay frozen at `candidate` forever, metrics are never updated, and no digest is ever produced. Success means a `phase.archived` event traverses orch → memory-engine, mutates skill state via orch HTTP, and persists a digest — end-to-end.

Strategy refs: V4.1 §6 (promotion), §9 (governance), §11 (worker), §13 (digest), §16 (M2). Exploration: `openspec/changes/consolidation-worker/explore.md`.

## Scope

### In Scope — PR1 sophia-orchestator (foundation, ships FIRST)

1. **Migration 011** — `skill_usage(id, change_id, phase_type, skill_id, skill_version, injected_at, outcome)` + GIN indexes (explore §6, §13).
2. **Instrument skill_usage writes** at injection sites: `internal/application/phase/service.go` + `internal/application/apply/teamlead.go`.
3. **HTTP API** — `PATCH /api/v1/skills/{id}/metrics`, `PATCH /api/v1/skills/{id}/status`, `GET /api/v1/skills/usage` (explore §5).
4. **Webhook outbound** — POST to memory-engine `POST /api/v1/worker/phase-archived` inside `advanceChange` after existing `publishEvent` (fire-and-forget, M3 adds outbox). Configurable URL + API key.
5. **M1 WARNINGS fixes**: SkillQuery.MaxRiskLevel filter + `SkipReasonRiskExceeded`; reinstate `usage_count desc` tertiary sort; type `NewPGSkillMatcher` pool as `*pgxpool.Pool` (explore §10).
6. **ADR-0013** — webhook transport + skill_usage table + cross-repo HTTP contract.

### In Scope — PR2 sophia-memory-engine (worker, ships SECOND)

1. **Webhook receiver** — `POST /api/v1/worker/phase-archived` with API-key auth.
2. **Real `Handler.Handle` pipeline** replacing PRE-0 stub: idempotency guard → fetch change/usage via orch HTTP → compute metrics → evaluate transitions → emit digest → emit proposals.
3. **ChangeDigest deterministic YAML generator** (V4.1 §13.1) + persist at `digest/{change_id}`.
4. **SkillsClient outbound port + HTTP adapter** calling orch PR1 PATCH endpoints.
5. **Promoter** — V4.1 §6.1 risk-level-aware thresholds (low relaxed; medium/high/critical same — high/critical NEVER relaxed).
6. **Demoter** — V4.1 §6.3 transitions: `active → deprecated` on `deprecated_api_hits >= 1` OR `avg_retry_reduction < 0.05` (last 10) OR `last_stack_version` mismatch; `active → blocked` on `rollback_count >= 1` OR `failure_rate > 0.15`. Q4 demotion window = last 10 uses.
7. **Proposer** — when `validated.usage_count >= 5` (Q1=5), emit `SkillActivationProposal` to memory-engine at `topic_key=governance/skill-proposal/{skill_id}`, type `semantic`, tags `["governance","skill_proposal","pending"]` (explore §8).
8. **Wire** in `cmd/workers/main.go` (replace nil subscriber).
9. **ADR** in memory-engine ADR sequence — worker pipeline + webhook receiver + governance pending contract.

### Out of Scope (explicit deferrals)

- **LLM critic advisory runner** (Q3 = OFF for M2 — M3 opt-in with budget). NO LLM calls in any M2 code path.
- **Webhook outbox / at-least-once delivery** (M3).
- **`rollback_count` + `deprecated_api_hits` instrumentation** (M4+) — both default 0 in M2, thresholds trivially met.
- **`last_stack_version`** (M3 — requires StructuralContext wired from INIT-0).
- **`tenant_id` enforcement** (Q5 = metadata-only in M2; M3 activates).
- **`agent-governance-core` HTTP surface** (no surface today — future milestone).
- **`skill_usage` SQL pushdown** (M1 S2 — only if scale demands).
- **Skills migration into `PriorContext.Skills`** (M3).
- **Episodes / digests / business-rules retrieval into PriorContext** (M3).
- **LLM-assisted digest** (V4.1 §13.2, M3 opt-in).
- **Real `avg_retry_reduction` baseline** — M2 uses proxy `(1.5 - current_attempts) / 1.5`; M4+ replaces with rolling historical baseline.

## Capabilities

> Contract between proposal and `sdd-spec`. Each `New Capability` → new `openspec/specs/<name>/spec.md`. Research existing `openspec/specs/` before naming.

### New Capabilities — PR1 sophia-orchestator

- `skill-usage-tracking`: migration 011 + write path at injection sites + `GET /api/v1/skills/usage` read endpoint.
- `skills-write-api`: `PATCH /api/v1/skills/{id}/metrics` + `PATCH /api/v1/skills/{id}/status` with typed validation + idempotent semantics.
- `phase-archived-webhook`: orch outbound HTTP POST to memory-engine after `phase.archived` (best-effort, logged on failure).
- `skill-matcher-m1-warnings-fix`: MaxRiskLevel filter + `usage_count desc` tertiary sort + `*pgxpool.Pool` typing.

### New Capabilities — PR2 sophia-memory-engine

- `worker-webhook-receiver`: `POST /api/v1/worker/phase-archived` with API-key auth.
- `worker-idempotency`: skip when `digest/{change_id}` already present.
- `change-digest-deterministic`: V4.1 §13.1 YAML generator + persistence.
- `skill-promoter`: V4.1 §6.1 risk-level-aware thresholds; `candidate → validated` transitions via orch PATCH.
- `skill-demoter`: V4.1 §6.3 `active → deprecated/blocked` transitions; Q4 last-10-uses window.
- `skill-activation-proposer`: emit `SkillActivationProposal` to memory-engine pending when `validated.usage_count >= 5`.
- `skills-http-client`: outbound port + HTTP adapter calling orch PATCH endpoints.
- `worker-pipeline`: end-to-end `Handler.Handle` replacing PRE-0 stub.

### Modified Capabilities

- None.

## Approach

**Sequencing**: PR1 (orch) ships and merges FIRST. PR1's webhook URL points at PR2's not-yet-existing endpoint — 4xx is logged; system stays at M1 functionally. PR2 ships endpoint + worker; once deployed, the loop closes. Both PRs use `size:exception` for atomic milestone delivery (precedent: INIT-0 PR2, M1).

**Per-PR strict TDD ordering** (foundation → repos → handlers → wire → ADR → verify), failing test FIRST per `strict-tdd.md`.

**Promotion thresholds (V4.1 §6.1, operator-locked)**:
- low-risk relaxed: `success_count ≥ 1`, `failure_count == 0`, `tests_passed_count ≥ 1`.
- medium: `success_count ≥ 2`, `failure_count == 0`, `rollback_count == 0`, `deprecated_api_hits == 0`, `tests_passed_count ≥ 2`, `avg_retry_reduction ≥ 0.20`.
- high/critical: **SAME as medium — NEVER relaxed**.

**Demotion thresholds (V4.1 §6.3, operator-locked)**:
- `active → deprecated`: `deprecated_api_hits ≥ 1` OR `avg_retry_reduction < 0.05` (last 10) OR `last_stack_version` mismatch.
- `active → blocked`: `rollback_count ≥ 1` OR `(failure_count / max(usage_count, 1)) > 0.15`.

**M2 metrics gap** (documented in ADR):
- `rollback_count` + `deprecated_api_hits` == 0 by default (not instrumented). Thresholds trivially met for missing fields.
- `avg_retry_reduction` proxy = `(1.5 - current_attempts) / 1.5` (baseline 1.5).
- `last_stack_version` = NULL in M2 (mismatch check skipped).
- M4+ closes instrumentation gap.

**Idempotency**: worker queries memory-engine for `topic_key=digest/{change_id}`; if present, no-op. Otherwise process + write digest.

**Governance destination**: proposals stored at `governance/skill-proposal/{skill_id}` in memory-engine, type `semantic`, tags `["governance","skill_proposal","pending"]`. `agent-governance-core` reads when its HTTP surface exists (future). Contract documented in ADR.

## Affected Areas

### PR1 — sophia-orchestator

| Area | Impact | Description |
|------|--------|-------------|
| `migrations/postgres/011_skill_usage.{up,down}.sql` | New | Table + GIN indexes |
| `internal/domain/skill_usage/` | New | Domain package |
| `internal/adapters/outbound/pg/skill_usage_repo.go` | New | Repo |
| `internal/application/phase/service.go` | Modified | Instrument skill_usage writes |
| `internal/application/apply/teamlead.go` | Modified | Instrument skill_usage writes |
| `internal/adapters/inbound/http/skills_handlers.go` | New | PATCH metrics + PATCH status + GET usage |
| `internal/adapters/inbound/http/router.go` | Modified | Wire new routes |
| `internal/adapters/outbound/pg/skill_matcher.go` | Modified | M1 W1+S1+S3 fixes |
| `internal/adapters/outbound/webhook/` | New | Outbound POST to memory-engine |
| `internal/bootstrap/wire.go` | Modified | Wire webhook + skill_usage repo |
| `docs/adr/ADR-0013-*.md` | New | ADR-0013 |

### PR2 — sophia-memory-engine

| Area | Impact | Description |
|------|--------|-------------|
| `internal/adapters/inbound/http/worker_handlers.go` | New | `POST /api/v1/worker/phase-archived` |
| `internal/application/consolidation/handler.go` | Modified | Real pipeline replacing stub |
| `internal/application/consolidation/digest.go` | New | YAML generation |
| `internal/application/consolidation/promoter.go` | New | V4.1 §6.1 thresholds |
| `internal/application/consolidation/demoter.go` | New | V4.1 §6.3 transitions |
| `internal/application/consolidation/proposer.go` | New | SkillActivationProposal emission |
| `internal/ports/outbound/skills_client.go` | New | Port to orch API |
| `internal/adapters/outbound/http/skills_client.go` | New | HTTP adapter |
| `cmd/workers/main.go` | Modified | Wire real subscriber |
| Memory-engine ADR | New | Worker pipeline + governance pending contract |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| Cross-repo coupling (PR2 depends on PR1 endpoints) | High | Strict sequencing: PR1 merges FIRST. PR2 CI uses HTTP mocks. Documented in both ADRs. |
| Webhook fire-and-forget loses events on memory-engine downtime | Med | Acceptable for M2 (V4.1 explicit). Log on failure. M3 ships outbox for at-least-once. |
| Incomplete M2 metrics (`rollback_count`, `deprecated_api_hits` == 0) | High | Documented gap. Thresholds trivially met for missing fields. M4+ remediation milestone. |
| `avg_retry_reduction` proxy (fixed baseline 1.5) | Med | Documented in ADR. M4+ replaces with rolling historical baseline. |
| `governance-core` empty — proposals stranded | Med | Proposals stored pending in memory-engine; topic_key contract documented for future governance consumer. |
| LLM advisory boundary discipline | Low (M2) | **LLM critic OFF in M2** — Q3 locked. NO LLM API calls in any code path. M3 reactivates with `ProposedBy: "llm_critic_advisory"` enforcement. |
| `size:exception` on both PRs | Med | Operator approves in this proposal. Precedent: INIT-0 PR2, M1. Atomic milestone delivery is correct trade-off here. |
| Conventional commits / NO Co-Authored-By drift | Low | Hard rule enforced in apply phase; verify checks. |

## Rollback Plan

**Order**: PR2 reverts FIRST (it's the consumer), then PR1.

- **PR2 revert**: Worker stops processing. Webhook receiver returns 410/404. Orch logs webhook failures but continues normally — system returns to M1 state. No data corruption (proposals at `governance/skill-proposal/*` remain readable but stale).
- **PR1 revert**: Migration 011 down. Webhook outbound removed. M1 WARNINGS fixes reverted. System returns to pre-M2 orch state.
- **PR1-only revert** (PR2 already merged): memory-engine receives 404s for orch endpoints. Worker errors logged but pipeline halts. Operator must redeploy PR1 or roll PR2 back.

## Dependencies

- M1 (`skill-injection-context`) merged. ✅
- V4.1 §18 Q1–Q5 closed by operator. ✅ (Q1=5, Q2=async, Q3=OFF, Q4=last 10, Q5=metadata-only).
- Cross-repo HTTP API key provisioned between orch and memory-engine.
- PR1 merged before PR2 ships.

## Success Criteria

### PR1 — sophia-orchestator
- [ ] Migration 011 applies + reverses cleanly.
- [ ] `skill_usage` rows written on every skill injection (both callsites: `phase/service.go`, `apply/teamlead.go`).
- [ ] `PATCH /api/v1/skills/{id}/metrics` returns 200 on valid input, 4xx on invalid.
- [ ] `PATCH /api/v1/skills/{id}/status` returns 200 on valid transition, 4xx on invalid.
- [ ] `GET /api/v1/skills/usage` returns rows filterable by `change_id` / `skill_id`.
- [ ] Webhook POST fires after every `phase.archived` (best-effort; failure logged, never blocks).
- [ ] M1 WARNINGS fixed: MaxRiskLevel filter applied + `SkipReasonRiskExceeded`; `usage_count desc` tertiary sort active; pool typed as `*pgxpool.Pool`.
- [ ] `go test ./...` green + `golangci-lint run` clean.
- [ ] ADR-0013 merged.

### PR2 — sophia-memory-engine
- [ ] Webhook receiver accepts orch payloads with API-key auth (401 on missing/wrong key).
- [ ] Idempotency: re-receiving same `change_id` is no-op (digest already at `digest/{change_id}`).
- [ ] ChangeDigest YAML generated per V4.1 §13.1 + persisted to memory-engine.
- [ ] Promoter: `candidate → validated` when V4.1 §6.1 thresholds met per risk level (low relaxed; medium/high/critical same).
- [ ] Demoter: `active → deprecated/blocked` on threshold breach per V4.1 §6.3.
- [ ] Proposer: `validated.usage_count >= 5` emits `SkillActivationProposal` to `governance/skill-proposal/{skill_id}` pending.
- [ ] End-to-end test (testcontainers): simulate `phase.archived` → metrics updated via orch PATCH + transition applied + digest persisted.
- [ ] `go test ./...` green + `golangci-lint run` clean.
- [ ] **NO LLM API calls in any code path** (Q3 OFF in M2).
- [ ] Memory-engine ADR merged.

## Open Questions

None — all 18 operator decisions locked (transport, governance threshold, proposal destination, LLM advisory off, PR sequencing, demotion window, tenant_id metadata-only, cross-repo skills update, skill_usage tracking, digest deterministic only, idempotency, promotion thresholds per risk level, demotion thresholds, M2 metrics gap, conventional commits, strict TDD, size:exception accepted, M1 WARNINGS fixes in PR1).

## Strict TDD Note

`strict_tdd: true`. Specs MUST define test-first acceptance per capability. Apply phase follows `strict-tdd.md`: failing test FIRST for every behavior, no production code without a red test.
