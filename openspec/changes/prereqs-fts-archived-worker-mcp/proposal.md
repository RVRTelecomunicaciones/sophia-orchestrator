# Proposal: prereqs-fts-archived-worker-mcp (M-KNOW-PRE-0)

**Strategy ref:** V4.1, locked decisions D12 + D13, milestone M-KNOW-PRE-0.
**Mode:** SDD propose. No production code changes — proposal artifact only.
**Cross-repo scope:** sophia-orchestator + sophia-memory-engine + sophia-agent-mcp + sophia-cli.
**Strict TDD:** ENABLED — tests-first for production logic in orchestator and memory-engine.

---

## Intent

M-KNOW-INIT-0 ("Graphify as Memory") cannot start until four pre-existing gaps in the platform are closed. V4.1 D12 locks the FTS configuration to `'simple'` so language-agnostic indexing works end-to-end; D13 locks `phase.archived` as a first-class orchestator event so the consolidation pipeline has a stable trigger; the V4.1 worker-location supuesto was invalidated during explore (cf. `explore.md:44-59` — orchestator has no `cmd/workers/`; the only worker entrypoint lives at `sophia-memory-engine/cmd/workers/main.go`); and `sophia-agent-mcp` has no schema for the `mcp_providers[]` array (cf. `explore.md:62-85` — `Config` struct has no `MCPProviders` field). Without this change, M-KNOW-INIT-0 cannot start because (1) FTS queries against `decisions` and `heuristics` would still tokenize through `pg_catalog.spanish` and silently drop English content, (2) the worker has no `phase.archived` signal to subscribe to, (3) there is no worker process to host the future consolidation handler, and (4) there is no config surface to declare Graphify as an MCP provider.

## Scope

### In Scope

1. **FTS config simple in memory-engine (3 tables).**
   - WHAT: switch FTS language default from `'spanish'` to `'simple'` across `memories`, `decisions`, `heuristics` (V4.1 D12 originally scoped only `memories`; explore widened to 3 tables — cf. `explore.md:30-31`).
   - WHERE: `sophia-memory-engine/internal/adapters/outbound/search/postgres_fts.go:110,112,117` (3 SQL literals), `sophia-memory-engine/internal/domain/memory/memory.go:119` (Go default), new idempotent migration `sophia-memory-engine/migrations/postgres/005_fts_simple.up.sql` + `.down.sql` doing `UPDATE ... SET fts_language = 'simple'` + `ALTER TABLE ... ALTER COLUMN fts_language SET DEFAULT 'simple'`.
   - WHY: existing trigger `trg_memories_fts` is `BEFORE INSERT OR UPDATE` and reads `NEW.fts_language` dynamically (cf. `explore.md:18-27`), so an `UPDATE` rebuilds `search_vector` per row with zero downtime. Anything narrower leaves stale rows tokenized as Spanish and makes English content silently unsearchable.

2. **`phase.archived` event explicit (orch + CLI in the same PR).**
   - WHAT: introduce `EventPhaseArchived EventType = "phase.archived"`, a `PhaseArchivedPayload`, and an emission at the existing archive-completion point. Mirror the constant in CLI's `knownEvents`.
   - WHERE: `sophia-orchestator/internal/ports/inbound/event_types.go` (new constant), `sophia-orchestator/internal/ports/inbound/event_payloads.go` (new payload struct), `sophia-orchestator/internal/application/phase/service.go:911` (emission inside `advanceChange()` when `completed == phase.PhaseArchive`, before `c.MarkCompleted`), `sophia-cli/pkg/contract/events.go` (mirrored constant).
   - WHY: V4.1 D13 explicitly rejects filtering `phase.completed` by status; the worker must subscribe to a distinct event name. `sophia-cli/pkg/contract/wire_alignment_test.go:177` AST-parses every `Event*` constant in orch and fails the build if CLI does not mirror it (cf. `explore.md:40-42`), so orch + CLI MUST land in a single cross-repo PR with explicit operator approval. No feature flags. No skip path.

3. **Worker skeleton in `sophia-memory-engine/cmd/workers/main.go`.**
   - WHAT: replace the 5-line stub (cf. `explore.md:46-52`) with a runnable skeleton: graceful `context.Context` lifecycle, slog logger, a stub `phase.archived` handler that logs receipt of a payload, and a fake event publisher used only in tests so the handler can be exercised without choosing a transport.
   - WHERE: `sophia-memory-engine/cmd/workers/main.go` plus a small internal package (TBD by sdd-design) for the handler + test fake.
   - WHY: V4.1 supuesto-invalidated the orchestator worker location; the actual `cmd/workers/main.go` lives in memory-engine (cf. `explore.md:11 section header, 44-59`). PRE-0's role is to make the process exist and prove the handler shape compiles + runs; the transport (SSE / webhook / bus) is M2's call.

