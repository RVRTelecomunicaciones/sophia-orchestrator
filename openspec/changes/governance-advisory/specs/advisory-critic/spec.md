# advisory-critic Specification

> Repo: **sophia-orchestator** (GAP B). Builds on the already-existing
> `PhaseStatusDoneWithConcerns` (`internal/domain/phase/status.go:11`) and the
> wire-v1 `phase.completed_with_concerns` SSE event
> (`docs/specs/sophia-wire-v1.md:419`), which have no spec file. Their
> concern-payload behavior is specified here.

## Purpose

Wire a strictly-advisory critic into the orch phase pipeline. The critic emits non-blocking `Concern`s into the existing `done_with_concerns` status and `phase.completed_with_concerns` SSE channel. It is per-change opt-in, DEFAULT OFF, and the MVP is a deterministic stub (no LLM call — deferred). Determinism (Iron Law) is preserved: an opted-out change behaves byte-identically to today.

## Requirements

### Requirement: Concern domain type

The orchestrator MUST define a domain type `Concern{severity, category, message, evidence}`. A `Concern` is informational only and carries NO decision authority.

#### Scenario: Concern is a pure value

- GIVEN the critic produces a finding
- WHEN it is represented as a `Concern`
- THEN it has `severity`, `category`, `message`, and `evidence` fields and no field that can gate, block, or escalate a phase

### Requirement: Critic is strictly advisory and non-blocking (HARD constraint)

The critic MUST NOT block a phase, MUST NOT change a phase to a failing/blocked state, and MUST NEVER escalate to a HARD-GATE or governance `require_approval`, regardless of concern `severity`. Cycle progression MUST continue whenever a phase would otherwise advance.

#### Scenario: No severity blocks or escalates

- GIVEN an opted-in phase produces concerns of the highest severity
- WHEN the phase terminates
- THEN the phase status is `done_with_concerns` (terminal, advance-allowed)
- AND no governance decision, HARD-GATE, or `require_approval` is triggered by the concern
- AND the cycle advances to the next phase exactly as a plain `done` would

#### Scenario: Critic stub error must not break the phase

- GIVEN the deterministic stub critic itself errors during evaluation
- WHEN the phase pipeline handles that error
- THEN the phase completes on its underlying outcome (e.g. `done`) with the critic contributing no concerns
- AND the error is non-fatal because the critic is advisory

### Requirement: Per-change opt-in, default OFF (determinism invariant)

Critic execution MUST be gated by a per-change opt-in flag whose DEFAULT is OFF. An opted-out change MUST be byte-identical to today: no critic invocation, no concerns, no SSE concern payload, and an unaffected phase status.

#### Scenario: Opted-out change is byte-identical to today (default)

- GIVEN a change with the critic flag unset (default OFF)
- WHEN every phase runs to completion
- THEN no `Concern` is produced and no critic code path executes
- AND phase statuses and emitted SSE events are identical to the pre-change behavior

#### Scenario: Opted-in change runs the critic on all enveloped phases

- GIVEN a change explicitly opted in
- WHEN any phase that produces an envelope reaches the critic stage
- THEN the deterministic stub critic runs on that phase's envelope
- AND it runs on ALL phases that produce an envelope for that change

### Requirement: Critic runs post-dispatch, pre-terminal in the phase pipeline

When opted in, the critic MUST run after dispatch (where the phase envelope exists) and before the phase reaches its terminal state. The MVP critic MUST be a DETERMINISTIC STUB — no real LLM call (a real LLM critic is a non-goal / deferred).

#### Scenario: Deterministic stub produces concerns on an enveloped phase

- GIVEN an opted-in phase that has produced an envelope post-dispatch
- WHEN the deterministic stub critic evaluates the envelope pre-terminal
- THEN it emits zero or more `Concern`s deterministically (same envelope ⇒ same concerns)
- AND if at least one concern is emitted the phase terminates as `done_with_concerns`

#### Scenario: Opted-in with zero concerns

- GIVEN an opted-in phase whose stub evaluation yields no concerns
- WHEN the phase terminates
- THEN the phase status is plain `done` (not `done_with_concerns`)
- AND no `phase.completed_with_concerns` event is emitted for that phase

### Requirement: SSE concern payload via phase.completed_with_concerns

When a phase terminates `done_with_concerns`, the orchestrator MUST emit the existing `phase.completed_with_concerns` SSE event with payload `{phase_id, phase_type, ended_at, confidence, concerns}`, where `concerns` is the list of emitted `Concern`s. `phase_id` MUST equal the `phase_id` of the stream's opening GET.

#### Scenario: Concerns carried on the SSE payload

- GIVEN an opted-in phase terminated `done_with_concerns` with one or more concerns
- WHEN the SSE stream emits the terminal event
- THEN it is `phase.completed_with_concerns` carrying `concerns` with each `{severity, category, message, evidence}`
- AND clients that ignore the event still see the cycle progress (skipping is safe)

### Requirement: governance-core unavailability does not implicate the critic

The advisory critic MUST be independent of GAP A. If governance-core is down or returns errors, the orchestrator's existing IL4 governance behavior applies to the phase; the critic neither compensates for nor is affected by governance availability.

#### Scenario: governance-core down

- GIVEN governance-core is unreachable
- WHEN an opted-in phase runs
- THEN governance failure is handled by the existing IL4 path (phase fails closed)
- AND the critic does not alter that outcome and raises no governance-related concern
