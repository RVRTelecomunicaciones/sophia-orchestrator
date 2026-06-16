# Design: governance-advisory

> Cross-repo. GAP A = `agent-governance-core`; GAP B = `sophia-orchestator`. Reads proposal + explore (#909). Decision IDs: D-GA-x.

## Technical Approach

GAP A (facade) is **already implemented** in governance-core: `/governance/v1/{decisions/phase,decisions/sensitive,approvals/{cid}/{pid}/status}` exist via `decisions_handler.go` + router (`/governance/v1` route group) + `application/govdecisions` (M-E0 default-allow + audit) + pg repos + bootstrap `WithPhaseDecisions`. DTOs already match the orch pinned wire contract byte-for-byte. So GAP A is **verification + documentation**, not a build. GAP B (advisory critic) is genuinely net-new in orch: an injectable `CriticPort` + domain `Concern` type, a deterministic stub impl, per-change opt-in (default OFF), invoked post-envelope/pre-`p.Complete()` in `runAsync`, folding concerns into the existing `done_with_concerns` status + `phase.completed_with_concerns` SSE event.

## Architecture Decisions

### D-GA-1 — Facade already exists; reconcile by verification, not Submit/Route/EvaluatePolicy mapping

| Option | Tradeoff | Decision |
|--------|----------|----------|
| Build facade mapping phase-eval → Submit+Route+EvaluatePolicy (proposal's assumption) | Duplicates shipped code; reopens a stable surface | Reject |
| Keep shipped `govdecisions` default-allow + audit facade; add an end-to-end orch↔gov integration check | Honors "facade owns contract, orch unchanged"; zero churn | **Choose** |

**Rationale**: `decisions_handler.go` DTOs (`decisionPhaseRequest/decisionPayload/approvalStatusPayload`) are identical to orch's `client.go` wire shapes; `toDecision` passes `decision` through verbatim. The orch wire-contract test (`test/wirecontract/governance_test.go`) only pins the 3 paths + `decision` field — already satisfied. Enum mapping is the **identity map**: governance emits `allow|deny|require_approval` (today only `allow`); orch accepts `allow|allow_with_constraints|require_approval|deny` and does NOT translate, so `constrain→allow_with_constraints` needs no code until governance starts emitting non-allow — captured as an open question for tasks, not built here. Approval-status path returns `granted` when no row exists (V1 default). Remaining work: an integration/wire check proving orch's real `Client` succeeds against a live facade (no 404, correct decode).

### D-GA-2 — Critic port + Concern type in orch

**Choice**: New outbound port `outbound.CriticPort interface { Review(ctx, CriticInput) ([]phase.Concern, error) }` where `CriticInput{ChangeID, PhaseType, Envelope *envelope.Envelope}`. New domain type `phase.Concern{Severity, Category, Message, Evidence string}` in `internal/domain/phase/concern.go` (same package as `status.go`, no new import cycles — envelope already imported by phase). Concerns attach to the phase via a new `phase.Phase.SetConcerns([]Concern)` setter and ride the SSE payload (D-GA-6). Port lives in `internal/ports/outbound/critic.go`.

**Alternatives**: Concern in `domain/envelope` (rejected — concerns are orch-side advisory metadata, not agent-declared envelope fields; keeping them in `phase` avoids polluting the agent contract). Critic in application layer only (rejected — needs a swappable port for stub-now / LLM-later).

**Rationale**: Mirrors the existing `GovernanceClient`/`AgentDispatcher` outbound-port pattern; injectable for fake-based strict-TDD; future OpenCode LLM impl drops in with no pipeline change.

### D-GA-3 — Deterministic stub critic (production impl for this change)

**Choice**: `adapters/outbound/critic/StubCritic` returns concerns derived **purely from envelope contents** — no `time.Now()`, no random. Rules (deterministic, reproducible): each `envelope.Risk{Level:"high"}` → one `Concern{Severity:"high", Category:"risk", Message, Evidence:Risk.Description}`; `Confidence < 0.5` → one `Concern{Severity:"medium", Category:"confidence", Evidence:fmt(confidence)}`. Empty/clean envelope → empty slice (→ stays `DONE`). Same envelope in ⇒ identical concerns out.

**Alternatives**: Random/sampled concerns (rejected — non-deterministic, violates Iron-Law determinism + CLAUDE.md rule 5). No-op stub (rejected — would not exercise the SSE/`done_with_concerns` path).

**Rationale**: Reproducible, table-testable, zero decision authority, negligible cost; satisfies "advisory skeleton on a real signal."

### D-GA-4 — Per-change opt-in, default OFF

**Choice**: Flag at `ContextOverrides["scope"]["critic_enabled"] bool` (mirrors the shipped `scope.tests_required` pattern). A `parseScopeCriticEnabled(in.ContextOverrides) bool` helper (default `false`) gates the call. Bootstrap wires `Deps.Critic outbound.CriticPort` (nil-tolerant, like `Skills`/`SkillUsageRepo`). When flag is false OR `Deps.Critic == nil` → critic is never called; path is **byte-identical to today**.

**Alternatives**: Global env flag (rejected — proposal wants per-change). Per-phase matrix (rejected — explicit non-goal).

**Rationale**: Determinism stays the default; opt-out is the inert no-op path.

### D-GA-5 — Pipeline integration point

**Choice**: Insert in `runAsync` **after** `Validator.Validate` succeeds (envelope exists, ~service.go:595) and **before** `p.Complete(env, now)` (:618). If opted-in and `Critic != nil`: call `Review`; on **error**, log + swallow (advisory must never break a phase); on concerns, coerce `env.Status` `DONE → DONE_WITH_CONCERNS` (only upgrades; never downgrades `BLOCKED`/`NEEDS_CONTEXT`) and `p.SetConcerns(...)` so `p.Complete` derives `PhaseStatusDoneWithConcerns` via the existing switch (`phase.go:133`). `AdvanceAllowed()` already returns true for `done_with_concerns` → **non-blocking confirmed**. Applies to all enveloped single-agent phases; apply-phase (`runApplyPhase`) is out of this slice (open question for tasks).

**Alternatives**: Run pre-dispatch (rejected — no envelope yet). Run inside `p.Complete` domain method (rejected — domain must stay free of ports/IO).

**Rationale**: This is the only point where the envelope exists and the terminal status is still mutable; coercion reuses the existing concerns→status machinery untouched.

### D-GA-6 — SSE payload

**Choice**: Add `Concerns []ConcernPayload `json:"concerns,omitempty"`` to `inbound.PhaseCompletedPayload` (`event_payloads.go:348`); `ConcernPayload{Severity,Category,Message,Evidence}`. Populated only when `eventTypeForStatus` resolves to `phase.completed_with_concerns`; `omitempty` keeps the wire **identical** for plain `phase.completed`. Matches wire-v1 §419 (`concerns` field already documented in the doc).

**Alternatives**: New event type (rejected — wire-v1 already defines `phase.completed_with_concerns` with a `concerns` field). Separate concerns stream (rejected — overkill).

**Rationale**: Doc-conformant, additive, opted-out emits byte-identical events.

## Data Flow

    [GAP B] dispatch → envelope → Validate ─→ (opt-in?) Critic.Review(env)
                                                  │ concerns
                                                  ▼ coerce DONE→DONE_WITH_CONCERNS
                                       p.SetConcerns → p.Complete → Save
                                                  │
                                                  ▼ phase.completed_with_concerns{concerns[]}
    [GAP A] orch Client ──POST /governance/v1/decisions/phase──▶ govdecisions (default-allow + audit row)

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `agent-governance-core/...` (GAP A) | None | Facade already shipped; verify only |
| `sophia-orchestator/internal/domain/phase/concern.go` | Create | `Concern` type + `Phase.SetConcerns` |
| `sophia-orchestator/internal/ports/outbound/critic.go` | Create | `CriticPort`, `CriticInput` |
| `sophia-orchestator/internal/adapters/outbound/critic/stub.go` | Create | Deterministic `StubCritic` |
| `sophia-orchestator/internal/application/phase/service.go` | Modify | `Deps.Critic`; opt-in parse; insert critic call pre-Complete; status coercion |
| `sophia-orchestator/internal/ports/inbound/event_payloads.go` | Modify | `Concerns` on `PhaseCompletedPayload` + `ConcernPayload` |
| `sophia-orchestator/internal/bootstrap/wire.go` | Modify | Wire `Deps.Critic` (nil-tolerant) |
| `sophia-orchestator/test/...` | Create | orch↔gov integration/wire check (GAP A) |

## Interfaces / Contracts

```go
// domain/phase
type Concern struct{ Severity, Category, Message, Evidence string }
// ports/outbound
type CriticInput struct { ChangeID ids.ChangeID; PhaseType phase.PhaseType; Envelope *envelope.Envelope }
type CriticPort interface { Review(ctx context.Context, in CriticInput) ([]phase.Concern, error) }
```

## Testing Strategy

| Layer | What | Approach |
|-------|------|----------|
| Unit | StubCritic determinism | Table tests: same envelope ⇒ identical concerns; clean ⇒ empty |
| Unit | Status coercion | DONE+concern→DONE_WITH_CONCERNS; BLOCKED unchanged; advance still allowed |
| Unit | Opt-in/default-off | flag false / Critic nil ⇒ critic never called, no concerns, status unchanged |
| Unit | Critic error swallowed | Review error ⇒ phase still completes DONE |
| Integration | SSE payload | opted-in emits `concerns`; opted-out byte-identical |
| Integration | GAP A | orch `Client` → live facade: no 404, decode ok, 3 paths |

## Migration / Rollout

No data migration. GAP A repos/migrations already shipped. GAP B is additive + default-OFF; rollback = revert orch PR (in-between state inert). GAP A and GAP B are independent PRs, no chaining.

## Open Questions

- [ ] Defer `constrain→allow_with_constraints` enum translation until governance emits non-allow? (no code now)
- [ ] Include apply-phase (`runApplyPhase`) in critic coverage, or single-agent phases only this slice?
- [ ] Concern persistence: SSE-only, or also persist on the phase row/envelope for post-hoc operator review?
