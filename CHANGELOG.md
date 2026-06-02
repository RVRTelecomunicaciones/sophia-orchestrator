# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

(empty — first changes after the v0.2.0 cut land here)

---

## [v0.2.0] — 2026-06-02

Coordinated final release with `sophia-cli v0.2.0`. Both repos carry
byte-identical `docs/specs/sophia-wire-v1.md` mirrors (SHA256
`0c2ff06ec54b44476b358b08da857ece377dbf933ec56012accaf4457396a07c`), enforced
by the cross-repo wire-contract matrix. Promoted after the 7-day soak window
(zero unresolved RED entries) plus an operator end-to-end smoke validation of
the full stack (orch → governance → runtime → opencode → LLM → envelope persist;
explore phase status=done, confidence 0.88).

### Added

- **M-E0 — real local OpenCode execution**: `scripts/local-mode/run-orchestrator-local.sh`
  and `run-runtime-local.sh` run orchestator + runtime-adapters on the host so
  runtime-adapters can spawn the user's local `opencode`/`claude` binaries (#2);
  `docs/runbooks/m-e0-local-execution.md` runbook for the partial-compose topology.
- **MCP Host Bridge (ADR-0008, V2.1)**: SDK-based MCP dispatcher client + provider,
  contract-tested initialize→tools/call sequence, host-side dispatch to local LLM CLIs.
- **M-WA1 — wire-contract matrix**: `docs/architecture/wire-contracts.md` (living doc)
  enforced by `test/wirecontract/` contract tests across orch↔memory/governance/runtime.
- `/api/v1/ready` endpoint + readiness DB ping.
- `current_phase_id` in the change GET response.

### Changed

- Canonicalized the §524 phase-status set in `sophia-wire-v1.md` to the closed
  7-value set (pending, running, done, done_with_concerns, blocked, needs_context,
  interrupted); `failed` clarified as the `phase.failed` event, not a status.
  Re-mirrored with `sophia-cli` (SHA `0c2ff06…`); ADR-0006 row closed.
- Restored the SDK-based MCP client per ADR-0008 original intent.
- `DONE_WITH_CONCERNS` now advances the change to the next phase.
- Per-group resumability: Resume blocked apply phases (BUG-28).

### Fixed

