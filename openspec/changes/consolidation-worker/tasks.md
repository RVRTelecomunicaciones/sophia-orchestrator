# Tasks: consolidation-worker (M2)

## Review Workload Forecast

| Field | Value |
|---|---|
| PR1 estimated lines (orch) | 1400–1700 |
| PR2 estimated lines (memory-engine) | 1800–2200 |
| 400-line budget risk | High both |
| Chained PRs recommended | Yes — PR1 → PR2 strict sequencing (PR2 consumes PR1 API) |
| Delivery strategy | ask-on-risk (size:exception pre-approved both) |
| Decision needed before apply | No |
| Chain strategy | stacked-to-main |
| Notes | PR1 must MERGE before PR2 ships; PR2 CI uses httptest fake orch |

Decision needed before apply: No
Chained PRs recommended: Yes
Chain strategy: stacked-to-main
400-line budget risk: High

### Suggested Work Units

| Unit | Goal | Likely PR | Notes |
|------|------|-----------|-------|
| 1 | Migration 011 + skill_usage repo + injection instrumentation + PATCH/GET endpoints + webhook outbound + M1 fixes + ADR-0013 | PR1 (orch) | Base: main; size:exception approved |
| 2 | SkillsClient port+adapter + webhook receiver + idempotency + digest + promoter + demoter + proposer + pipeline wire + memory-engine ADR | PR2 (memory-engine) | Base: main after PR1 merged; size:exception approved |

## Cross-repo PR strategy

PR1 (orch) lands first. PR2 (memory-engine) depends on PR1's HTTP API being live. No same-commit-pair gate. Each PR has its own operator checkpoint before push.

## Locked decisions absorbed (4 spec risks resolved)

1. `tests_passed_count` = derived from `skill_usage` outcome: when verify phase completes with envelope `status=done`, all `skill_usage` rows for that change get `outcome=success`, and `tests_passed_delta=1` per skill. M4+ refines with real test counts from verify envelope.
2. `avg_retry_reduction` attempts source = apply phase envelope `attempts` field; exposed via extended `GET /api/v1/skills/usage` response (includes `apply_attempts` per change).
3. Pipeline order: promoter BEFORE demoter (spec default confirmed safe).
4. Demoter precedence: `blocked` > `deprecated` (higher severity wins when both conditions met simultaneously).

---

## PR1 task groups (sophia-orchestator)

### Group A — Migration 011: skill_usage table
*Satisfies: skill-usage-tracking spec §Migration 011*

- [ ] A.1 (RED) Write integration test asserting `skill_usage` table does NOT exist at schema version 010 (pre-condition guard).
- [ ] A.2 Create `migrations/postgres/011_skill_usage.up.sql` with table DDL, `UNIQUE(change_id, phase_type, skill_id, skill_version)`, `idx_skill_usage_change`, and `idx_skill_usage_skill_injected`.
- [ ] A.3 Create `migrations/postgres/011_skill_usage.down.sql` dropping indexes then table.
- [ ] A.4 (GREEN) Integration test: post-up `skill_usage` table exists with all columns, constraints, and both indexes.
- [ ] A.5 (GREEN) Integration test: up+down round-trip — table absent after down.
- [ ] A.6 `make test-integration` green on Group A.

### Group B — skill_usage domain + repository
*Satisfies: skill-usage-tracking spec §Skill injection write path*

- [ ] B.1 (RED) Unit test: `SkillUsage` entity validates `outcome` enum (accepts `pending`, `success`, `failure`, `blocked`; rejects unknown values).
- [ ] B.2 (RED) Integration tests: `skill_usage_repo` — `Insert` writes row, `UpdateOutcome` updates existing row, `FindByChange` returns filtered rows, `FindBySkill` returns filtered rows.
- [ ] B.3 (GREEN) Create `internal/domain/skillusage/` package with `SkillUsage` entity and outcome enum.
- [ ] B.4 (GREEN) Create `internal/adapters/outbound/pg/skill_usage_repo.go` with `Insert`, `UpdateOutcome`, `FindByChange`, `FindBySkill`.
- [ ] B.5 `make test-unit && make test-integration` green on Group B.

