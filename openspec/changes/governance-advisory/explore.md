# Exploration: governance-advisory cluster (Sophia M4+)

> SDD explore artifact. Engram topic: `sdd/governance-advisory/explore` (obs #909, project 2026).
> Source: Sophia V4.1 §16 M4+ backlog ("governance+advisory cluster — LLM critic opt-in, governance-core HTTP").

## Source-of-truth intent
The cluster derives from the **Sophia V4.1 §16 milestone roadmap**, specifically the M4+ backlog. Two prior archived changes name it verbatim:
- `sophia-orchestator/openspec/changes/priorcontext-enrichment/archive.md:105,173` → "governance-core HTTP surface — Out of scope. M4+ dedicated governance API"; "LLM critic opt-in — M4 with governed opt-in, off by default."
- `priorcontext-enrichment/explore.md:32` → item 9 "LLM critic opt-in ~400 [LoC]. NOT in M3 criteria. D-M2-12 lint guard explicitly retained."
- `context7-bootstrap/archive.md:99` → "Governance + advisory (items 6+10): LLM critic opt-in, governance-core HTTP surface."

Authoritative cross-repo gap doc: `sophia-orchestator/docs/research/sophia-surface-inventory.md §7`.

## Current State (file:line evidence)

### agent-governance-core — MATURE HTTP service, NOT a stub
- Real chi v5 REST API, 15 endpoints under `/api/v1/*` (README.md:75-92). HTTP handlers: `internal/adapters/inbound/http/task_handler.go`, `approval_handler.go`, `audit_handler.go`, `escalation_handler.go`.
- Entrypoint `cmd/agent-governance-core/main.go`; PORT 8080 default (README.md:115). PostgreSQL persistence (pgx v5), 8 repos, 488 tests, v0.6.0 shipped.
- Models real DECISIONS: policy (allow/deny/constrain/require_approval) `internal/domain/policy/`; routing (score-based) `internal/domain/routing/`; approval gates (HITL) `internal/domain/approval/`; workflow state machine; escalation; audit; resilience (circuit breaker, kill switch).
- Inbound port `internal/ports/inbound/governance_service.go`: SubmitTask, ProcessTask, RouteTask, EvaluatePolicy, StartWorkflow.
- Its real endpoints are TASK-oriented: `POST /api/v1/tasks/{id}/evaluate-policy`, `/route`, `/process`; `GET /api/v1/approvals/pending`; `POST /api/v1/approvals/{id}/resolve`.
- Roadmap `docs/superpowers/roadmap/2026-04-16-post-v0.6.0.md`: next is Hardening → Breaker→Routing feedback → Approvals UX → Security. **No "critic"/"advisory" concept anywhere in governance-core** (the only matches were `riskCritical*` rules, e.g. `internal/domain/policy/rules.go:22`).

### orchestrator — governance gate is REAL and enforced (not stubbed), but against a DIFFERENT assumed contract
- Outbound port `sophia-orchestator/internal/ports/outbound/governance.go`: `GovernanceClient{EvaluatePhase, AwaitApproval, EvaluateSensitiveAction}`; decision set allow/allow_with_constraints/require_approval/deny.
- HTTP client `internal/adapters/outbound/governance/client.go` calls: `POST /governance/v1/decisions/phase`, `POST /governance/v1/decisions/sensitive`, `GET /governance/v1/approvals/{cid}/{pid}/status`.
- Wired live in `internal/bootstrap/wire.go:123` (real client, not a fake). Wire-contract test `test/wirecontract/governance_test.go` pins these 3 paths.
- IL4 gate is enforced in the phase pipeline: `internal/application/phase/service.go:384-403` — `Governance.EvaluatePhase()` runs before dispatch; `deny` fails the phase, `require_approval` pauses. Iron Law catalog `internal/domain/ironlaw/laws.go:15` IL4 "No runtime call without governance decision." Boundary D1.1 is HONORED in orch design.

### The advisory primitive ALREADY EXISTS in orch
- `internal/domain/phase/status.go:11` `PhaseStatusDoneWithConcerns = "done_with_concerns"`; `:38-48 AdvanceAllowed()` doc: "concerns are informational signals the LLM raised about its own output, NOT policy blockers... operator reviews concerns post-hoc by reading the persisted envelope; cycle progression continues."
- Wire-v1 SSE event `phase.completed_with_concerns` (`docs/specs/sophia-wire-v1.md:419`): "orchestrator emits when a phase finishes successfully but Iron Law / governance flagged **advisory concerns**." This is the existing channel a critic would feed.

## The two gaps

### GAP A — "governance-core HTTP" = CROSS-REPO CONTRACT MISMATCH
Orchestrator expects `/governance/v1/decisions/phase|sensitive` + `/governance/v1/approvals/{cid}/{pid}/status`. governance-core exposes `/api/v1/tasks/{id}/evaluate-policy|route|process` + `/api/v1/approvals/...`. **Neither side implements the other's contract** — the orch client would 404 against a live governance-core today. Surface-inventory §7.2:450 confirms "skill lifecycle governance path entirely absent; integration covers only phase + sensitive." The gap is NOT "build governance" (it's built) — it is to RECONCILE the wire contract: either (a) add a `/governance/v1/*` decision facade to governance-core that maps phase-eval → its task/policy pipeline, or (b) retarget the orch client to governance-core's existing `/api/v1/tasks/*` shape, or (c) a thin BFF/adapter. Payloads also differ: orch sends `{change_id, phase_type, task_description, sensitive}`; governance-core's EvaluatePolicy wants a `taskID` + `action` against a previously-submitted task.

