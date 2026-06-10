# Tasks: prereqs-fts-archived-worker-mcp (M-KNOW-PRE-0)

## Review Workload Forecast

| Field | Value |
|---|---|
| Estimated changed lines | 620–720 |
| 400-line budget risk | High |
| Chained PRs recommended | Yes |
| Suggested split | PR1 (memory-engine): Group A (FTS) + Group C (worker skeleton) → PR2 (cross-repo): Group B (orch event + CLI mirror) → PR3 (agent-mcp): Group D (config + allowlist) |
| Delivery strategy | ask-on-risk |
| Chain strategy needed | Yes — pending operator choice |
| Decision needed before apply | Yes |
| Notes | orch+CLI forced into single cross-repo PR by wire_alignment_test; PR2 cannot be split further. PR1 and PR3 are independent and can ship in any order relative to each other but must precede no-op M2 work. |

Decision needed before apply: Yes
Chained PRs recommended: Yes
Chain strategy: pending
400-line budget risk: High

### Suggested Work Units

| Unit | Goal | Likely PR | Notes |
|------|------|-----------|-------|
| 1 | FTS simple migration + worker skeleton | PR1 (memory-engine) | Independent of orch; testcontainers integration for FTS |
| 2 | phase.archived event + CLI wire mirror | PR2 (cross-repo: orch + CLI) | wire_alignment_test is hard gate; cannot split orch from CLI |
| 3 | mcp_providers TOML schema + allowlist enforcer | PR3 (agent-mcp) | Independent of PR1 and PR2; unit-only tests |

---

## Cross-repo PR pairing (locked)

Single PR pattern for orchestator + CLI event mirror (Group B). Operator approval gate before merge. No feature flags. No skip path. `wire_alignment_test` is an unconditional CI gate.

---

## Task groups (in operator-mandated order)

### Group A: FTS simple (memory-engine)

Spec: `fts-simple-config`. Satisfies requirements: FTS Language Default — Simple, FTS Language Migration — Idempotent Backfill, English Content Searchable After Migration, Code-Side Default Matches Migration.

- [x] A.1 **(test-first — RED)** Write integration test `sophia-memory-engine/internal/adapters/outbound/search/postgres_fts_migration_test.go` (build tag `integration`): pre-seed one row each in `memories`, `decisions`, `heuristics` with `fts_language='spanish'`; run migration 005 via `golang-migrate`; assert each row's `fts_language='simple'`; assert `column_default` for all 3 tables is `'simple'::regconfig`; assert `plainto_tsquery('simple', '<english term>')` against `search_vector` returns the fixture row; run 005.up twice (idempotency); run 005.down + 005.up round-trip (no error). Test MUST fail before migration file exists.
- [x] A.2 **(test-first — RED)** Write unit test `postgres_fts_literal_test.go`: table-driven assertions that the three SQL string constants in `postgres_fts.go` contain the literal `'simple'` (not `'spanish'`). Test MUST fail before code change.
- [x] A.3 Write `sophia-memory-engine/migrations/postgres/005_fts_simple.up.sql`: idempotent `UPDATE memories|decisions|heuristics SET fts_language = 'simple' WHERE fts_language = 'spanish'` + `ALTER TABLE ... ALTER COLUMN fts_language SET DEFAULT 'simple'` for all 3 tables, wrapped in `BEGIN/COMMIT`.
- [x] A.4 Write `sophia-memory-engine/migrations/postgres/005_fts_simple.down.sql`: symmetric idempotent reverse — `UPDATE ... WHERE fts_language = 'simple'` + `ALTER COLUMN fts_language SET DEFAULT 'spanish'`, wrapped in `BEGIN/COMMIT`.
- [x] A.5 **(GREEN)** In `sophia-memory-engine/internal/adapters/outbound/search/postgres_fts.go` lines 110, 112, 117: replace all three `'spanish'` SQL literals with `'simple'`. No other changes.
- [x] A.6 **(GREEN)** In `sophia-memory-engine/internal/domain/memory/memory.go` line 119: change `FTSLanguage: "spanish"` to `FTSLanguage: "simple"`.
- [x] A.7 Verify: `make test-unit` passes in `sophia-memory-engine`; `make test-integration` passes (testcontainers PG, `-tags=integration`, 5 min timeout).
- [ ] A.8 **CHECKPOINT** — operator approval before committing Group A changes.