4. **`mcp_providers[]` TOML config in sophia-agent-mcp.**
   - WHAT: add `MCPProviders []MCPProviderConfig` to `Config`, define `MCPProviderConfig` (id, package, command, transport, tools_allowed, lifecycle), validate the array at load time, and add a tools_allowed allowlist enforcement middleware.
   - WHERE: `sophia-agent-mcp/internal/infrastructure/config/config.go` (struct), `sophia-agent-mcp/internal/infrastructure/config/loader.go:24` (validation; existing `toml.DecodeFile` already covers parsing), new `sophia-agent-mcp/internal/infrastructure/mcp/allowlist.go`.
   - WHY: agent-mcp already uses BurntSushi/toml (`loader.go:24`); V4.1 examples are YAML but semantically equivalent (cf. `explore.md:62-85`). Choosing TOML matches the existing loader, avoids a new YAML parser dependency, and keeps the config surface coherent with the rest of agent-mcp.

### Out of Scope (explicit non-goals)

- Worker transport choice (SSE / webhook / message bus). Explicitly deferred to M2 per `explore.md:58-59`.
- Any Graphify-specific provider config values. Graphify wiring is M-KNOW-INIT-0, not PRE-0.
- Any consolidation logic inside the worker handler. PRE-0 logs receipt only; consolidation is M2.
- V4.1 strategy doc patches (e.g., updating YAML examples to TOML). That is a separate documentation change, not this proposal.
- Any FTS query reformulation, ranking change, or query parser change beyond the language switch.
- Any new index strategies on `memories`, `decisions`, or `heuristics` (the trigger + existing GIN index continue to apply).
- Any cross-module replace directives, go.work changes, or shared-module extraction (the three Go modules remain independent — cf. `explore.md:108-114`).

## Approach (high level)

**Order of work (operator-mandated):** FTS → `phase.archived` (orch + CLI together) → worker skeleton → `mcp_providers` config → tests → checkpoint.

- **FTS:** ship a single idempotent migration `005_fts_simple` that does `UPDATE memories|decisions|heuristics SET fts_language = 'simple' WHERE fts_language = 'spanish'` followed by `ALTER TABLE ... ALTER COLUMN fts_language SET DEFAULT 'simple'`. The down migration reverses both. The existing per-row trigger rebuilds `search_vector` automatically, so no manual reindex is required. Update the three SQL literals in `postgres_fts.go` and the Go-side default in `memory.go` to match.
- **`phase.archived`:** add the constant, payload, and emission inside `advanceChange()` at the existing archive detection point, then mirror the constant in CLI in the same PR. `wire_alignment_test` is treated as a hard CI gate; if it fails, the PR does not merge.
- **Worker skeleton:** structure the new entry point so that the handler is a small, testable function and the publisher is an interface with a test fake. Production wiring of a real publisher is intentionally left as a `// TODO(M2)` comment with a typed nil or noop.
- **`mcp_providers` config:** extend the existing TOML decoder by adding the struct fields — no new dependency. Loader validation rejects unknown transports, empty `id`, and missing `tools_allowed`. The allowlist middleware is a thin interceptor in front of MCP tool invocation; it does not yet need to know anything about Graphify.
- **Cross-repo discipline:** orch + CLI land in one PR (single checkpoint, operator approval). memory-engine and agent-mcp can land independently because they have no cross-repo CI gate, but each still requires explicit approval per operator rules.

## Affected Areas

| Area | Impact | Description |
|---|---|---|
| `sophia-memory-engine/internal/adapters/outbound/search/postgres_fts.go` | Modified | Replace 3 `'spanish'` literals with `'simple'` |
| `sophia-memory-engine/internal/domain/memory/memory.go` | Modified | `FTSLanguage` default → `"simple"` |
| `sophia-memory-engine/migrations/postgres/005_fts_simple.up.sql` | New | Idempotent UPDATE + ALTER DEFAULT over 3 tables |
| `sophia-memory-engine/migrations/postgres/005_fts_simple.down.sql` | New | Idempotent reverse |
| `sophia-memory-engine/cmd/workers/main.go` | Modified | Replace stub with runnable skeleton + stub handler |
| `sophia-orchestator/internal/ports/inbound/event_types.go` | Modified | Add `EventPhaseArchived` |
| `sophia-orchestator/internal/ports/inbound/event_payloads.go` | Modified | Add `PhaseArchivedPayload` |
| `sophia-orchestator/internal/application/phase/service.go` | Modified | Emit `EventPhaseArchived` at archive completion (around L911) |
| `sophia-cli/pkg/contract/events.go` | Modified | Mirror `EventPhaseArchived` constant in `knownEvents` |
| `sophia-agent-mcp/internal/infrastructure/config/config.go` | Modified | Add `MCPProviders []MCPProviderConfig` |
| `sophia-agent-mcp/internal/infrastructure/config/loader.go` | Modified | Validate `mcp_providers[]` array |
| `sophia-agent-mcp/internal/infrastructure/mcp/allowlist.go` | New | `tools_allowed` enforcement middleware |