### GAP B — "LLM critic opt-in" = NET-NEW, NOTHING EXISTS
- No critic/advisory code in either repo (grep across `/2026` for `\bcritic\b` etc. → only the hermes research skill, `gocritic` lint directives, and `riskCritical`).
- Concept (inferred from refs): a phase-output reviewer that an LLM runs to flag quality/risk on an envelope BEFORE a HARD-GATE, emitting non-blocking advisory concerns (→ `done_with_concerns` / `phase.completed_with_concerns`), distinct from governance's BLOCKING decision.
- "opt-in / off by default" + governed: the D-M2-12 constraint (`sophia-memory-engine/internal/application/consolidation/no_llm_guard_test.go`) bans LLM imports **only in memory-engine's consolidation package** (deterministic learning loop). It does NOT ban LLM use in the orchestrator. So a critic CAN run in orch (which already dispatches LLM agents via OpenCode), it just must NOT live in the consolidation worker.

## Candidate work items

### Item 1 — Governance wire contract reconciliation (GAP A)  [effort: M]
Approaches:
1. **Facade on governance-core** (add `/governance/v1/decisions/phase` mapping to internal Submit+Route+EvaluatePolicy). Pros: orch unchanged, wire-contract test stays green, governance owns its surface. Cons: new endpoints + DTOs in governance-core; must reconcile stateless phase-eval vs governance-core's task-aggregate model (a phase eval implies creating/looking-up a task).
2. **Retarget orch client to `/api/v1/tasks/*`**. Pros: no governance-core change. Cons: breaks orch wire-contract test + port semantics; orch must manage governance task IDs; leaks governance's task model into orch (D1.1 smell).
3. **Thin adapter/BFF**. Pros: isolates mapping. Cons: extra deployable; more ops.

Recommend Approach 1. Cross-repo contract: new governance-core endpoints `POST /governance/v1/decisions/phase`, `/sensitive`, `GET /governance/v1/approvals/{cid}/{pid}/status`; map decision enum (governance `require_approval`/`constrain` ↔ orch `require_approval`/`allow_with_constraints`).

### Item 2 — LLM critic / advisory reviewer (GAP B)  [effort: L]
Sub-items:
- 2a. Critic port + domain advisory type in orch (non-blocking `Concern{severity, category, message, evidence}`), feeding existing `done_with_concerns` + `phase.completed_with_concerns`.  [S-M]
- 2b. Opt-in config + governance gating (flag off by default; granularity per-change / per-phase TBD).  [S]
- 2c. LLM invocation path (reuse OpenCode dispatcher; one extra review call on the phase envelope).  [M]
- 2d. Wire concern payload into SSE `phase.completed_with_concerns.concerns`.  [S]

Approaches for WHERE the critic runs:
1. **In orch phase pipeline** (post-dispatch, pre-terminal). Pros: has the envelope, already LLM-capable, feeds existing advisory channel, respects D1.1 (advisory ≠ policy). Cons: adds latency/cost to every opted-in phase.
2. **In governance-core as an advisory decision type**. Pros: centralizes "judgement." Cons: governance is deterministic/policy by charter — adding LLM there contradicts its design + roadmap; would need its own no-LLM exceptions.
3. **In memory-engine consolidation worker**. REJECTED — D-M2-12 forbids LLM there.

Recommend Approach 1.

## Dependencies / ordering
- Item 1 (wire reconciliation) is independent and unblocks any real orch↔governance traffic; do FIRST.
- Item 2 (critic) depends on the `done_with_concerns`/SSE plumbing (already exists) but NOT on Item 1. Item 2's "governed opt-in" MAY consult governance (Item 1) but can ship advisory-only without it.

## Recommended scope for `governance-advisory`
- **NOW**: Item 1 (contract reconciliation, Approach 1) + Item 2a/2b/2d as an advisory-only MVP (critic emits concerns, off by default, no LLM call yet OR a single guarded LLM call). This delivers the boundary fix + the advisory skeleton.
- **DEFER**: Full LLM critic prompt engineering / multi-pass critique (2c hardening), per-phase opt-in matrix UX, governance-driven critic enablement. Effort for the full cluster is L; the NOW slice is M.

## Open product/architecture questions for the operator (MUST decide before propose)
1. **Blocking vs advisory**: confirm the critic is strictly advisory (non-blocking, `done_with_concerns`) and NEVER becomes a HARD-GATE? Or can a high-severity critic concern escalate to governance `require_approval`?
2. **Where governance contract is owned**: facade on governance-core (Approach 1) vs retarget orch (Approach 2)? This decides which repo's wire-contract test changes.
3. **Critic location**: orch phase pipeline (recommended) — confirm it must NOT touch memory-engine consolidation (D-M2-12).
4. **Opt-in granularity**: global flag / per-change / per-phase-type? And default = off — confirm.
5. **Which phases get critiqued**: all 9, or only high-risk (apply/verify)?
6. **LLM cost/latency budget**: is an extra LLM call per opted-in phase acceptable, or should the MVP ship advisory plumbing with a deterministic/no-LLM stub first?
7. **governance-core appetite**: governance-core's own roadmap (post-v0.6.0) does NOT list this work — does adding a `/governance/v1/*` facade jump its queue, or does orch absorb the mapping?

## Risks
- Cross-repo change touches two repos with independent CI + a CI-enforced wire checksum culture; contract drift is high-risk.
- governance-core roadmap conflict: its team prioritized Hardening, not an orch-facing decision facade.
- "Critic" is under-defined; risk of scope creep into a blocking quality gate that violates D1.1 (orchestrator must not decide policy).
- Adding LLM to a critic re-introduces non-determinism into the SDD pipeline; must stay advisory + opt-in to avoid undermining Iron Law determinism.
- D-M2-12 guard is memory-engine-scoped; misreading it as orch-wide would wrongly block a valid orch critic.
