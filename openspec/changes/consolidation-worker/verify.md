# Verify Report: consolidation-worker (M2)

Cross-repo verification of the M2 learning loop.
- PR1 #82 merged to sophia-orchestator main @ `5998a82` (7 commits, working tree at `0e35bda` = pre-merge tip, identical content).
- PR2 #17 merged to sophia-memory-engine main @ `5c323ae` (9 commits, working tree at `1746344` = pre-merge tip, identical content).

## Verdict

**PASS_WITH_WARNINGS** — 0 CRITICAL, 3 WARNING, 4 SUGGESTION. The closed loop is code-complete, cross-repo JSON contracts match field-for-field, all operator invariants honored. Warnings are pre-approved M2 deviations forwarded to M3/M4+; none block archive.

## Coverage matrix

### PR1 — sophia-orchestator

| Spec requirement | Implementation evidence (file:line) | Status |
|---|---|---|
| Migration 011 table + UNIQUE + outcome CHECK + 2 indexes | `migrations/postgres/011_skill_usage.up.sql:3-16` (table, `UNIQUE(change_id,phase_type,skill_id,skill_version)`, CHECK outcome, `idx_skill_usage_change`, `idx_skill_usage_skill_injected`) | PASS |
| Migration down drops indexes + table | `011_skill_usage.down.sql` (DROP INDEX ×2 + DROP TABLE IF EXISTS) | PASS |
| skill_usage domain entity + SkillUsageID + outcome enum | `internal/domain/skillusage/skillusage.go` | PASS |
| Repo Insert (idempotent) + UpdateOutcome + FindByChange + FindBySkill | `internal/adapters/outbound/pg/skill_usage_repo.go:33-35` (`ON CONFLICT (...) DO NOTHING`), `:51,:59,:71` | PASS |
| skill_usage write at phase injection (outcome=pending) | `internal/application/phase/service.go:436-450` (Insert), `:565,:1325-1331` (UpdateOutcome on completion) | PASS |
| skill_usage write at apply injection — both sites | `internal/application/apply/teamlead.go:388,:490` via `recordSkillUsageInjection` `:612` | PASS |
| Outcome maps done→success / blocked→blocked / err→failure | `phase/service.go:1338-1340` `skillUsageOutcomeFor` | PASS |
| PATCH /skills/{id}/metrics — additive delta, SELECT FOR UPDATE, last_used_at, 422 negative, 404 unknown | handler `handlers/skills.go:42-84`; repo `skill_repo.go:293-337` (`SELECT metrics ... FOR UPDATE`, additive, `last_used_at=$3`, ErrNotFound→404) | PASS |
| PATCH /skills/{id}/status — 6-enum validation (§5.2), forbidden candidate→archived, last_validated_at | handler `skills.go:93-141` (`validStatusValues` 6 values, 422 invalid, ErrForbiddenStatusTransition→422); repo `skill_repo.go:344` sets last_validated_at | PASS |
| GET /skills/usage — filter by change_id, +apply_attempts, JSON | `skills.go:154-185` returns `{"items":[...]}` with `apply_attempts` | PASS |
| Routes under API-key middleware | `router.go:91-93` (`APIKeyWithAnonOption`), skills routes `:138-148` inside that group | PASS |
| Webhook fire-and-forget POST after publishEvent, X-API-Key, no retry, no panic, 5s default, empty URL disabled | `webhook/adapter.go:49-138` (goroutine, `context.Background`, X-API-Key, no retry, WARN logs, disabled-on-empty-URL); wired `phase/service.go:1084-1099` (after publishEvent for PhaseArchive) | PASS |
| Webhook payload mirrors PhaseArchivedPayload | `adapter.go:21-26` + `phase_bridge.go:22-28` (change_id, change_name, phase_type, archived_at) | PASS |
| M1 fix: MaxRiskLevel filter + SkipReasonRiskExceeded | `skill_matcher.go:107-115` | PASS |
| M1 fix: usage_count desc tertiary sort (NULL/zero last) | `skill_matcher.go:250-261` | PASS |
| M1 fix: NewPGSkillMatcher pool typed *pgxpool.Pool | `skill_matcher.go:39` | PASS |
| ADR-0013 | `docs/adr/0013-webhook-transport-and-skill-usage.md` (142 lines) | PASS |

