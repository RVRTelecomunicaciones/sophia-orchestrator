# Verification Report: governance-advisory

> Phase: sdd-verify | Mode: openspec | Date: 2026-06-16
> Cross-repo change. orch main tip `03cc00e` (PR #100 PR-B, PR #101 PR-A merged).
> govcore main tip `5d5648c` (PR #10 seam merged). All PRs MERGED.

## Executive Verdict

**PASS** — Ready for sdd-archive: **YES**

Both capabilities are fully implemented, match their specs scenario-for-scenario,
honor every operator-locked decision, and pass at runtime. Zero production code was
changed for GAP A (verify+lock only). The advisory critic is strictly non-blocking,
deterministic, per-change opt-in default OFF, and byte-identical to today when opted out.

- CRITICAL: 0
- WARNING: 0
- SUGGESTION: 2

## Completeness

| Item | State |
|------|-------|
| Stale `governance-decision-facade` capability | ABSENT (correctly removed) — only `advisory-critic` + `governance-integration-contract` exist |
| GAP A tasks (A.1, B.1..B.6 test + CI isolation) | Complete (obs #915) |
| GAP B tasks (groups C,D,E,F,G,H) | Complete (obs #916) |
| govcore seam | Complete + merged to govcore main (obs #917, PR #10) |

## Test / Build Evidence (exit codes captured directly, never piped)

| Command | Exit | Result |
|---------|------|--------|
| `go test -tags=contract -race -count=1 -run TestContractLock ./test/integration/...` | 0 | PASS (1.027s, uncached) — re-run locally GREEN |
| `go test -tags=integration -count=1 -run TestContractLock ./test/integration/...` | 0 | "no tests to run" — contract test EXCLUDED from integration job (no CI regression) |
| `go test -race -count=1 ./internal/...` (full unit suite) | 0 | ALL GREEN |
| critic-focused: `./internal/domain/phase ./internal/adapters/outbound/critic ./internal/application/phase ./internal/ports/inbound` | 0 | ALL GREEN |
| `make lint` (orch, golangci-lint) | — | 0 issues |
| `go test -tags=wirecontract -count=1 ./test/wirecontract/...` | 0 | PASS — wire-contract test unchanged + green |
| govcore `go test -race -count=1 ./govhttptest/...` | 0 | PASS |
| govcore `golangci-lint run ./govhttptest/...` | — | 0 issues |
| CI run 27607068239 (`contract` job) | success | Cross-repo workspace job GREEN in CI |

All local runs were re-executed uncached (`-count=1`); did not rely solely on the merged-CI-green run.

## Capability: governance-integration-contract (GAP A) — PASS

Test host: `test/integration/governance_contract_lock_test.go` (`//go:build contract`).
Drives orch's REAL `governance.Client` against govcore's REAL `/governance/v1/*` facade
in-process via the `govhttptest` seam (production chi router + real `govdecisions.DecisionsService`
over deterministic in-memory repos). NO mocks on either side.

| Spec Scenario | Status | Evidence |
|---------------|--------|----------|
| Real client reaches real facade in-process | PASS | governance_contract_lock_test.go:68-80 (seam boots real handler + real client; no mock); zero production source touched in either repo |
| Phase decision returns deserializable payload | PASS | :87-114 — `EvaluatePhase` → asserts `DecisionAllow`, `AgentRole="team-lead"`, `Reason="M-E0 default-allow policy"`, 1 recorded decision row |
| Sensitive decision returns deserializable payload | PASS | :119-139 — `EvaluateSensitiveAction` → asserts `DecisionAllow`, sensitive reason, 1 recorded row |
| Approval-status GET returns deserializable status | PASS | :147-162 — `AwaitApproval` on `{change_id}/{phase_id}/status` resolves granted (nil), drift-locks route + `status` field |
| Request/response shape drift fails the test | PASS | concrete decoded-field assertions (:103-108, :132-135) fail on any wire-shape drift |

Non-goals honored: no facade rebuild (production code untouched both repos); no `constrain→allow_with_constraints` mapping (recorded as follow-up); facade default-allow+audit taken as-is.

CI isolation confirmed: dedicated `contract` job (.github/workflows/ci.yaml:71-118) checks out both repos + `go work init` + `-tags=contract`; standard `integration` job (:59-69) runs `-tags=integration` and does NOT compile the contract test. Merge-order dependency (PR-A after govcore seam #10) is satisfied — seam is on govcore main `5d5648c`.

## Capability: advisory-critic (GAP B) — PASS

| Spec Scenario | Status | Evidence |
|---------------|--------|----------|
| Concern is a pure value | PASS | concern.go:13-27 (`{Severity,Category,Message,Evidence}` strings, no gating field); concern_test.go:15 |
| No severity blocks or escalates | PASS | service.go:636-640 upgrades DONE→DONE_WITH_CONCERNS only; AdvanceAllowed true; service_critic_test.go:181 (high severity still non-blocking) |
| Critic stub error must not break the phase | PASS | service.go:1533-1541 logs+swallows; service_critic_test.go:212 (error → phase stays DONE) |
| Opted-out byte-identical to today (default OFF) | PASS | reviewConcerns gate service.go:1525 (`Critic==nil \|\| !parseScopeCriticEnabled`); parseScopeCriticEnabled default false :1549-1567; service_critic_test.go:135 (never called) + :262 (plain completed, no concerns payload) |
| Opted-in runs critic on all enveloped phases | PASS | insertion in runAsync post-Validate/pre-Complete service.go:636 (single-agent enveloped path); service_critic_test.go:163 |
| Deterministic stub produces concerns | PASS | stub.go:37-67 (high risk→high/risk, confidence<0.5→medium/confidence; NO time.Now/ulid/rand); stub_test.go:27,97 (reproducible) |
| Opted-in zero concerns → plain DONE | PASS | service.go:637 (`len>0 && StatusDone` guard); service_critic_test.go:196 (stays DONE, no `_with_concerns` event) |
| SSE concern payload via phase.completed_with_concerns | PASS | event_payloads.go:348-372 (`Concerns []ConcernPayload omitempty`); service.go:692-696 populated ONLY when eventType==EventPhaseCompletedWithConcerns; service_critic_test.go:240 |
| governance-core down does not implicate critic | PASS | critic independent of GAP A (no governance import in critic/concern/port); IL4 path untouched |
| BLOCKED/NEEDS_CONTEXT never downgraded | PASS | service.go:637 only acts on `env.Status==StatusDone`; service_critic_test.go:226 |

Determinism (Iron Law / CLAUDE.md rule 5): `grep time.Now()/ulid.Make()` in concern.go, critic.go, stub.go → no matches. Critic never touches memory-engine/consolidation: `grep memory/Memory/consolidat` in critic package + port + concern → no matches. Wired nil-tolerant in wire.go:415 (`criticadapter.NewStub()`).

## Design Coherence (D-GA-1..D-GA-6) — PASS

D-GA-1 (verify not build) ✓ zero production change GAP A. D-GA-2 (CriticPort + Concern in phase pkg) ✓ critic.go + concern.go. D-GA-3 (deterministic stub) ✓ stub.go. D-GA-4 (opt-in default OFF, nil-tolerant) ✓ parseScopeCriticEnabled + wire.go. D-GA-5 (post-Validate/pre-Complete, upgrade-only, swallow error) ✓ service.go:626-640. D-GA-6 (SSE omitempty payload) ✓ event_payloads.go + service.go:692-696.

## Operator Invariants — PASS

| Invariant | Status |
|-----------|--------|
| Facade ownership = govcore (seam is test-support only) | PASS — govhttptest is a non-internal test seam; no facade rebuild |
| Critic strictly advisory / non-blocking / never escalates | PASS — upgrade-only DONE→DONE_WITH_CONCERNS, AdvanceAllowed true |
| Deterministic stub (real LLM deferred) | PASS — no clock/rand; error return reserved for future LLM impl |
| Per-change opt-in DEFAULT OFF | PASS — parseScopeCriticEnabled default false |
| GAP A = verify+lock (no production change) | PASS — only test + CI yaml changed |
| Conventional commits, NO Co-Authored-By/AI attribution | PASS — orch range a63a935..03cc00e + govcore seam: no AI trailer/🤖/noreply@anthropic. govcore has a legitimate HUMAN co-author (Russell) only |
| Strict-TDD RED-first structure | PASS — per apply-progress #915/#916/#917, RED→GREEN→VERIFY per work unit |
| No time.Now()/ulid.Make() in domain/application | PASS — both repos clean in critic/seam code |

## Deferred Follow-ups (recorded, not failures)

- Real LLM critic (non-goal/deferred); apply-phase (runApplyPhase) critic coverage; durable concern persistence (SSE-only MVP); `constrain→allow_with_constraints` enum mapping; govcore module-path vs remote mismatch (workspace-only resolution); ECOSYSTEM_REPO_TOKEN CI prerequisite (configured).

## Issues

### CRITICAL (0)
None.

### WARNING (0)
None.

### SUGGESTION (2)
1. The `contract` CI job depends on `ECOSYSTEM_REPO_TOKEN` and govcore main carrying the seam — both now satisfied, but document the cross-repo merge-order in a CONTRIBUTING note so future govcore facade changes do not silently break the orch contract job.
2. Track the deferred `runApplyPhase` critic coverage and durable concern persistence as explicit backlog items so the SSE-only MVP boundary stays visible post-archive.