### Group B: phase.archived event (orch + CLI — single cross-repo PR)

Spec: `phase-archived-event`. Satisfies requirements: EventPhaseArchived Constant, PhaseArchivedPayload Shape, Exactly-Once Emission at Archive Completion, CLI Wire Contract Mirror.

- [x] B.1 **(test-first — RED)** Write `sophia-orchestator/internal/application/phase/archive_event_test.go`: build a `Service` with `FakeEventPublisher` + `FakeClock` (fixed time) + `FakeChangeRepo` (Save returns nil) + a `Change` in archive-running state; call `advanceChange(ctx, change, phase.PhaseArchive)`; assert exactly one `EventPhaseArchived` emission with `PhaseArchivedPayload{ChangeID, ChangeName, "archive", clock.Now()}`. Test MUST fail before constant/emission exist.
- [x] B.2 **(test-first — RED)** Add test case to B.1 file: `FakeChangeRepo.Save` returns error after `MarkCompleted` → assert zero `EventPhaseArchived` emissions (Iron Law D1.2 guard). Test MUST fail before save-error branch is guarded.
- [x] B.3 **(test-first — RED)** Add test case to B.1 file: call `advanceChange(ctx, change, phase.PhaseArchive)` twice → assert exactly ONE `EventPhaseArchived` emission total (MarkCompleted idempotency check; if MarkCompleted is not idempotent this test drives the required guard). Test MUST fail before guard exists.
- [x] B.4 **(test-first — RED)** Add test case: call `advanceChange(ctx, change, phase.PhaseTasks)` (non-archive) → assert zero `EventPhaseArchived` emissions. Test MUST fail before emission is gated on `completed == phase.PhaseArchive`.
- [x] B.5 **(GREEN)** Add `EventPhaseArchived EventType = "phase.archived"` to `sophia-orchestator/internal/ports/inbound/event_types.go` and add `EventPhaseArchived: {}` entry to `knownEventTypes` map.
- [x] B.6 **(GREEN)** Add `PhaseArchivedPayload` struct to `sophia-orchestator/internal/ports/inbound/event_payloads.go` with fields `ChangeID`, `ChangeName`, `PhaseType`, `ArchivedAt time.Time` and JSON tags matching the wire format.
- [x] B.7 **(GREEN)** Wire emission in `sophia-orchestator/internal/application/phase/service.go` around L911 inside `advanceChange`: resolve `phaseID` via `FindByChangeAndType` repo lookup (no new parameter); emit via `s.publishEvent` only when `c.MarkCompleted(s.d.Clock.Now())` returns nil AND `s.d.ChangeRepo.Save(ctx, c)` returns nil. `ArchivedAt` uses `s.d.Clock.Now()` (no `time.Now()` direct call).
- [x] B.8 **(GREEN)** Add `EventPhaseArchived = "phase.archived"` constant to `sophia-cli/pkg/contract/events.go` Section 1 (after `EventPhaseNeedsContext`) and add `EventPhaseArchived: {}` to `knownEvents` map.
- [x] B.9 Verify: `make test-unit` passes in `sophia-orchestator`; `make test` passes in `sophia-cli` (wire_alignment_test must be green).
- [ ] B.10 **CHECKPOINT** — operator approval for single cross-repo PR pairing orch + CLI (both repos must land together; operator explicitly approves merge).

### Group C: Worker skeleton (memory-engine)

Spec: `consolidation-worker-skeleton`. Satisfies requirements: Worker Process Lifecycle, Stub Handler for phase.archived, Publisher Interface for M2 Extensibility, Worker Transport Deferred to M2.