### Group C — Injection instrumentation
*Satisfies: skill-usage-tracking spec §Skill injection write path, scenarios: row written on injection + outcome updated on completion + idempotent re-injection*

- [ ] C.1 (RED) Test: `phase/service.go` writes `skill_usage` row with `outcome=pending` when skills are injected into a phase.
- [ ] C.2 (RED) Test: `apply/teamlead.go` writes `skill_usage` rows at both `hydrateSkills` callsites.
- [ ] C.3 (RED) Test: outcome updated to `success` when phase envelope reaches `done`; `failure` on error.
- [ ] C.4 (RED) Test: re-injecting same `(change_id, phase_type, skill_id)` triple is a no-op (upsert, no error, no duplicate).
- [ ] C.5 (GREEN) Wire `SkillUsageRepo` into `internal/application/phase/service.go` at injection and completion paths.
- [ ] C.6 (GREEN) Wire `SkillUsageRepo` into `internal/application/apply/teamlead.go` at both injection sites.
- [ ] C.7 Verify green + regression: all pre-existing phase and apply tests still pass.

### Group D — Skills write API (PATCH + GET endpoints)
*Satisfies: skills-write-api spec + skill-usage-tracking spec §GET /api/v1/skills/usage*

- [ ] D.1 (RED) HTTP test: `PATCH /api/v1/skills/{id}/metrics` applies additive delta atomically (SELECT FOR UPDATE); `last_used_at` updated; returns 200.
- [ ] D.2 (RED) HTTP test: negative delta returns 422; no mutation occurs.
- [ ] D.3 (RED) HTTP test: `PATCH /api/v1/skills/{id}/metrics` missing/invalid API key returns 401.
- [ ] D.4 (RED) HTTP test: unknown skill id returns 404.
- [ ] D.5 (RED) HTTP test: `PATCH /api/v1/skills/{id}/status` valid transition `candidate→validated` returns 200; `last_validated_at` set.
- [ ] D.6 (RED) HTTP test: invalid enum value returns 422; forbidden skip transition `candidate→archived` returns 422.
- [ ] D.7 (RED) HTTP test: `GET /api/v1/skills/usage?change_id=X` returns filtered rows with `apply_attempts`; missing auth returns 401.
- [ ] D.8 (GREEN) Create `internal/adapters/inbound/http/skills_handlers.go` with `PatchMetrics`, `PatchStatus`, `GetUsage` handlers; `SELECT FOR UPDATE` transaction in metrics handler.
- [ ] D.9 (GREEN) Modify `internal/adapters/inbound/http/router.go` to wire three new routes under existing API-key middleware.
- [ ] D.10 `make test-integration` green on Group D.

### Group E — phase.archived outbound webhook
*Satisfies: phase-archived-webhook spec, all scenarios*

- [ ] E.1 (RED) Unit test: webhook adapter POSTs correct payload + API-key header after `publishEvent` (mock `httptest.Server` captures request).
- [ ] E.2 (RED) Unit test: network failure is logged at WARN level with change_id; orch returns no error.
- [ ] E.3 (RED) Unit test: configurable timeout (default 5s) — timeout logged at WARN level; orch continues.
- [ ] E.4 (RED) Unit test: non-2xx response (e.g. 500) is logged at WARN level; orch does not propagate error.
- [ ] E.5 (GREEN) Create `internal/adapters/outbound/webhook/` package with HTTP adapter; URL, API key, timeout configurable from env.
- [ ] E.6 (GREEN) Wire webhook adapter into `internal/application/phase/service.go` `advanceChange` after `publishEvent`.
- [ ] E.7 (GREEN) Modify `internal/bootstrap/wire.go` to construct and inject webhook adapter.
- [ ] E.8 `make test-unit` green on Group E.

### Group F — M1 WARNINGS fixes
*Satisfies: skill-matcher-m1-warnings-fix spec, all scenarios*