## Risks

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| FTS migration scope wider than V4.1 D12 stated (3 tables vs 1) | Realized | Medium — incomplete migration leaves stale rows | Spec covers all 3 tables; migration uses idempotent `WHERE fts_language = 'spanish'`; integration test asserts all 3 defaults |
| Wire alignment hard CI gate — orch + CLI must land together | High | High — broken build blocks all orch work | Single cross-repo PR with explicit operator approval; `wire_alignment_test` is a non-negotiable gate; no feature flag bypass |
| Worker transport unresolved at PRE-0 | Certain (deferred) | Low for PRE-0 | Explicit non-goal; skeleton uses a fake publisher interface so M2 can plug in SSE/webhook/bus without rewriting handler |
| TOML vs YAML mismatch with V4.1 doc examples | Realized | Low (cosmetic) | Choose TOML in code (matches existing loader); document mismatch; doc patch is a separate follow-up |
| Go toolchain drift across repos (1.26.2 vs 1.26.3 observed) | Realized | Low | Per-repo `go.mod` `toolchain` directive already pins; CI uses `GOTOOLCHAIN=auto`; non-blocking but tracked |
| FTS migration applied without rebuild on legacy rows | Low | Medium | Existing trigger is `BEFORE INSERT OR UPDATE` and reads `NEW.fts_language` per row, so `UPDATE` rebuilds `search_vector` automatically (cf. `explore.md:18-27`) |

## Acceptance Criteria (proposal-level)

V4.1 M-KNOW-PRE-0 lists 6 criteria. This proposal expands criterion (1) per operator decision to cover 3 tables instead of 1; the remaining 5 are preserved verbatim in intent. Detailed, testable acceptance criteria are deferred to the spec phase.

1. **FTS:** `memories`, `decisions`, and `heuristics` all default `fts_language` to `'simple'`; existing rows updated; trigger rebuilds `search_vector` per row; integration test against testcontainers PG confirms language-agnostic tokenization on all 3 tables (expanded scope vs V4.1).
2. **`phase.archived` (orch):** `EventPhaseArchived` constant + payload exist; emission occurs exactly once when a change transitions into archive completion; unit test subscribes and asserts payload shape.
3. **`phase.archived` (CLI):** `wire_alignment_test` passes — the new constant is mirrored in CLI's `knownEvents`. Same PR as orch.
4. **Worker:** `sophia-memory-engine/cmd/workers/main.go` builds and runs; logs receipt of a fake `phase.archived` payload in a unit test using a fake publisher; no transport selected.
5. **`mcp_providers` config:** TOML-loadable; loader rejects invalid entries; allowlist middleware enforces `tools_allowed`; no new parser dependency.
6. **Cross-repo checkpoint:** operator-approved single PR pairing for orch + CLI event mirror; memory-engine and agent-mcp land per their own approval.

## Open Questions

None. All scope, format (TOML), table scope (3), wire alignment policy (single PR), worker transport (deferred to M2), and order of work are operator-locked.

## Rollback Plan

- **FTS:** run `005_fts_simple.down.sql` (reverses `UPDATE` and `ALTER DEFAULT`); revert `postgres_fts.go` + `memory.go` literals. Trigger rebuilds `search_vector` on the `UPDATE`.
- **`phase.archived`:** revert orch + CLI commits in the joint PR (single revert, both repos). No data migration involved.
- **Worker skeleton:** revert `cmd/workers/main.go` to the previous stub. No persistent state.
- **`mcp_providers` config:** revert config + allowlist additions. Absent `mcp_providers` array is treated as empty (no providers), so existing agent-mcp behavior is unaffected.

## Strict TDD Note

`strict_tdd: true` per `sdd-init/2026`. Spec phase MUST define test-first acceptance per item (table-driven subtests + testify; testcontainers for FTS integration; goroutine + per-subscriber chan for orch event emission tests; fake publisher for worker). Apply phase will follow `strict-tdd.md` — production logic is preceded by a failing test in every batch.

## Capabilities

> Captured for sdd-spec contract. Hybrid mode: each item below becomes a capability surface in spec.

### New Capabilities

- `fts-simple-config`: language-agnostic FTS config across `memories`, `decisions`, `heuristics` via migration 005 and code-side defaults.
- `phase-archived-event`: first-class orchestator event for archive completion, mirrored in CLI wire contract.
- `consolidation-worker-skeleton`: runnable worker entrypoint in memory-engine with a stub `phase.archived` handler and a fake publisher for tests.
- `mcp-providers-config`: TOML `mcp_providers[]` schema in agent-mcp with `tools_allowed` allowlist enforcement.

### Modified Capabilities

- None at the spec level. All four items introduce new capability surfaces rather than changing existing spec-level requirements.