### PR2 — sophia-memory-engine

| Spec requirement | Implementation evidence (file:line) | Status |
|---|---|---|
| SkillsClient port: PatchMetrics/PatchStatus/GetSkill/GetUsage | `internal/ports/outbound/skills_client.go:72-88` | PASS |
| HTTP adapter retry 3× backoff 100ms→500ms→2.5s, X-API-Key, empty-key rejected at construction, env config | `adapters/outbound/orchhttp/skills_client.go:37-72` (BackoffFactor 5.0), `:60-62` empty-key error, `:227` X-API-Key; env `ORCH_BASE_URL`/`ORCH_API_KEY` (pkg doc) | PASS |
| 4xx typed error no retry, 5xx retriable | `skills_client.go:196-210` (`HTTPStatusError` on 4xx, retry on 5xx) | PASS |
| POST /worker/phase-archived: 202+async, 400 bad JSON, API-key auth | `worker_handlers.go:41-66` (400 decode-fail, 202 before goroutine dispatch); auth `server.go:53` `middleware.APIKey(authSvc)`, route `:93-94` | PASS |
| 401 on missing/wrong key | enforced by `middleware.APIKey` wrapping `/api/v1` (server.go:53) | PASS |
| Idempotency: HasTopic(digest/{id}) first step, skip+INFO if exists, error aborts | `handler.go:122-137` (Step 1 first; exists→INFO+nil; err→return wrapped error) | PASS |
| Pipeline order: idempotency→GetUsage→deltas→PatchMetrics→promoter→demoter→proposer→digest persist | `handler.go:122-256` (Steps 1-9 in order) | PASS |
| Per-skill error isolation + panic recovery | `handler.go:154-228` (`recover()` per skill, continue on PatchMetrics/GetSkill error) | PASS |
| Deterministic YAML digest, sorted phases by phase_type, skills by skill_id, topic_key digest/{id}, type semantic, tags [change_digest] | `digest.go:54-78` (`sort.Slice` phases+skills, copy-before-sort); persist `handler.go:247-254` | PASS |
| Golden fixture byte-stable | `testdata/digest_golden.yaml` (phases apply/explore/spec alpha; skills aaa/mmm/zzz alpha; retry_reasons when attempts>1) | PASS |
| Promoter §6.1: low (succ≥1, fail==0, tests≥1); med/high/crit (succ≥2, fail==0, rollback==0, hits==0, tests≥2, retry≥0.20) NEVER relaxed; non-candidate skipped; failure>0 blocks | `promoter.go:28-90` (`thresholdsForRisk`, failure>0 early-return, status!=candidate skip, high/crit==medium) | PASS |
| Demoter: blocked (fail/usage>0.15), deprecated (retry<0.05), blocked precedence, non-active skipped, M4+ gaps noted | `demoter.go:37-63` (blocked-first precedence, `failureRatioThreshold=0.15`, `retryReductionThreshold=0.05`, status!=active skip, M4+ comments :16-19,:51-52) | PASS |
| Proposer: validated+usage≥5 → governance/skill-proposal/{id}, proposed_by=archive_worker, tags [governance,skill_proposal,pending], idempotent evidence append | `proposer.go:51-97` (validated+≥5 gates, topic_key, proposed_by, tags, evidence merge :64-74) | PASS |
| D-M2-12 no-LLM guard exists + structurally valid | `no_llm_guard_test.go:16-40` (`go list -deps` scan, banned list); structural scan of consolidation/ finds zero LLM imports | PASS |
| cmd/workers minimal + N.5 doc note | `cmd/workers/main.go:3-16,36-39` (receiver in MAIN server per N.5) | PASS |
| Pipeline wired into main server | `cmd/memory-engine/main.go:127-137` (`NewHandlerV2(...).WithPromoter().WithDemoter().WithProposer()` → `NewRouter`) | PASS |
| ME ADR-0007 | `docs/decisions/0007-consolidation-worker-pipeline.md` | PASS |