- [ ] F.1 (RED) Test: `SkillsForContext` with `MaxRiskLevel=medium` excludes skills with `risk_level=high` and `risk_level=critical`; excluded skills recorded with `SkipReasonRiskExceeded`.
- [ ] F.2 (RED) Test: `MaxRiskLevel=0` (unset) is a no-op — all risk levels pass.
- [ ] F.3 (RED) Test: two skills with identical primary + secondary sort keys — skill with `usage_count=10` appears before `usage_count=2`; `NULL usage_count` sorts last.
- [ ] F.4 (GREEN) Modify `internal/adapters/outbound/pg/skill_matcher.go`: wire `MaxRiskLevel` filter loop + `SkipReasonRiskExceeded` constant; reinstate `ORDER BY metrics->>'usage_count' DESC NULLS LAST` tertiary sort; retype `NewPGSkillMatcher` pool param to `*pgxpool.Pool`.
- [ ] F.5 `make test-unit` green on Group F; `golangci-lint run` 0 issues.

### Group G — PR1 verification + checkpoint
- [ ] G.1 `make test-unit` green — all PR1 unit tests pass.
- [ ] G.2 `make test-integration` green — all PR1 integration tests pass.
- [ ] G.3 `golangci-lint run` reports 0 issues; `forbidigo` satisfied.
- [ ] G.4 Create `docs/adr/ADR-0013-webhook-transport-and-skill-usage.md` documenting: webhook transport choice, skill_usage table, cross-repo HTTP contract, M2 metrics gap, M4+ instrumentation roadmap.
- [ ] G.5 CHECKPOINT — operator review and approval → `git push origin feat/consolidation-worker-pr1` + open PR1 against `main`.
- [ ] G.6 GATE — do NOT start Group H until PR1 is merged to `main`.

---

## PR2 task groups (sophia-memory-engine)

### Group H — SkillsClient outbound port + HTTP adapter
*Satisfies: skills-http-client spec, all scenarios*

- [ ] H.1 (RED) Unit test: `PatchMetrics` marshals correct delta JSON + sends `ORCH_API_KEY` header (httptest fake orch returns 200 → nil error).
- [ ] H.2 (RED) Unit test: retry — 3 attempts with backoff 100ms → 500ms → 2.5s on 5xx; 4th call not made.
- [ ] H.3 (RED) Unit test: orch returns 404 → adapter returns typed non-nil error containing status code.
- [ ] H.4 (RED) Unit test: `GetSkill` round-trip — fake orch returns valid JSON → populated `*Skill` with nil error.
- [ ] H.5 (RED) Unit test: constructor with empty API key returns error (adapter rejected at construction).
- [ ] H.6 (GREEN) Create `internal/ports/outbound/skills_client.go` with `SkillsClient` interface (`PatchMetrics`, `PatchStatus`, `GetSkill`, `GetUsage`).
- [ ] H.7 (GREEN) Create `internal/adapters/outbound/orchhttp/skills_client.go` HTTP adapter reading `ORCH_BASE_URL` + `ORCH_API_KEY` from env; retry/backoff; OTel span per call.
- [ ] H.8 `make test-unit` green on Group H.

### Group I — Webhook receiver
*Satisfies: worker-webhook-receiver spec, all scenarios*

- [ ] I.1 (RED) HTTP test: valid payload + correct API-key header → 202 returned immediately; `Handle` called asynchronously.
- [ ] I.2 (RED) HTTP test: malformed JSON body → 400; no pipeline triggered.
- [ ] I.3 (RED) HTTP test: missing API-key header → 401; no processing.
- [ ] I.4 (RED) HTTP test: wrong API-key value → 401; no processing.
- [ ] I.5 (GREEN) Create `internal/adapters/inbound/http/worker_handlers.go` with `POST /api/v1/worker/phase-archived` handler; dispatches `Handler.Handle` in goroutine.
- [ ] I.6 (GREEN) Wire route into memory-engine router under API-key middleware.
- [ ] I.7 `make test-unit` green on Group I.

### Group J — Idempotency + ChangeDigest
*Satisfies: worker-idempotency spec + change-digest-deterministic spec, all scenarios*