- E2E dispatcher model: `google/gemini-3-flash-preview` is unavailable against
  current auth; routed EXPLORE + ARCHIVE to `github-copilot/gpt-5.4` (#58).
- The BUG-6 … BUG-30 wire/dispatch cycle (dispatcher provider arg injection,
  synthetic envelope from git status, host-mounted worktree, resume terminal
  guard, and related M-E0 fixes).

---

## [v0.2.0-rc.1] — 2026-05-08

Coordinated release-candidate cut with `sophia-cli v0.2.0-rc.1`,
landing the M10 wire-alignment milestone. Both repos now implement a
single canonical specification: `docs/specs/sophia-wire-v1.md`
(byte-identical mirrors, SHA256
`097be33907771e727fa1e4e834f5afc01d8c3f212bb503b2a4f2dc00d19fd6c5`).

7-day soak window per D-M10-11 begins on the rc.1 tag date. Both repos
promote to `v0.2.0` final on the same day after the soak window
closes, conditional on the soak matrix carrying zero unresolved RED
entries.

See the M10 plan in the cli repo at
`docs/superpowers/plans/2026-05-07-sophia-m10-wire-alignment-v0.2.0.md`
for the full rationale and decision log (D-M10-01 through D-M10-17).

### Compatibility

> **v0.2.0 is a coordinated cut-over.** Upgrade `sophia-cli` AND
> `sophia-orchestator` together. There is **no partial-upgrade path**.

- `sophia-orchestator v0.2.0` **requires** `sophia-cli v0.2.0+` for end-to-end use.
- `sophia-orchestator v0.2.0` is **incompatible** with `sophia-cli v0.1.0`.
- `sophia-orchestator v0.1.x` is **incompatible** with `sophia-cli v0.2.0`.
- A **remote** orchestrator (non-loopback bind) MUST be configured
  with an API key (`HTTP.APIKey`); clients MUST send it via
  `X-Sophia-API-Key`.
- A **local loopback** orchestrator (bound to `localhost`,
  `127.0.0.0/8`, or `::1`) MAY accept anonymous calls only when
  started with `AllowAnonLocalhost=true`. The flag is **silently
  downgraded** to `false` if the listener is bound to a non-loopback
  interface; a warning is logged.

Migration guide: [`docs/migration/v0.1.0-to-v0.2.0.md`](docs/migration/v0.1.0-to-v0.2.0.md).
The same guide is mirrored byte-identically in the cli repo.

### Wire-protocol changes (sophia-wire-v1)

- **Canonical wire spec** lives at `docs/specs/sophia-wire-v1.md`,
  byte-identical to the cli repo's mirror; the SHA256 cross-repo gate
  is enforced by `sophia-cli`'s contract test suite (Phase 5).
- **Health path:** removed `/api/v1/healthz`; canonical is
  **`GET /api/v1/health`** (D-M10-06).
- **Approval flow** is **phase-scoped** (D-M10-13 Form A):
  - `POST /api/v1/phases/{phase_id}/approve`
  - `POST /api/v1/phases/{phase_id}/reject`
  Phase IDs are globally unique; the redundant `change_id` is
  removed from the URL.
- **SSE streams are per-phase** (D-M10-05): publish to
  `GET /api/v1/phases/{phase_id}/events`. The legacy per-Change SSE
  feed is gone.
- **SSE event ids** are ULIDs per event (sophia-wire-v1 §5.1).
- **`approval.resolved`** replaces the split `phase.approved` /
  `phase.rejected` events. The audit log uses the same event name.
- **`agent.dispatched`** is the canonical event name (replaces
  legacy `agent.spawned`).
- **`410 phase_terminal_no_events`**: SSE attach on a terminal
  phase short-circuits with this code so clients fall back to
  `GET /api/v1/phases/{id}` for state.
- **`open` event** is sent first on every SSE connection (Phase 1.5
  amendment, Optional).
- **Optional `apply.*` diagnostic events** (Phase 1.5 amendment) MAY
  be emitted; clients MUST tolerate them.

### Authentication

- Single canonical header: **`X-Sophia-API-Key`**. The legacy
  `X-API-Key` is accepted for migration but clients SHOULD use the
  canonical name.
- New `AllowAnonLocalhost` config flag — composed with the listener's
  bind address at bootstrap. The orchestrator NEVER trusts the flag
  without verifying the bind, so a non-loopback bind silently disables
  it (D-M10-02).
- The cli resolves API keys from `--api-key` → `$SOPHIA_API_KEY` →
  empty; never logged.

### Error envelope (13 stable codes)

- All non-2xx responses now carry **`{code, error, details?}`**
  (sophia-wire-v1 §9.1).
- 13 stable codes (sophia-wire-v1 §9.2): `unauthorized`,
  `validation_failed`, `approver_required`, `limit_too_large`,
  `change_not_found`, `phase_not_found`, `change_already_exists`,
  `change_already_terminal`, `phase_not_resumable`, `phase_not_gated`,
  `gate_already_decided`, `phase_terminal_no_events`,
  `internal_error`.
- Resource-scoped 404 disambiguation: changes-handler returns
  `change_not_found`; phases-handler returns `phase_not_found`.

### Lifecycle event payloads (sophia-wire-v1 §5.3)

- `phase.started`: `{phase_id, phase_type, change_id, started_at}`
- `phase.completed`: `{phase_id, phase_type, ended_at, confidence,
  envelope_status?, envelope_confidence?}` (envelope_* fields are
  forward-compat extras retained for diagnostic clients)
- `phase.failed`: `{phase_id, phase_type, ended_at, error}`

### Added

- `pkg/contract` adoption (M10 Phase 3.7) — orchestrator's HTTP
  handlers, SSE event names, and error codes are imported from the
  cli-side `pkg/contract` package, eliminating the field-name
  divergence class of bugs (D-M10-10).
- Phase-scoped HTTP routes (M10 Phase 3 / D-M10-13 Form A):
  `/api/v1/phases/{phase_id}/(get|resume|approve|reject|board|events)`.
- Per-event ULIDs in the SSE handler (`shared.IDGenerator` injected
  via `Deps.IDGen`; production wires `NewSystemIDGenerator`, tests
  wire `FixedIDGenerator`).
- `outbound.AuditLog.HasEventForPhase` query method (M10 Phase 3.8) —
  the audit log is the single source of truth for gate state, used
  by `Approve` / `Reject` to detect `phase_not_gated` and
  `gate_already_decided`.
- New phase service sentinels: `ErrApproverRequired`,
  `ErrPhaseNotGated`, `ErrGateAlreadyDecided` (M10 Phase 3.8).
- `MaxListLimit = 100` cap on `GET /api/v1/changes`; over-limit
  requests receive `400 limit_too_large` with a `details` map.
- 410 `phase_terminal_no_events` short-circuit on the SSE handler
  when the phase is already terminal.
- `docs/migration/v0.1.0-to-v0.2.0.md` operator-facing migration
  guide (mirrored byte-identically in the cli repo).
- `docs/specs/sophia-wire-v1.md` — canonical wire spec mirrored
  from `sophia-cli`. SHA256 enforced cross-repo by the cli's
  contract suite.

### Changed

- Lifecycle event emissions (M10 Phase 3.8) now use
  `contract.EventPhase*` constants and the canonical payload
  shapes documented above.
- `eventTypeForStatus` returns `contract` constants instead of
  string literals.
- Error envelope is `contract.ErrorResponse` with optional
  `details map[string]any` (was a local `errorBody` with `details
  string`).

### Removed

- The change-scoped phase routes
  `POST /api/v1/changes/{cid}/phases/{pid}/(approve|reject|...)`
  beyond `/run`. Phase IDs are globally unique; the redundant
  change-id was eliminated per D-M10-13.

### Pre-M10 (still pending v0.2.0 cut)

- Initial V1 design spec (`docs/superpowers/specs/2026-05-03-sophia-orchestator-design.md`).
- V1 implementation plan with 13 milestones / ~90 tasks (`docs/superpowers/plans/2026-05-03-sophia-orchestator-v1.md`).
- Project scaffolding: `go.mod` pinned to **Go 1.26.2** via toolchain directive, Makefile, `.golangci.yaml`, directory layout.
- Documentation: `CLAUDE.md`, `AGENTS.md`, `README.md`, `docs/architecture.md`, `docs/rules.md`, `docs/domain-invariants.md`, `docs/ai-orientation.md`.
- ADRs: `_template`, **0001** (project init), **0002** (dispatcher abstraction), **0003** (sophia-memory-engine integration contract), **0004** (PostgreSQL 16+ minimum, recommended PG 17, PG 18 feature-flagged).
- Domain layer (Milestone 2): typed ULID identifiers, injectable Clock/IDGenerator, PhaseType + PhaseStatus enums, Envelope value object with validation, IronLaw catalog, Change aggregate with phase transitions, Phase aggregate with retry budget and threshold gating, Apply aggregates (Board/Group/Task) with DAG validation and Iron Law #5, AgentSession aggregate, Worktree value object lifecycle.
- Renamed `ArtifactStoreEngram` → `ArtifactStoreMemoryEngine` (string value `memory-engine`). Engram is a session-level personal memory tool, NOT part of the Sophia ecosystem. The orchestrator persists artifacts via `sophia-memory-engine` HTTP API. See ADR-0003.
- DB minimum version: PG 15 → **PG 16+** (recommended PG 17). PG 15 EOL is Nov 2027; PG 16 EOL is Nov 2028. PG 17 brings `MERGE … RETURNING` and major vacuum/WAL improvements; PG 18 adds async I/O and UUIDv7 (feature-flagged for future). See ADR-0004.
