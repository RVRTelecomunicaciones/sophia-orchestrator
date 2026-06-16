# Proposal: governance-advisory

> Cross-repo change. Repos: `agent-governance-core` (GAP A) + `sophia-orchestator` (GAP B). Source: Sophia V4.1 §16 M4+. Explore: `sdd/governance-advisory/explore` (obs #909, **GAP A premise was wrong — corrected here**). Verification: obs #913.

## Intent

**GAP A — the facade already ships; the only real gap is an unverified, undrift-locked contract.** Direct code inspection (obs #913) overturns the explore-phase claim that orch would 404. The `/governance/v1/decisions/phase`, `/governance/v1/decisions/sensitive`, and `GET /governance/v1/approvals/{cid}/{pid}/status` facade ALREADY EXISTS and is wired in governance-core ("M-E0 govdecisions": `router.go:142-144`, `decisions_handler.go`, `application/govdecisions/service.go`, `wire.go:207`). Contracts MATCH byte-for-byte (`decisionPhaseRequest{change_id, phase_type, task_description, sensitive}` == orch client request). Today the facade is default-allow + audit (only ever emits `allow`, so orch enum mapping is identity). The risk is NOT a missing build — it is that each side is tested in isolation (orch wire-contract uses a mock; governance-core has handler tests) and **nothing proves them wired together end-to-end**. In a CI-enforced wire-checksum culture, that compatibility can silently drift.

**GAP B — lay the advisory skeleton on the primitive that already exists.** Orch has `PhaseStatusDoneWithConcerns` and the wire-v1 `phase.completed_with_concerns` SSE event, but nothing produces concerns. This wires a strictly-advisory, never-escalating critic into that channel.

## Scope

### In Scope
- **GAP A — ONE cross-repo end-to-end integration test.** Prove orch's REAL governance client talks to governance-core's REAL `/governance/v1/*` facade: no 404, correct decision mapping (`allow` today), and the approval-status path. LOCK that compatibility against future drift. Both repos share the `go.work` workspace, enabling a live-wire test. Decide host: orch `test/` integration harness or a dedicated cross-repo harness.
- **GAP B — orch critic plumbing.** Critic outbound port; domain `Concern{severity, category, message, evidence}` type; per-change opt-in config (DEFAULT OFF); **deterministic stub critic** invoked post-dispatch / pre-terminal on ALL enveloped phases when opted in; SSE concern payload wired into the existing `phase.completed_with_concerns`.

### Out of Scope (Non-Goals)
- **Rebuilding or modifying the governance-core facade** — it already ships and matches; this change does NOT touch it.
- **The `constrain` → `allow_with_constraints` enum mapping** — no producer emits non-`allow` yet; speculative. Record as follow-up.
- Real LLM critic invocation; critic escalation to blocking / `require_approval` (advisory FOREVER here); per-phase opt-in UX.
- Orch governance client changes (it already works).
- Touching memory-engine consolidation (D-M2-12 stays ME-scoped).

## Capabilities

### New Capabilities
- `advisory-critic` (orch): critic port, `Concern` domain type, per-change opt-in (default off), deterministic stub critic post-dispatch/pre-terminal, SSE concern payload via existing `phase.completed_with_concerns` (GAP B).

### Modified Capabilities
- `governance-integration-contract` (**reframed from "build facade"**): the orch↔governance-core `/governance/v1/*` contract is ALREADY implemented and matching; this change VERIFIES and LOCKS it via a cross-repo e2e test. No production code in either service changes (GAP A).

## Approach

- **GAP A — verify + lock, do NOT build.** Stand up orch's real governance client against a live (or in-process) governance-core facade in the shared `go.work` workspace. Assert: phase decision → `allow`, sensitive decision, and approval-status path all succeed without 404, with correct mapping. The test becomes the cross-repo drift guard the isolated tests cannot provide.
- **GAP B — orch pipeline, deterministic stub.** Critic runs post-dispatch / pre-terminal where the envelope exists; emits non-blocking `Concern`s into `done_with_concerns` + SSE. Zero decision authority; respects D1.1 (advisory ≠ policy) and Iron-Law determinism (opt-in default OFF = byte-identical to today).

## Affected Areas

| Area | Impact | Description |
|------|--------|-------------|
| orch `test/` (or cross-repo harness) | New | e2e test: real orch client → live govcore facade → decision (GAP A) |
| `sophia-orchestator/internal/ports/outbound` | New | critic port (GAP B) |
| `sophia-orchestator/internal/domain/phase` | New | `Concern` type; stub critic wiring post-dispatch/pre-terminal |
| `sophia-orchestator` config | New | per-change opt-in flag (default off) |
| `sophia-orchestator` SSE emission | Modified | `phase.completed_with_concerns.concerns` payload |
| `agent-governance-core` production code | **None** | facade already ships; untouched |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| Cross-repo contract drift (independent CI + wire checksum) | High | The e2e test locks orch client ↔ live facade compatibility, closing the gap the isolated tests leave open |
| e2e test flakiness / wiring complexity across `go.work` | Med | Prefer in-process facade wiring; keep the test hermetic and deterministic |
| Critic scope-creep into blocking gate (violates D1.1) | Med | Advisory-only HARD constraint; deterministic stub has zero decision authority |
| Opt-in undermines determinism | Low | Default OFF; opted-out = byte-identical to today |

## Rollback Plan

- GAP A: revert the e2e test only — no production code changed in either repo, so rollback is inert.
- GAP B: revert the orch PR; opt-in default OFF means any in-between state is already inert.

## Dependencies / PR Chaining

- GAP A (test) and GAP B (critic) are **INDEPENDENT** — separate PRs, **no chaining**.
- GAP A test may live in orch `test/` or a dedicated cross-repo harness (both repos in `go.work`).
- GAP B depends only on the existing `done_with_concerns`/SSE primitive, NOT on GAP A.

## Success Criteria

- [ ] **GAP A**: an e2e test exercises the real orch governance client → live govcore `/governance/v1/*` facade → decision (`allow`) + approval-status path, no 404, correct mapping — and it is GREEN.
- [ ] **GAP B (opted out)**: default behavior is **byte-identical to today** — no concerns, no behavior change.
- [ ] **GAP B (opted in)**: a change emits `phase.completed_with_concerns` with stub `Concern`s in the SSE payload, and concerns NEVER block or escalate.
- [ ] Orch's `test/wirecontract/governance_test.go` stays green and unchanged.