- [ ] J.1 (RED) Unit test: `digest/{change_id}` exists in memory → `Handle` returns nil immediately; no metrics call made.
- [ ] J.2 (RED) Unit test: `digest/{change_id}` absent → pipeline proceeds.
- [ ] J.3 (RED) Unit test: `HasTopic` returns error → pipeline aborts; error logged with change_id; no metrics call made.
- [ ] J.4 (RED) Golden test: marshal same `ChangeDigest` input twice — byte-identical YAML output; phases sorted by `phase_type` ascending; skills sorted by `skill_id` ascending; golden file at `testdata/digest_golden.yaml`.
- [ ] J.5 (RED) Unit test: digest persisted via `memory.Ingest` at `topic_key=digest/{change_id}`, `type=semantic`, tags `["change_digest"]`.
- [ ] J.6 (GREEN) Create `internal/application/consolidation/digest.go`: `ChangeDigest` struct + `BuildDigest` func using `gopkg.in/yaml.v3`; deterministic via `sort.Slice` on phases + skills.
- [ ] J.7 (GREEN) Wire idempotency guard as first step of `Handler.Handle` in `internal/application/consolidation/handler.go`.
- [ ] J.8 `make test-unit` green on Group J.

### Group K — Promoter
*Satisfies: skill-promoter spec, all scenarios*

- [ ] K.1 (RED) Table-driven unit tests: low-risk promotes at `success=1, tests_passed=1, failure=0`; stays candidate at `success=0`.
- [ ] K.2 (RED) Table-driven unit tests: medium promotes at `success=2, tests_passed=2, failure=0, rollback=0, deprecated_api_hits=0, avg_retry_reduction=0.25`; stays candidate at `success=1` (all else met).
- [ ] K.3 (RED) Unit test: high-risk uses same threshold as medium — `success=1, tests_passed=1, failure=0` does NOT promote.
- [ ] K.4 (RED) Unit test: `failure_count > 0` blocks promotion regardless of risk level.
- [ ] K.5 (RED) Unit test: non-candidate skill (`active`, `validated`, `blocked`) — no `PatchStatus` call made.
- [ ] K.6 (GREEN) Create `internal/application/consolidation/promoter.go` with `thresholdsForRisk` table per D-M2-6; `Promoter.Evaluate` calls `SkillsClient.PatchStatus` on transition.
- [ ] K.7 `make test-unit` green on Group K.

### Group L — Demoter
*Satisfies: skill-demoter spec, all scenarios*

- [ ] L.1 (RED) Unit test: `failure_count=2, usage_count=10` (ratio=0.20 > 0.15) → `blocked`.
- [ ] L.2 (RED) Unit test: `failure_count=1, usage_count=10` (ratio=0.10 ≤ 0.15) → no demotion.
- [ ] L.3 (RED) Unit test: `avg_retry_reduction=0.03` (< 0.05) → `deprecated`.
- [ ] L.4 (RED) Unit test: both `failure_ratio > 0.15` AND `avg_retry_reduction < 0.05` → `blocked` (precedence over `deprecated`).
- [ ] L.5 (RED) Unit test: non-active skill (`candidate`, `validated`) — no `PatchStatus` call made.
- [ ] L.6 (GREEN) Create `internal/application/consolidation/demoter.go` per D-M2-7 reachability table; evaluate `blocked` first, then `deprecated`; note M4+ unreachable branches in comments.
- [ ] L.7 `make test-unit` green on Group L.

### Group M — Proposer
*Satisfies: skill-activation-proposer spec, all scenarios*

