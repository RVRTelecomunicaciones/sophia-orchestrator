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

Behind these five gaps, a sixth was uncovered (Anthropic OAuth quota
exhausted on the test machine). That one is not a code issue but it
hid the fact that the rest of the pipeline was already working —
opencode authenticated, called the LLM, and the LLM responded; the
response was just the API-level usage-cap error. Switching the model
to `google/gemini-2.5-flash` (the user's Google OAuth provider in
opencode) closed M-E0 #4 end-to-end with a valid `NEEDS_CONTEXT`
envelope returned by Gemini.

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

## Related ADRs / branches

- ADR-0005 (Local-First Ecosystem Hardening) — the parent milestone.
- ADR-0003 (memory-engine integration) — first orch ↔ memory contract.
- Branches that closed the 5 M-E0 gaps:
  - `sophia-orchestator/feature/m-e0-real-local-execution` (orch-side fixes 2-5 + dispatcher hardening + local-mode scripts + model flag)
  - `agent-governance-core/feature/m-e0-governance-decisions-endpoint` (gov-side fix 1)