- [x] C.1 **(test-first — RED)** Write `sophia-memory-engine/internal/application/consolidation/handler_test.go`: create `FakeSubscriber` + `Handler` (with captured `slog.Logger` over `bytes.Buffer` + `shared.FakeClock`); call `sub.Subscribe(ctx, PhaseArchivedEventType, handler.Handle)`; call `sub.Emit(ctx, PhaseArchivedEventType, PhaseArchivedReceived{...})`; assert log buffer contains `"phase.archived received"`, `change_id`, `change_name`, `archived_at`, `received_at`; assert handler returned nil. Negative case: emit with no handler subscribed → nil return. Test MUST fail before package exists.
- [x] C.2 **(GREEN)** Create `sophia-memory-engine/internal/application/consolidation/subscriber.go`: define `EventSubscriber` interface (one method: `Subscribe(ctx, eventType string, handler EventHandler) error`), `EventHandler` type, `PhaseArchivedReceived` struct with JSON tags, `PhaseArchivedEventType = "phase.archived"` constant.
- [x] C.3 **(GREEN)** Create `sophia-memory-engine/internal/application/consolidation/fake_subscriber.go`: `FakeSubscriber` with `sync.Mutex`-guarded `handlers map[string]EventHandler`; implement `Subscribe` (records handler) and `Emit` (drives payload synchronously into recorded handler, returns nil if no handler registered).
- [x] C.4 **(GREEN)** Create `sophia-memory-engine/internal/application/consolidation/handler.go`: `Handler` struct with `log *slog.Logger` + `clock shared.Clock`; `NewHandler` constructor; `Handle(ctx, PhaseArchivedReceived) error` that logs receipt via `slog.InfoContext` with all payload fields + `received_at: h.clock.Now()` and returns nil.
- [x] C.5 **(GREEN)** Replace stub in `sophia-memory-engine/cmd/workers/main.go` with runnable skeleton: `signal.NotifyContext` for SIGINT/SIGTERM; `slog.New(slog.NewJSONHandler(os.Stdout, nil))`; `shared.RealClock{}` (codebase uses RealClock, not SystemClock); `consolidation.NewHandler`; `var subscriber consolidation.EventSubscriber` typed nil with `// TODO(M2)` comment; log warning + `<-ctx.Done()` + clean exit when subscriber is nil.
- [x] C.6 Verify: `go build ./cmd/workers/...` produces binary in `sophia-memory-engine`; `make test-unit` passes (handler_test.go green).
- [ ] C.7 **CHECKPOINT** — operator approval before committing Group C changes.

### Group D: mcp_providers TOML + allowlist (agent-mcp)

Spec: `mcp-providers-config`. Satisfies requirements: MCPProviderConfig Schema, Loader Validation at Load Time, tools_allowed Allowlist Enforcement, No New Parser Dependency.

- [x] D.1 **(test-first — RED)** Write (or extend) `sophia-agent-mcp/internal/infrastructure/config/loader_test.go` with table-driven cases: valid full TOML fixture (2 providers) → loads without error; absent `mcp_providers` block → empty slice, no error; `id=""` → error; duplicate `id` → error; `command=""` → error; unknown `transport="grpc"` → error matching `"transport %q is not allowed"`; `tools_allowed=[]` → error; unknown `lifecycle="forever"` → error. All cases MUST fail before config struct + validation block exist.
- [x] D.2 **(test-first — RED)** Write `sophia-agent-mcp/internal/infrastructure/mcp/allowlist_test.go`: `NewAllowlistEnforcer` with one provider `{ID: "graphify", ToolsAllowed: ["query_graph", "get_node"]}`; `Authorize("graphify", "query_graph")` → nil; `Authorize("graphify", "delete_graph")` → `errors.Is(err, ErrToolNotAllowed) == true`; `Authorize("unknown", "anything")` → `errors.Is(err, ErrUnknownProvider) == true`; empty providers → any call returns `ErrUnknownProvider`. Tests MUST fail before allowlist.go exists.
- [x] D.3 **(GREEN)** Add `MCPProviderConfig` struct + `MCPProviders []MCPProviderConfig \`toml:"mcp_providers"\`` field to `sophia-agent-mcp/internal/infrastructure/config/config.go`. Fields: `ID`, `Package`, `Command`, `Transport`, `ToolsAllowed []string`, `Lifecycle` with `toml:` struct tags. No new parser import.
- [x] D.4 **(GREEN)** Add validation block to `sophia-agent-mcp/internal/infrastructure/config/loader.go` inside `validate(cfg Config)`: sentinel constants for `transportStdio`, `lifecycleSpawnedPerChange`, `lifecycleLongLived`; iterate `cfg.MCPProviders` — reject empty `id`, duplicate `id`, empty `command`, unknown `transport`, empty `tools_allowed`, unknown `lifecycle`; absent block → skip loop (no error).
- [x] D.5 **(GREEN)** Create `sophia-agent-mcp/internal/infrastructure/mcp/allowlist.go`: package `mcp`; `ErrToolNotAllowed = errors.New("mcp: tool not in provider allowlist")`; `ErrUnknownProvider = errors.New("mcp: unknown provider")`; `AllowlistEnforcer` struct with `allowed map[string]map[string]struct{}`; `NewAllowlistEnforcer([]config.MCPProviderConfig) *AllowlistEnforcer`; `Authorize(providerID, toolName string) error` wrapping sentinels with `fmt.Errorf("%w: provider=%q tool=%q", ...)`. Caller uses `errors.Is`. NOT wired into MCP dispatch (deferred to INIT-0).
- [x] D.6 Verify: `make test` passes in `sophia-agent-mcp`; confirm zero new parser dependencies in `go.mod`.
- [ ] D.7 **CHECKPOINT** — operator approval before committing Group D changes.