- [ ] M.1 (RED) Unit test: `validated` skill with `usage_count=5` → `SkillActivationProposal` emitted and stored at `governance/skill-proposal/{skill_id}`.
- [ ] M.2 (RED) Unit test: `validated` skill with `usage_count=4` → no proposal.
- [ ] M.3 (RED) Unit test: `active` skill with `usage_count=10` → no proposal (wrong status).
- [ ] M.4 (RED) Unit test: re-emission with existing proposal → `evidence_changes` appended; `metrics` snapshot updated; no duplicate record.
- [ ] M.5 (RED) Unit test: proposal `proposed_by="archive_worker"`; emitting `change_id` appears in `evidence_changes`.
- [ ] M.6 (GREEN) Create `internal/application/consolidation/proposer.go` with `SkillActivationProposal` struct (V4.1 §9 fields); persist at `governance/skill-proposal/{skill_id}`, `type=semantic`, tags `["governance","skill_proposal","pending"]`.
- [ ] M.7 `make test-unit` green on Group M.

### Group N — Worker pipeline wire
*Satisfies: worker-pipeline spec, all scenarios*

- [ ] N.1 (RED) Integration test: full happy path — `Handle` with 1 skill, no existing digest; assert `GetUsage` called → `PatchMetrics` called → promoter evaluated → digest persisted at `digest/{change_id}` (fake orch httptest + real memory client).
- [ ] N.2 (RED) Integration test: `PatchMetrics` fails for skill A (fake orch returns 500); pipeline logs error for A and continues for skill B; digest still persisted.
- [ ] N.3 (RED) Integration test: re-receive same `change_id` — second `Handle` call is no-op; no `PatchMetrics` or `PatchStatus` calls on second invocation.
- [ ] N.4 (RED) Unit test: panic in promoter step is recovered; error logged for that skill; pipeline continues; worker process does not exit.
- [ ] N.5 (GREEN) Modify `internal/application/consolidation/handler.go` with real pipeline: idempotency → `GetUsage` → `computeDeltas` → `PatchMetrics` loop (continue on error) → promoter loop → demoter loop → proposer loop → `BuildDigest` → `Ingest`; recover panics per skill step.
- [ ] N.6 (GREEN) Modify `cmd/workers/main.go` to wire real webhook receiver (note: receiver lives in main HTTP server; `cmd/workers` stays minimal pending scheduler work; add comment documenting decision per D-M2-1).
- [ ] N.7 (RED→GREEN) Lint guard: `grep -r "llm\|LLM\|openai\|anthropic" internal/application/consolidation/` returns empty; add as `TestNoLLMImportsInConsolidation` using `go list` or file scan per D-M2-12.
- [ ] N.8 `make test-unit && make test-integration` green on Group N.

### Group O — PR2 verification + checkpoint
- [ ] O.1 `make test-unit` green — all PR2 unit tests pass.
- [ ] O.2 `make test-integration` green — all PR2 integration tests pass.
- [ ] O.3 `golangci-lint run` reports 0 issues; `forbidigo` confirms no LLM imports in consolidation paths.
- [ ] O.4 `grep` test (N.7) confirms no LLM client imports under `internal/application/consolidation/`.
- [ ] O.5 Create memory-engine ADR (next ADR sequence number) documenting: worker pipeline architecture, webhook receiver, governance pending contract, M2 reachability table, M4+ gaps.
- [ ] O.6 CHECKPOINT — operator review and approval → `git push origin feat/consolidation-worker-pr2` + open PR2 against `main`.

---

## Strict TDD discipline

Every GREEN task MUST be preceded by a RED task. No production code without a failing test first. PR1 Groups A→G are strictly sequential. PR2 Groups H→O are strictly sequential. Gate G.6: PR1 MUST be merged to `main` before Group H begins.

## Out of scope reminders

- LLM critic (M3) — D-M2-12 forbids imports in PR2 consolidation paths
- Webhook outbox / at-least-once delivery (M3)
- `rollback_count` + `deprecated_api_hits` instrumentation (M4+)
- `last_stack_version` wiring (M3 via StructuralContext)
- `tenant_id` enforcement (M3)
- `agent-governance-core` HTTP surface (future)
- `skill_usage` SQL pushdown (only if scale demands, M1 S2 deferred)
- Skills migration into `PriorContext.Skills` (M3)
- Episodes/digests/business-rules retrieval into PriorContext (M3)
- LLM-assisted digest (V4.1 §13.2, M3 opt-in)
- Real `avg_retry_reduction` rolling historical baseline (M4+)
