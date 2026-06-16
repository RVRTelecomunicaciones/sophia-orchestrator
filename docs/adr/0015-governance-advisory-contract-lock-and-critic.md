# ADR 0015: Governance integration contract-lock + advisory critic

- **Status:** accepted
- **Date:** 2026-06-16
- **Deciders:** Russell (operator)
- **Supersedes:** none. Builds on ADR-0002 (dispatcher abstraction), ADR-0003 (memory-engine integration boundary), and the V4.1 §16 M4+ roadmap.

## Context

The Sophia V4.1 §16 M4+ backlog named two governance-adjacent gaps for the
orchestrator (`sophia-orchestator`) and governance-core
(`agent-governance-core`):

- **GAP A** — the orch→govcore governance decision contract. The explore phase
  (engram obs #909) claimed orch would 404 against govcore because the
  `/governance/v1/*` facade did not exist, framing GAP A as "build the facade".
- **GAP B** — an advisory critic. Orch already had the
  `PhaseStatusDoneWithConcerns` status and the wire-v1
  `phase.completed_with_concerns` SSE event, but nothing produced concerns.

A **mid-flight scope correction** (proposal obs #910 corrected, verification
obs #913) overturned the GAP A premise. Direct code inspection proved the
`/governance/v1/{decisions/phase,decisions/sensitive,approvals/{cid}/{pid}/status}`
facade ALREADY ships and is wired in govcore (`router.go:142-144`,
`decisions_handler.go`, `application/govdecisions/service.go`, `wire.go:207`),
and its DTOs match orch's pinned wire client (`client.go:72-75`) byte-for-byte.
The facade is today default-allow + audit (only ever emits `allow`), so the orch
enum mapping is the identity map. The REAL gap was not a missing build — it was
that each side is tested only in isolation (orch uses a mock; govcore has handler
tests), so the matching contract could silently drift under independent CI +
wire-checksum enforcement. GAP A was therefore reframed from "build facade" to
"verify + lock the existing contract".

## Decision

1. **GAP A — verify + lock, do NOT build.** Add ONE cross-repo end-to-end
   integration test that drives orch's REAL `governance.Client` against govcore's
   REAL `/governance/v1/*` facade — no mocks on either side — and locks the three
   paths (phase decision, sensitive decision, approval-status) against drift. Zero
   production code changes in either repo. The stale "build a facade"
   `governance-decision-facade` capability was DROPPED as a false premise and is
   NOT resurrected. The capability is `governance-integration-contract`.

2. **GAP B — advisory critic on the existing primitive.** Add an injectable
   `outbound.CriticPort`, a pure domain `phase.Concern{Severity,Category,Message,Evidence}`
   type, a deterministic `StubCritic`, and a per-change opt-in flag
   (`ContextOverrides[scope][critic_enabled]`, DEFAULT OFF). The critic runs in
   `runAsync` after `Validator.Validate` and before `p.Complete`, upgrading
   `DONE → DONE_WITH_CONCERNS` only (never downgrading `BLOCKED`/`NEEDS_CONTEXT`),
   and folds concerns into the existing `phase.completed_with_concerns` SSE event
   via an `omitempty` `Concerns` payload. It is strictly advisory — zero decision
   authority, never escalates, errors are logged and swallowed. Opted-out is
   byte-identical to today. The capability is `advisory-critic`.

## Consequences

### Positive

- The orch↔govcore governance contract now has a real cross-repo regression
  guard (`test/integration/governance_contract_lock_test.go`, `//go:build contract`),
  closing the drift gap the isolated tests left open. CI run 27607068239
  (`contract` job) is green.
- Orch can surface advisory `Concern`s on the existing SSE channel with zero
  blocking risk and a clean swap point for a future real LLM critic.
- Determinism (Iron Law / CLAUDE.md rule 5) is preserved: opted-out behavior is
  byte-identical; the stub uses no clock/random.

### Negative

- The `contract` CI job depends on the `ECOSYSTEM_REPO_TOKEN` secret AND on
  govcore main carrying the `govhttptest` seam. A cross-repo merge-order exists:
  the govcore seam (PR #10) must land BEFORE the orch contract job can pass.
- govcore's module path (`github.com/russellcxl/agent-governance-core`) does not
  match its remote (`RVRTelecomunicaciones`), blocking a `go.mod require`; the
  test resolves the dependency via `go.work` + the public `govhttptest` seam only.

### Neutral

- The advisory critic MVP covers single-agent enveloped phases only and persists
  concerns via SSE only; both are explicit MVP boundaries, tracked below.
- The `constrain → allow_with_constraints` enum mapping has no code because no
  producer emits a non-`allow` decision today (identity map).

## Deferred follow-ups (tracked backlog)

1. **Real LLM critic** — replace the deterministic `StubCritic` with a real LLM
   critic via the OpenCode dispatcher (non-goal here, deferred).
2. **`runApplyPhase` critic coverage** — extend critic to the apply phase
   (emits the slimmer `PhaseCompletedFromApplyPayload`); this change covers
   single-agent enveloped phases only. (Verify SUGGESTION 2.)
3. **Durable concern persistence** — the MVP is SSE-only (`done_with_concerns`
   persists on the phase row; `Concern` detail rides SSE). Add durable storage
   for post-hoc operator review. (Verify SUGGESTION 2.)
4. **Enum mapping `constrain → allow_with_constraints`** — NON-GOAL; add only
   when govcore starts emitting non-`allow` decisions.
5. **govcore module-path vs remote mismatch** — `github.com/russellcxl/agent-governance-core`
   vs the `RVRTelecomunicaciones` remote blocks cross-module `go.mod require`.
   Ecosystem cleanup, govcore-owned. The contract test works around it via
   `go.work` + the `ECOSYSTEM_REPO_TOKEN`-gated `contract` CI job.
6. **Document the cross-repo merge-order** — a CONTRIBUTING note must state that a
   govcore facade/seam change must land before the orch `contract` job, so future
   govcore changes do not silently break the orch contract job. (Verify SUGGESTION 1.)

## Alternatives considered

- **Build the facade mapping (proposal's original GAP A assumption)** — rejected:
  it duplicates already-shipped govcore code and reopens a stable surface. Direct
  inspection proved the facade already exists and matches.
- **Live HTTP roundtrip for the contract test** — acceptable but rejected in favor
  of in-process wiring via the `govhttptest` seam for hermeticity and determinism.
- **`Concern` in `domain/envelope`** — rejected: concerns are orch-side advisory
  metadata, not agent-declared envelope fields; keeping them in `phase` avoids
  polluting the agent contract.
- **Critic in the application layer only / inside `p.Complete`** — rejected: a
  swappable outbound port is needed for stub-now / LLM-later, and the domain must
  stay free of ports/IO.
- **Global env opt-in flag / per-phase opt-in matrix** — rejected: the proposal
  requires per-change opt-in; a per-phase matrix is an explicit non-goal.
- **Random/sampled stub concerns** — rejected: non-deterministic, violates the
  Iron-Law determinism guarantee and CLAUDE.md rule 5.