### Group E: Cross-cutting verification

- [ ] E.1 Run full strict-TDD verify in each affected repo: `make test-unit` in memory-engine (Groups A+C); `make test-unit` in orch + `make test` in sophia-cli (Group B); `make test` in agent-mcp (Group D).
- [ ] E.2 Run `make test-integration` in `sophia-memory-engine` (testcontainers PG, `-tags=integration`); confirm FTS migration round-trips cleanly on the containerized DB.
- [ ] E.3 Confirm `TestWireAlignment_OrchEventsMirrored` passes in `sophia-cli`.
- [ ] E.4 Confirm zero `Co-Authored-By` or AI attribution in any commit message across all repos.
- [ ] E.5 Confirm all commits follow conventional commits format: `feat(scope)`, `fix(scope)`, `test(scope)`, etc. No scope-less commits.
- [ ] E.6 **FINAL CHECKPOINT** — operator approval to mark PRE-0 complete and unblock M-KNOW-INIT-0.

---

## Strict TDD requirement

Strict TDD mode is ACTIVE. Every production task (A.3–A.6, B.5–B.8, C.2–C.5, D.3–D.5) is preceded by a failing test task. RED → GREEN order is non-negotiable. Do not write any production code before its corresponding test exists and fails for the right reason.

Test runners per repo:
- `sophia-memory-engine`: `make test-unit` (unit); `make test-integration` (testcontainers, `-tags=integration`, 5 min timeout)
- `sophia-orchestator`: `make test-unit`
- `sophia-cli`: `make test`
- `sophia-agent-mcp`: `make test`

---

## Locked decisions (baked in, not re-opened)

1. **phaseID resolution**: `c.CurrentPhase()` lookup inside `advanceChange` — no new parameter to `advanceChange`.
2. **MarkCompleted idempotency**: test-driven discovery (B.3 drives the guard). If `MarkCompleted` is already idempotent, test passes trivially; if not, apply phase adds explicit "already-emitted" guard.
3. **Allowlist error type**: `ErrToolNotAllowed` and `ErrUnknownProvider` as typed sentinels wrapped with `fmt.Errorf("%w: ...")`. Callers use `errors.Is`.
4. **Migration 005 test data setup**: explicit pre-seed step — INSERT row in each of `memories/decisions/heuristics` with `fts_language='spanish'` BEFORE running migration; assert `fts_language='simple'` and rebuilt `search_vector` post-migration.

---

## Out-of-scope reminders

- No worker transport implementation (SSE / webhook / bus) — deferred to M2.
- No wiring of `AllowlistEnforcer` into MCP dispatch path — deferred to INIT-0.
- No V4.1 doc YAML→TOML patches — separate documentation change.
- No consolidation logic in the worker handler — PRE-0 logs receipt only.
- No FTS query reformulation, ranking changes, or new index strategies.
- No cross-module `replace` directives or `go.work` changes.
- No Graphify-specific provider config values — M-KNOW-INIT-0 owns Graphify wiring.
- No `ChangeName` field added to `PhaseArchivedPayload` if not present in existing event signature — follow existing `publishEvent` helper signature exactly; spec field list is authoritative.