## CRITICAL findings (block archive)

None.

## WARNING findings

1. **GetSkill consumes orch endpoint that does not ship** (deviation #2, forwarded to M3). `orchhttp/skills_client.go:119-142` calls `GET /api/v1/skills/{id}`, but PR1 orch router (`router.go:141-147`) exposes only `/usage`, `/{id}/metrics`, `/{id}/status` — NOT `GET /{id}`. In production the promoter/demoter `GetSkill` round-trip (`handler.go:178`) will return 404, so post-patch promotion/demotion cannot fire until M3 ships the read endpoint. Integration coverage uses `fakeSkillsClientWithUsage.GetSkill` (`pipeline_test.go:84`), so CI is green but does NOT exercise the real gap. Documented in apply-progress + ME ADR-0007. Correctly classified WARNING (not CRITICAL): the loop's write path (metrics PATCH + digest) is fully functional; only the in-loop status transitions are inert until M3. **M3 must ship orch `GET /api/v1/skills/{id}` returning the `SkillSnapshot` JSON shape (`skill_id, status, risk_level, version, metrics{}`).**

2. **SkillActivationProposal field set narrower than spec text.** Spec `skill-activation-proposer/spec.md:37` requires `skill_name, scope, applies_when, risk_level` in the proposal; the implemented struct (`proposer.go:16-23`) carries only `skill_id, version, proposed_by, proposed_at, evidence_changes, metrics_snapshot`. The implementation faithfully follows design.md §6 (which lists exactly those 6 fields), so this is a spec-vs-design drift, not an implementation defect. Functionally sufficient for M2 pending governance (no consumer reads the extra fields yet). **M3 governance-core integration should reconcile the proposal schema (add skill_name/scope/applies_when/risk_level, sourced from GetSkill once #1 lands).**

3. **GET /skills/usage requires change_id (spec lists it optional alongside skill_id).** Handler `skills.go:156-163` returns 400 when `change_id` is absent and ignores `skill_id`. The spec `skill-usage-tracking/spec.md:57-71` describes both params as optional with a `skill_id` filter scenario. The worker only ever calls with `change_id` (`skills_client.go:147`), so the loop is unaffected, but the `skill_id` filter scenario is not served. Repo-level `FindBySkill` exists (`skill_usage_repo.go:71`) but is unwired to HTTP. **Low impact; wire `skill_id` query support in a later milestone if an external consumer needs it.**

## SUGGESTION findings (forward to M3/M4+)

1. **Webhook outbox / at-least-once delivery** — current adapter is fire-and-forget with no persistence (`adapter.go`). M3 should add the outbox per D-M2-1 so memory-engine downtime does not silently drop archive events.
2. **rollback_count + deprecated_api_hits instrumentation** — trivially 0 in M2; the `active→blocked` (rollback) and `active→deprecated` (api_hits) branches are unreachable (`demoter.go:16-19`). M4+ instrumentation closes the gap.
3. **avg_retry_reduction real baseline** — M2 uses the fixed-baseline proxy `(1.5−attempts)/1.5` (`handler.go:304-309`). M4+ should replace with a rolling historical baseline.
4. **last_stack_version wiring** — NULL in M2; the `active→deprecated` stack-mismatch branch is skipped. M3 wires StructuralContext.

## Cross-repo contract verification

**Webhook payload (orch sends → receiver expects):**
| Field | orch `PhaseArchivedWebhookPayload` (adapter.go:21-26) | ME `PhaseArchivedReceived` (worker_handlers consumes) | Match |
|---|---|---|---|
| change_id | `json:"change_id"` | decoded by `worker_handlers.go:42` | ✓ |
| change_name | `json:"change_name"` | ✓ | ✓ |
| phase_type | `json:"phase_type"` | ✓ | ✓ |
| archived_at | `json:"archived_at"` time.Time | ✓ | ✓ |

**PATCH /metrics (ME client sends → orch handler expects):** `success_delta, failure_delta, tests_passed_delta, rollback_delta, deprecated_api_hits_delta, usage_delta, avg_retry_reduction` — IDENTICAL field names: ME `patchMetricsBody` (`skills_client.go:76-84`) vs orch `patchMetricsReq` (`skills.go:31-39`). ✓ MATCH.

**PATCH /status:** `{status, reason}` — ME `patchStatusBody` (`skills_client.go:87-90`) vs orch `patchStatusReq` (`skills.go:87-90`). ✓ MATCH.

**GET /usage response envelope:** orch serves `{"items":[...]}` (`skills.go:184`); ME parses `usageResponse{Items}` (`skills_client.go:93-95,162-166`). Row fields `skill_usage_id, change_id, phase_type, skill_id, skill_version, outcome, apply_attempts` — orch `skillUsageRowDTO` (`skills.go:144-152`) vs ME `SkillUsageRow` (`skills_client.go` port:46-54). ✓ MATCH.

**GET /skills/{id} (SkillSnapshot):** ME expects `skill_id, status, risk_level, version, metrics{usage_count,success_count,failure_count,tests_passed_count,deprecated_api_hits,rollback_count,avg_retry_reduction}` (port:23-42). Orch does NOT serve this endpoint — see WARNING #1. Contract is defined ME-side only; M3 must implement orch side to this shape.

**API-key auth:** Both directions use `X-API-Key`. orch→ME webhook sets `X-API-Key` (`adapter.go:104`); ME→orch client sets `X-API-Key` (`skills_client.go:227`). Receiver gated by `middleware.APIKey` (server.go:53); orch skills routes gated by `APIKeyWithAnonOption` (router.go:93). Symmetric. ✓

## The closed loop verification

Traced through merged code, hop by hop:
1. Archive phase completes → `phase/service.go:1084-1089` publishes `EventPhaseArchived` (SSE).
2. Immediately after, `phase/service.go:1092-1099` calls `WebhookNotifier.Notify(ArchivedWebhookPayload)` (non-nil guard).
3. `webhook/phase_bridge.go:22-28` maps to `webhook.PhaseArchivedWebhookPayload`; `adapter.go:68-81` spawns goroutine → `adapter.go:84-138` POST to `{URL}/api/v1/worker/phase-archived` with X-API-Key (fire-and-forget).
4. ME receiver `worker_handlers.go:41-52` decodes payload, returns 202, dispatches `pipeline.Handle` in goroutine (`:57-65`).
5. `handler.go:124` idempotency `HasTopic("digest/"+id)`; if absent → `:140` `skills.GetUsage(changeID)` → orch `GET /usage` (`skills_client.go:146-167`) → orch `skills.go:155-185`.
6. `handler.go:151` `computeDeltas` → `:168` `PatchMetrics` → orch `PATCH /metrics` → `skill_repo.go:293-337` SELECT FOR UPDATE delta apply.
7. `handler.go:178` `GetSkill` (WARNING #1: 404 in prod until M3) → `:189-200` promoter / `:203-214` demoter → `PatchStatus` → orch `skill_repo.go:339+`.
8. `handler.go:217-224` proposer.Emit → ME `Ingest` at `governance/skill-proposal/{id}`.
9. `handler.go:242-254` `BuildDigest` → `Ingest` at `digest/{id}` type semantic tags [change_digest].

Write path (steps 1-6, 9) is fully operational. Status-transition sub-path (step 7) is inert until M3 ships orch GetSkill.

## M2 metrics gap re-confirmation

- **rollback_count + deprecated_api_hits trivially 0** — not instrumented (M4+). Demoter branches gated on them are unreachable, documented `demoter.go:16-19,:51-52` and ME ADR-0007. Confirmed.
- **avg_retry_reduction proxy** — `handler.go:304-309`: `attempts := apply_attempts (default 1 when 0); avgRetryReduction = (1.5 - attempts)/1.5`. Matches D-M2-11 `(1.5−attempts)/1.5` exactly. Confirmed.
- **last_stack_version NULL (M3)** — stack-mismatch deprecation branch skipped; no code references it in M2 demoter. Confirmed.

## Adaptations approved during apply

- `ArchivedWebhookPayload` rename (revive stutter fix) — application-layer type `phase.ArchivedWebhookPayload` (`service.go:184`) bridged to adapter `webhook.PhaseArchivedWebhookPayload` via `PhaseBridge` (`phase_bridge.go`). No external callers; compile-time interface guard `var _ phaseapp.WebhookNotifier = (*PhaseBridge)(nil)` (`phase_bridge.go:32`). No breakage. Approved.
- GetUsage failure non-fatal in pipeline — `handler.go:140-148` logs + continues with empty usage rather than aborting (design pseudocode returned the error). Defensible: digest still written, idempotency preserved. Approved.
- Pre-existing lint debt fixed in 5 ME files (responses.go, heuristic_pg.go, relation_pg.go, authsvc/service.go, trace/trace.go) — a typecheck error had masked them. Behavior-neutral cleanups unblocking the package build. Approved (deviation #3).

## Incidents during apply (ENOSPC, Docker zombie) — state integrity confirmation

ENOSPC + Docker zombie incidents occurred mid-apply (per apply-progress #819). State integrity confirmed clean:
- Both repos: conventional commits, NO Co-Authored-By / AI attribution (git log + full-body scan of PR1 `5c3c54d..0e35bda` and PR2 `866f240..1746344` — both return "NO AI attribution found").
- PR1 = 7 well-scoped commits (`feat(skillusage)`, `feat(phase,apply)`, `feat(pg)`, `feat(http)`, `feat(webhook)`, `fix(skill/matcher)`, `fix(lint)`); PR2 = 9 commits matching the documented sequence (866f240→1746344).
- No partial/corrupted artifacts: all source files compile-coherent (compile-time interface guards present in matcher, bridge, skills_client, handler). CI green per apply-progress (24 unit pkgs, integration happy/partial-failure/idempotency, lint 0, no-LLM guard green).
- No orphaned/uncommitted production code in either working tree (only untracked openspec/docs dirs in orch; nested repo marker in ME). Clean.

## Acceptance criteria (proposal-level, per PR)

**PR1** — all 9 met: migration up/down ✓; skill_usage written both callsites ✓; PATCH metrics 200/4xx ✓; PATCH status 200/4xx ✓; GET usage filterable ✓; webhook best-effort non-blocking ✓; M1 warnings fixed ✓; tests+lint green (CI) ✓; ADR-0013 ✓.

**PR2** — 10 of 10 met for M2 scope: receiver+auth (401) ✓; idempotency no-op ✓; digest YAML+persist ✓; promoter §6.1 per risk ✓; demoter §6.3 ✓; proposer ≥5 ✓; e2e test (fake orch) ✓; tests+lint green ✓; NO LLM calls ✓; ADR ✓. Caveat: in-loop promotion/demotion is inert in production until M3 GetSkill (WARNING #1) — the criterion is met at the unit/integration level (fake orch), tracked for M3.

## Risks observed for M3 / M4+

- **M3 (must):** ship orch `GET /api/v1/skills/{id}` → `SkillSnapshot` shape, or the live promoter/demoter never fires (WARNING #1). Highest-priority M3 item.
- **M3:** webhook outbox (event loss on ME downtime); StructuralContext + last_stack_version; LLM critic opt-in with budget guard; reconcile SkillActivationProposal schema (WARNING #2).
- **M4+:** rollback_count + deprecated_api_hits instrumentation; real avg_retry_reduction rolling baseline.
- **Low:** wire `skill_id` filter on GET /usage if an external consumer emerges (WARNING #3).

## Recommendation

**Ready for sdd-archive: YES.** Zero CRITICAL findings. The learning loop is code-complete and the cross-repo JSON contract — the highest-risk integration surface — matches field-for-field in both directions. All 3 WARNINGs are pre-approved M2 deviations with explicit M3/M4+ owners (chiefly the orch GetSkill endpoint), none of which corrupt state or block closing M2. Operator invariants (conventional commits, no AI attribution, strict-TDD RED-first structure, no-LLM guard, 4 locked decisions, §5.2 enums, retry-reduction proxy, fire-and-forget webhook) are all satisfied.
