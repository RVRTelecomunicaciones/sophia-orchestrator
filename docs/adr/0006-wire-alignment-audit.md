# ADR 0006: Wire-Alignment Audit across the Sophia Ecosystem

- **Status:** accepted
- **Date:** 2026-05-14
- **Deciders:** rfactperu

## Context

During the M-E0 milestone ("Real Local OpenCode Execution") E2E test on
2026-05-13 / 14, the orchestator was driven against real downstream
services for the first time — no stubs, no mocks. The smoke revealed
**five distinct wire-contract mismatches** in a single session:

| # | Mismatch | Discovered via |
|---|----------|----------------|
| 1 | orch outbound: `/governance/v1/decisions/phase`<br>gov inbound: `/api/v1/tasks/{id}/evaluate-policy` | governance returned `404 page not found`; phase BLOCKED |
| 2 | orch outbound path: `/api/v1/executions` (plural)<br>runtime inbound path: `/api/v1/execute` (singular) | runtime returned `404`; phase BLOCKED |
| 3 | orch outbound request: `{capability, payload_b64, timeout_ms}`<br>runtime inbound request: 12 fields including `correlation_id` ULID, separated `adapter_id`/`capability_name`/`capability_version`, raw `payload`, `timeout_budget_ms`, `submitted_at` | runtime returned `400` with `decode request: invalid id: expected 26-char ULID` |
| 4 | orch payload inside shell.exec: `{cmd, args, stdin (string), timeout_ms}`<br>runtime ExecPayload: `{command, args, stdin (base64 of []byte), env, working_dir, exit_success}` | runtime returned `400` with `decode payload: json: unknown field "cmd"` |
| 5 | orch dispatcher args: `opencode run --prompt-stdin --output-json --cwd <path>`<br>opencode CLI (current version): `opencode run [--dir <path>] [-m <model>] <message>` (positional, no `--prompt-stdin`, `--format json` replaced `--output-json`) | opencode returned help text to stderr; runtime receipt status=success but no envelope |
| 6 | orch `createWorktrees` shell.exec payload: `{cmd, args, timeout_ms}` (legacy shape) and tried `git worktree add`<br>runtime ExecPayload requires `{command, args, working_dir, ...}`; also no upstream repo exists yet for V1 smoke | runtime returned `validation_failure` receipt; subsequent dispatch failed with `Failed to change directory to /tmp/sophia/worktrees/...` because the dir was never created |
| 7 | orch runtime HTTP client `HTTPTimeout = 30 * time.Second` (legacy default) while the inner `timeout_budget_ms` defaults to `1_800_000` (30 min) for apply<br>The HTTP transport cancels the request after 30s regardless of inner budget | every opencode dispatch returned `status=timeout duration_ms=30038` — exactly the orch-side HTTP timeout, NOT the runtime's |
| 8 | orch dispatcher passed `--dir <worktree>` to opencode<br>opencode permission sandbox honors only the launching shell's actual `cwd`; `--dir` is recorded but NOT propagated to its permission boundary | opencode logged `permission requested: external_directory (...); auto-rejecting` and `read failed`; receipt status=success but no envelope returned. Fixed by setting `working_dir` on the runtime payload so the subprocess is spawned with cwd=worktree |
| 9 | runtime `valueobjects/catalog.go` shell.exec@v1 `DefaultTimeout = 30 * time.Second`<br>`execute_service.go` applies `effective = min(req.timeout_budget, cap.DefaultTimeout, s.maxTimeout)` — the 30s default capped EVERY shell.exec call regardless of requested budget | opencode + LLM responses can take 7–30s; many dispatches timed out at 30037ms with `error_class=timeout`. Bumped capability default to 10 min for AI-CLI-friendly budgets |
| 10 | opencode v1.3.14 permission system: `external_directory` defaults to `"ask"`; subprocess has no TTY → `auto-rejecting` every file access against paths under `/tmp/sophia/worktrees/**`<br>orch dispatcher emitted no config telling opencode the worktree is safe | every read/edit by the LLM failed with `permission requested: external_directory; auto-rejecting`. Closed by injecting an inline `OPENCODE_CONFIG_CONTENT` env var on the runtime ExecPayload with `permission.external_directory[worktreePath/**] = "allow"`. Empirical schema check: only `external_directory` accepts dict-of-patterns; other keys (`read`/`edit`/`webfetch`) make opencode reject the config |

Behind these five gaps, a sixth was uncovered (Anthropic OAuth quota
exhausted on the test machine). That one is not a code issue but it
hid the fact that the rest of the pipeline was already working —
opencode authenticated, called the LLM, and the LLM responded; the
response was just the API-level usage-cap error. Switching the model
to `google/gemini-2.5-flash` (the user's Google OAuth provider in
opencode) closed the EXPLORE phase E2E with a valid `NEEDS_CONTEXT`
envelope returned by Gemini.

A subsequent **APPLY phase smoke** (M-E0 Validation Gap #5,
2026-05-14) surfaced **four more wire-alignment gaps** (rows 6–9
above) that the EXPLORE smoke never exercised because EXPLORE does
not touch the SpawnGovernor, worktrees, or the apply-loop dispatch
path. Each was a distinct boundary problem:

- #6 was inside the orch's apply pipeline — `createWorktrees` had
  not been updated to the new ExecPayload shape and still requested
  `git worktree add` against a non-existent upstream repo.
- #7 was a forgotten knob — the runtime HTTP client used a 30-second
  default that pre-dated AI-CLI integration and clamped every
  request regardless of `timeout_budget_ms`.
- #8 was an opencode-CLI surprise — its permission sandbox is keyed
  off `os.Getwd()` of the subprocess, not the `--dir` flag, so
  setting `working_dir` on the runtime payload is mandatory to grant
  the agent file access to its worktree.
- #9 was inside runtime-adapters — `shell.exec@v1` had a 30-second
  capability default; combined with `effective = min(...)` in
  `execute_service`, that default capped every dispatch.

All five were closed in the same session (commits on the
`m-e0-apply-wire-alignment` branch of orchestator and on
`m-e0-shell-exec-timeout-bump` of runtime-adapters). With gap #10
closed, **M-E0 Validation Gap #5 is fully green**: the apply pipeline
reaches the LLM, the LLM reads and writes files inside the worktree,
the response is parsed into a valid envelope with `status=DONE
confidence=0.85`, and the actual file change persists on disk.

Validation evidence (2026-05-14 04:25):
- Phase `01KRJWV6VJV63QXKKD6T5AR6KF` reached `done` in 25 seconds.
- Task envelope `status=DONE attempts=1` (Iron Law #5 did not fire).
- `/tmp/sophia/worktrees/.../group-1/README.md` written by the agent.
- Phase envelope persisted to `audit_log` via `phase.transitioned`.

### Why this happened

Each service in the Sophia ecosystem was built against **mocks** of
the others. Unit + integration tests in each repo passed because the
mocks faithfully implemented whatever the local team assumed the other
side would expose. There was no cross-repo contract test that exercised
the live pair, so drift accumulated silently:

- Memory-engine V1 GA was verified standalone and behind a stub-memory
  proxy in the orchestator's compose; the orch never spoke to a real
  memory-engine until P0.1 + P0.2 (ADR-0005 Sprint 0).
- Governance-core was tested standalone (488 unit/integration tests)
  but no client of governance was ever spoken with a real running
  governance instance. The orchestator's governance client targets a
  URL prefix (`/governance/v1/decisions/...`) that governance does not
  expose at all.
- Runtime-adapters Phase 1 v0.9.0 has rigorous contract tests for its
  own adapters, but the contract was specified internally, not jointly
  with the orchestator. The 8 fields the runtime requires were unknown
  to the orchestator's adapter author.
- The opencode dispatcher in the orchestator was built against opencode
  CLI flags from a tutorial / older spec. The real opencode CLI evolved
  to a different flag set (and a positional message instead of stdin).

In short: **"cross-repo wire contract"** was declarative in `ADR-0005`
and the V1 GA spec, but enforced in zero places. M-E0 #4 is the first
real exercise of the contracts and the natural surface where the drift
becomes visible.

### Scope of damage and recovery (all fixed during M-E0)

All five gaps were closed in this session. The orchestator → runtime →
opencode → LLM round-trip works end-to-end. Each fix is in a pushed
branch and reachable from this ADR's follow-up table.

## Decision

We commit to a **wire-alignment audit** across the entire Sophia
ecosystem, scoped as the next milestone (Sprint 3 in the existing
roadmap or a dedicated **M-WA1**). The audit has three deliverables.

### 1. Wire-Alignment Matrix

A markdown matrix in `docs/architecture/wire-contract-matrix.md`
listing every pair of services that talk to each other, the exact
endpoints / payload shapes on both sides, and the verification
status (mocked / live-tested / contract-tested). Format:

| Producer | Consumer | Endpoint(s) | Wire shape source | Verified? |
|---|---|---|---|---|
| orch | memory-engine | POST /api/v1/memories, GET /by-topic-key, ... | M-E0 P0.2 + adapter | ✅ live (P0.1+P0.2 E2E) |
| orch | governance | POST /governance/v1/decisions/phase, ... | M-E0 gov branch | ✅ live (M-E0 #4 explore phase) |
| orch | runtime-adapters | POST /api/v1/execute | M-E0 wire-alignment fix | ✅ live (M-E0 #4 explore phase) |
| dispatcher | opencode CLI | argv | M-E0 opencode flag fix | ✅ live (M-E0 #4 explore phase) |
| cli | orch | POST /api/v1/changes, SSE phases events | sophia-cli e2e_smoke | ⚠️ mocked? (verify) |
| governance | memory-engine | GET /memories/by-topic-key (if used) | ? | 🔲 unaudited |
| runtime-adapters | memory-engine | (Phase 2+ planned) | ? | 🔲 out-of-scope for V1 |
| ... | ... | ... | ... | ... |

### 2. Cross-Repo Contract Tests

A dedicated test workflow (or set of integration tests) that boots a
**pair of real services in CI** (via testcontainers + docker compose
ephemeral) and exercises representative requests. The orch ↔ memory
pair already has this conceptually via the apply-phase smoke; the
remaining pairs are uncovered.

Sprint scope for this:
- **High-priority pairs** (used in the SDD critical path):
  - orch ↔ memory ✅ done (P0.1+P0.2)
  - orch ↔ governance ✅ now done (M-E0)
  - orch ↔ runtime ✅ now done (M-E0)
  - dispatcher ↔ opencode ✅ now done (M-E0)
- **Medium-priority pairs** (used in observability + ops):
  - cli ↔ orch (SSE event stream shapes)
  - governance ↔ memory (if governance writes audit to memory)
- **Low-priority pairs** (Phase 2 planned, not in V1):
  - runtime-adapters → memory-engine (for capability outputs)
  - cross-tenant: orch → governance with tenant routing (Sprint 4 SaaS)

### 3. Ownership and Drift Detection

- Every cross-repo endpoint pair has a **named owner** (a person or
  repo). The owner is responsible for keeping the contract in sync.
- The wire-contract matrix is updated whenever a new endpoint pair is
  introduced.
- Contract drift becomes a CI gate: the contract test must pass before
  the producer or consumer can merge a change that touches the wire.

## Consequences

### Positive

- Reduces silent breakage. Future cross-repo changes will be caught
  by contract tests before they ship.
- Documents the **actual** wire contracts, not the aspirational ones
  in the V1 GA spec.
- Establishes a clear ownership model — when something breaks at the
  boundary, there's an explicit person to ping.

### Negative

- Adds CI complexity and runtime cost (boot 2+ services per pair).
- The matrix becomes a maintenance burden if it grows past ~20 pairs.
- Existing tests that relied on mocks need to be re-evaluated — some
  may become redundant once their contract test exists.

### Neutral

- The five gaps fixed in M-E0 establish a precedent for what
  "wire-alignment fix" looks like (small, focused PR with a clear
  before/after diff on the wire shape).

## Alternatives considered

- **OpenAPI-driven generation**: generate clients from the producer's
  OpenAPI spec. Rejected for V1 because (a) only memory-engine has an
  OpenAPI spec today (ADR-0005 P2.1), and (b) generation introduces
  its own complexity. We may revisit in Sprint 4 once more services
  have specs.
- **Replace the orch's hand-written clients with gRPC stubs**:
  rejected as too disruptive for the current sprint cycle. gRPC is
  reasonable for a future major version but not for V1.5.
- **Defer the audit until SaaS**: rejected because the next milestone
  (SaaS-readiness, ADR-0007 forthcoming) will introduce JWT auth,
  multi-tenancy, and rate limiting — all of which add new wire fields.
  Auditing the existing contracts first prevents compounding drift.

## Follow-ups

- Write the wire-contract matrix (`docs/architecture/wire-contract-matrix.md`).
- Open a tracking issue per uncovered pair (cli ↔ orch, governance ↔ memory, etc.).
- Schedule M-WA1 (or Sprint 3) with explicit acceptance: every
  high-priority pair has a contract test in CI; the matrix is
  committed and reviewed.

### Closed: PhaseStatus drift audit (2026-05-31)

| Drift source | Finding | Resolution |
|---|---|---|
| `sophia-wire-v1.md §524` stale phase-status set | Spec listed 5 values `{pending, running, blocked, done, failed}`; orch domain already emitted 7 canonical values since changelog 0.1.2 events were added, but §524 was never updated. `failed` appeared as a phase status but is in fact the **`phase.failed` EVENT**, not a reachable phase status. | Updated §524 to the canonical 7: `pending, running, done, done_with_concerns, blocked, needs_context, interrupted`. Added clarifying note that `failed` is the `phase.failed` event; a failing phase persists `status=blocked` and emits that event. SHA256 regenerated and mirrored to both repos in the same commit pair (feature-branch-chain: orch PR → cli PR). |
| `sophia-cli` PhaseStatus definitions | CLI carried two conflicting internal definitions (`pkg/contract/events.go` and `internal/domain/phase.go`) both including a phantom `PhaseStatusFailed` that orch never emits at phase level. `runner.go` switched on literal strings `"failed"`, `"timed_out"`, `"aborted"` — none of which are valid phase statuses. | Addressed in Slice B (cli mirror + status unification). CLI drift detector added in Slice C. |

**Governance note:** the same-commit-pair rule (`Spec checksum bumps; BOTH repos must re-mirror in the SAME commit pair.`) is the enforcement gate. Slice A (orch) and Slice B (cli) are chained PRs that must merge together before either repo's CI gate passes independently.

## Related ADRs / branches

- ADR-0005 (Local-First Ecosystem Hardening) — the parent milestone.
- ADR-0003 (memory-engine integration) — first orch ↔ memory contract.
- Branches that closed the 5 M-E0 gaps:
  - `sophia-orchestator/feature/m-e0-real-local-execution` (orch-side fixes 2-5 + dispatcher hardening + local-mode scripts + model flag)
  - `agent-governance-core/feature/m-e0-governance-decisions-endpoint` (gov-side fix 1)
