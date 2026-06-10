# Design: prereqs-fts-archived-worker-mcp (M-KNOW-PRE-0)

**Strategy ref:** V4.1 §11 + §16 — locked decisions D12 (FTS=`simple`) + D13 (`phase.archived` event).
**Cross-repo scope:** sophia-memory-engine + sophia-orchestator + sophia-cli + sophia-agent-mcp.
**Strict TDD:** ENABLED. Every component in this design MUST be preceded by a failing test in the apply phase.
**Iron Law D1.2:** Envelope persisted BEFORE caller-visible state change. The `phase.archived` emission is downstream of envelope persistence.
**Clock/IDGenerator:** No direct `time.Now()` / `ulid.Make()` in domain/application — see Decision D-PRE-2 for the explicit injection points.

---

## Approach

PRE-0 ships four independent prerequisite components in a strict, operator-mandated order: (1) idempotent FTS migration 005 across 3 tables in memory-engine, (2) `phase.archived` event in orchestator paired with its CLI wire mirror in a single cross-repo PR, (3) memory-engine worker skeleton with a transport-agnostic `Publisher` interface and a `FakePublisher` test fake, (4) TOML `mcp_providers[]` schema + tools_allowed allowlist middleware in agent-mcp. Cross-repo discipline: orch ↔ CLI is a single PR enforced by `wire_alignment_test`; memory-engine and agent-mcp ship independently with explicit operator approval. Worker transport (SSE / webhook / message bus) is explicitly deferred to M2 — PRE-0 only fixes the publisher interface shape so M2 can plug a concrete transport without rewriting the handler.

---

## Decisions

### D-PRE-1: FTS migration via per-row `UPDATE` + `ALTER COLUMN ... SET DEFAULT` across 3 tables

**Why.** The trigger `trg_memories_fts` (and its `decisions` / `heuristics` siblings at `migrations/postgres/001_initial_schema.up.sql:78-91, 218-231`) is `BEFORE INSERT OR UPDATE` and reads `NEW.fts_language` dynamically. A no-op `UPDATE` per row causes the trigger to rebuild `search_vector` with the new language. This gives us zero-downtime, idempotent migration with no manual reindex. The `WHERE fts_language = 'spanish'` predicate makes re-runs of `up` a no-op, and the symmetric `WHERE fts_language = 'simple'` predicate makes `down` re-runs a no-op.

**Tradeoff.** Slower than a column-default-only change (one row update per row), but mandatory because the existing rows' `search_vector` was tokenized through `pg_catalog.spanish` and would silently drop English content. The proposal made the 3-table scope explicit (cf. `proposal.md:18-21` and `explore.md:30-31`) — V4.1 D12 originally only mentioned `memories`.

**Alternatives considered.**
- **A**: `GENERATED ALWAYS AS STORED` for `search_vector`. REJECTED — would require dropping and recreating the column; loses per-row `fts_language` flexibility; major migration risk.
- **B**: `DROP TRIGGER` + `CREATE TRIGGER` with hardcoded `'simple'`. REJECTED — destroys the per-row `fts_language` capability that the schema explicitly offers (the column exists for a reason).
- **C**: Code-side default only, no SQL migration. REJECTED in proposal (cf. `proposal.md:18-21`) — leaves all existing rows tokenized as Spanish.

### D-PRE-2: `phase.archived` emission AFTER envelope persisted, inside `advanceChange`

**Why.** Iron Law D1.2 states the envelope is persisted BEFORE any caller-visible state change. The SSE event IS caller-visible state. The current archive completion path at `phase/service.go:903-916` already mutates the change AFTER envelope persistence (the envelope-persisting helpers run earlier in `runPhaseCompletion` / `runApplyPhase` before `advanceChange` is called). Therefore the emission point is INSIDE `advanceChange` and AFTER `c.MarkCompleted(s.d.Clock.Now())` — at that line, both the envelope and the terminal state transition are durable.

**Where (precise).** `sophia-orchestator/internal/application/phase/service.go:911-915` — the existing block

```go
if completed == phase.PhaseArchive {
    if err := c.MarkCompleted(s.d.Clock.Now()); err == nil {
        _ = s.d.ChangeRepo.Save(ctx, c)
    }
}
```

becomes

```go
if completed == phase.PhaseArchive {
    if err := c.MarkCompleted(s.d.Clock.Now()); err == nil {
        if saveErr := s.d.ChangeRepo.Save(ctx, c); saveErr == nil {
            // Iron Law D1.2 satisfied: envelope persisted upstream, change.MarkCompleted
            // saved here, NOW emit the caller-visible event.
            s.publishEvent(ctx, /* phaseID derived from c */, contract.EventPhaseArchived, inbound.PhaseArchivedPayload{
                ChangeID:    c.ID().String(),
                ChangeName:  c.Name(),
                PhaseType:   string(phase.PhaseArchive),
                ArchivedAt:  s.d.Clock.Now(),
            })
        }
    }
}
```

The `phaseID` is the just-completed archive phase; the caller already has it in scope at the `advanceChange` call site (caller passes `completed` and has the phase pointer). The design phase identifies this; spec/tasks will lock the exact signature change (either add `phaseID` parameter to `advanceChange` or look it up via the change's current phase pointer).

**Tradeoff.** Adds one event emission per archive completion. Reuses the existing `publishEvent` helper which already wires `Clock` and `Trace` correctly (cf. `service.go:967-978`). No new goroutine, no new bus.

**Clock injection.** `ArchivedAt: s.d.Clock.Now()` — domain/application never touches `time.Now()`. The `Service`'s `Clock` dependency is already in scope at the emission site.

**Alternatives considered.**
- **A**: Filter `phase.completed` events by `phase_type == archive` on the consumer side. REJECTED by V4.1 D13 explicitly (cf. `proposal.md:53`) and by `eventTypeForStatus()` design at `service.go:1057-1069` which maps only by status.
- **B**: Emit from `runPhaseCompletion` BEFORE `advanceChange`. REJECTED — at that point `MarkCompleted` has not yet been called, so the change's terminal state is not durable. Event would be premature.
- **C**: Emit from a separate audit-log consumer. REJECTED — adds infrastructure for no benefit; emission is naturally co-located with the state transition.

### D-PRE-3: Cross-repo PR pairing for orch + CLI (hard CI gate)

**Why.** `sophia-cli/pkg/contract/wire_alignment_test.go:177` (`TestWireAlignment_OrchEventsMirrored`) AST-parses every `Event*` constant in `sophia-orchestator/internal/ports/inbound/event_types.go` and fails the build if any orch constant is missing from CLI's `knownEvents` map. Adding `EventPhaseArchived` to orch without mirroring it in CLI breaks the wire alignment test and blocks all orch development.

**Discipline.** Single PR pairing orch + CLI. Both repos' commits MUST land together (operator-approved single checkpoint). No feature flag bypass, no skip path. The orch + CLI changes are co-dependent and ship as one atomic change.

**Tradeoff.** Requires explicit cross-repo coordination, but the CI gate makes this self-enforcing — a partial PR cannot land green.

**Alternatives considered.**
- **A**: Land orch first behind a feature flag, then CLI. REJECTED — `wire_alignment_test` is an unconditional unit test; it does not respect runtime flags.
- **B**: Mirror CLI first (declare the constant), then orch. REJECTED — CLI's `knownEvents` is allowed to contain unknown values only on the CLI-only allowlist; an orch-mirrored constant without an orch source fails E3 of the wire alignment test.

### D-PRE-4: Worker publisher interface for M2-agnostic transport

**Why.** V4.1 §16 defers worker transport (SSE / webhook / message bus) to M2. PRE-0 must keep the publisher interface transport-agnostic so M2 can plug in a concrete implementation without rewriting the handler. Memory-engine already has a `outbound.EventPublisher` interface for its OWN domain events at `internal/ports/outbound/event_publisher.go:25`, but that publisher is in-process and not suitable for consuming external orch events. PRE-0 introduces a SEPARATE `consolidation.EventSubscriber` interface that models the consumption-side of remote events.

**Pattern.** Define a new internal package (proposed: `sophia-memory-engine/internal/application/consolidation/`) with:

```go
// EventSubscriber subscribes a handler to an event stream. The transport
// (SSE client, webhook server, message bus consumer) is chosen at wiring
// time. Cancellation of ctx stops the subscription.
type EventSubscriber interface {
    Subscribe(ctx context.Context, eventType string, handler EventHandler) error
}

// EventHandler processes a single received event. Returning an error
// triggers the subscriber's transport-specific retry/backoff policy.
type EventHandler func(ctx context.Context, payload PhaseArchivedReceived) error

// PhaseArchivedReceived is the worker-side mirror of the orch-side
// PhaseArchivedPayload. The string literal "phase.archived" is duplicated
// here (not imported) because the three repos are independent Go modules.
type PhaseArchivedReceived struct {
    ChangeID   string    `json:"change_id"`
    ChangeName string    `json:"change_name"`
    PhaseType  string    `json:"phase_type"`
    ArchivedAt time.Time `json:"archived_at"`
}
```

The PRE-0 production wiring in `cmd/workers/main.go` uses a `// TODO(M2)` placeholder — either a typed-nil subscriber with a logged warning at boot, or an explicit noop subscriber that logs "transport deferred to M2" once and exits the subscription goroutine cleanly. Tests use `FakePublisher` (renamed `FakeSubscriber` for symmetry with the interface) that lets the test drive synthetic events into the handler.

**Tradeoff.** Ships a one-method interface plus a fake one milestone before the real transport exists. Cost is minimal (interface + fake + handler stub). Benefit is that M2 implements a concrete `EventSubscriber` and wires it without touching `cmd/workers/main.go` structure or the handler signature.

**Clock injection.** The handler accepts an injected `shared.Clock` via the consolidation package's constructor so any `time.Now()` use during consolidation logic is testable. PRE-0's handler only logs — no time-dependent logic yet — but the seam exists.

**Alternatives considered.**
- **A**: Pick SSE in PRE-0. REJECTED — V4.1 explicitly defers; cross-repo SSE client adds dependency on orch's HTTP shape.
- **B**: Pick webhook receiver in PRE-0. REJECTED — adds an HTTP server to the worker process; M2 decision not made.
- **C**: Reuse memory-engine's existing `outbound.EventPublisher`. REJECTED — that publisher is in-process and emits memory-engine's own domain events; it cannot consume orchestator's SSE/webhook/bus events. Different direction (publish vs subscribe), different scope (local vs remote).

### D-PRE-5: `mcp_providers[]` TOML schema via existing `BurntSushi/toml` loader, allowlist as middleware

**Why.** `sophia-agent-mcp` already uses `BurntSushi/toml` (`loader.go:24`). Adding a new YAML parser dependency would violate the V4.1 "no unnecessary deps" posture. TOML's array-of-tables (`[[mcp_providers]]`) is semantically equivalent to V4.1's YAML list examples (cf. `explore.md:67-85`). The loader already invokes `toml.DecodeFile(path, &cfg)`; adding a `MCPProviders []MCPProviderConfig` field to `Config` is sufficient — TOML decoding picks up `[[mcp_providers]]` sections automatically.

**Allowlist as thin middleware.** A new file `sophia-agent-mcp/internal/infrastructure/mcp/allowlist.go` defines an `AllowlistEnforcer` that wraps MCP tool invocation. For a given provider `id`, it rejects any tool call whose name is not in `tools_allowed`. The enforcer is constructed at boot from `cfg.MCPProviders` and exposes a single `Authorize(providerID, toolName string) error` method that callers invoke before forwarding the tool call. PRE-0 ships the enforcer + tests but does NOT yet wire it into the MCP tool dispatch path — that wiring is deferred to M-KNOW-INIT-0 when Graphify is registered as the first concrete provider.

**Tradeoff.** Splits "config + enforcer exists" (PRE-0) from "enforcer wired into dispatch" (M-KNOW-INIT-0). Acceptable because PRE-0's acceptance criterion #5 only requires that the schema loads, validates, and the allowlist module is importable/testable.

**Alternatives considered.**
- **A**: YAML with `yaml.v3`. REJECTED — new dependency; mismatches existing loader (cf. `proposal.md:34-36`).
- **B**: Inline allowlist check inside each MCP handler. REJECTED — scatters policy across the codebase; duplicates allowlist lookup logic.
- **C**: Generate enforcers per-provider at startup. REJECTED — premature; one centralized enforcer with a `map[string]map[string]struct{}` lookup is O(1) and sufficient.

---

## Component design

### 1. Migration 005 — FTS simple (memory-engine)

**Files (NEW).**
- `sophia-memory-engine/migrations/postgres/005_fts_simple.up.sql`
- `sophia-memory-engine/migrations/postgres/005_fts_simple.down.sql`

**Up SQL (idempotent).**

```sql
-- 005_fts_simple.up.sql
-- Switch FTS language from 'spanish' to 'simple' across all 3 FTS tables.
-- The existing BEFORE INSERT/UPDATE triggers (trg_memories_fts,
-- trg_decisions_fts, trg_heuristics_fts) read NEW.fts_language per row
-- and rebuild search_vector. A no-op UPDATE forces the trigger to run.
--
-- Idempotent: WHERE fts_language = 'spanish' makes re-application a no-op.

BEGIN;

UPDATE memories   SET fts_language = 'simple' WHERE fts_language = 'spanish';
UPDATE decisions  SET fts_language = 'simple' WHERE fts_language = 'spanish';
UPDATE heuristics SET fts_language = 'simple' WHERE fts_language = 'spanish';

ALTER TABLE memories   ALTER COLUMN fts_language SET DEFAULT 'simple';
ALTER TABLE decisions  ALTER COLUMN fts_language SET DEFAULT 'simple';
ALTER TABLE heuristics ALTER COLUMN fts_language SET DEFAULT 'simple';

COMMIT;
```

**Down SQL (idempotent).**

```sql
-- 005_fts_simple.down.sql

BEGIN;

UPDATE memories   SET fts_language = 'spanish' WHERE fts_language = 'simple';
UPDATE decisions  SET fts_language = 'spanish' WHERE fts_language = 'simple';
UPDATE heuristics SET fts_language = 'spanish' WHERE fts_language = 'simple';

ALTER TABLE memories   ALTER COLUMN fts_language SET DEFAULT 'spanish';
ALTER TABLE decisions  ALTER COLUMN fts_language SET DEFAULT 'spanish';
ALTER TABLE heuristics ALTER COLUMN fts_language SET DEFAULT 'spanish';

COMMIT;
```

**Code-side companions.**
- `sophia-memory-engine/internal/adapters/outbound/search/postgres_fts.go:110, 112, 117` — replace `'spanish'` literals with `'simple'` in the three `plainto_tsquery` / `ts_headline` call sites.
- `sophia-memory-engine/internal/domain/memory/memory.go:119` — change `FTSLanguage: "spanish"` to `FTSLanguage: "simple"`.

**Test plan.**
- **Integration (testcontainers PG)** — new test file under `sophia-memory-engine/internal/adapters/outbound/search/`:
  - Fixture: insert one memory + one decision + one heuristic with English content BEFORE running migration 005.
  - Apply migration 005 via `golang-migrate`.
  - Assert: `SELECT fts_language FROM memories|decisions|heuristics` returns `'simple'` for fixture rows.
  - Assert: `column_default` for `fts_language` on all 3 tables is `'simple'::regconfig`.
  - Assert: `plainto_tsquery('simple', 'english query')` against `search_vector` returns the fixture row on all 3 tables.
  - Idempotency: run `005.up` twice — second run is a no-op (verified by no error + same row counts).
  - Down: run `005.down` then re-run `005.up` — round-trips cleanly.
- **Unit** — table-driven test for `postgres_fts.go` SQL strings: assert all three updated literals contain `'simple'` (catches accidental regressions).

### 2. `EventPhaseArchived` (orch + CLI)

**Files (modified).**
- `sophia-orchestator/internal/ports/inbound/event_types.go` — add `EventPhaseArchived` constant + entry in `knownEventTypes` map.
- `sophia-orchestator/internal/ports/inbound/event_payloads.go` — add `PhaseArchivedPayload` struct.
- `sophia-orchestator/internal/application/phase/service.go` — emit at archive completion inside `advanceChange` (around L911).
- `sophia-cli/pkg/contract/events.go` — mirror constant in section 1 + add to `knownEvents` map.

**Constant + payload (orch).**

```go
// internal/ports/inbound/event_types.go
const (
    // ... existing constants ...

    // EventPhaseArchived is published once when a change's archive phase
    // reaches DONE and the change transitions to Completed. Distinct from
    // EventPhaseCompleted (which fires on every phase's DONE) because the
    // consolidation worker subscribes specifically to archive-completion
    // (V4.1 D13 — filtering phase.completed by status is rejected).
    EventPhaseArchived = "phase.archived"
)

// add to knownEventTypes map:
//     EventPhaseArchived: {},
```

```go
// internal/ports/inbound/event_payloads.go

// PhaseArchivedPayload is the payload of phase.archived. Emitted by
// application/phase/service.go inside advanceChange when the just-
// completed phase is PhaseArchive AND the change has been marked
// Completed. Carries enough identifying data for the memory-engine
// consolidation worker to fetch the full change context from
// memory-engine without subscribing to upstream phase.completed events.
type PhaseArchivedPayload struct {
    ChangeID   string    `json:"change_id"`
    ChangeName string    `json:"change_name"`
    PhaseType  string    `json:"phase_type"`   // always "archive"
    ArchivedAt time.Time `json:"archived_at"`  // s.d.Clock.Now() at emission
}
```

**Emission point (orch).** Inside `Service.advanceChange` at `phase/service.go:911`. Modified block:

```go
func (s *Service) advanceChange(ctx context.Context, c *change.Change, completed phase.PhaseType) {
    if err := c.AdvancePhase(completed, s.d.Clock.Now()); err == nil {
        _ = s.d.ChangeRepo.Save(ctx, c)
    }
    if completed == phase.PhaseArchive {
        if err := c.MarkCompleted(s.d.Clock.Now()); err == nil {
            if saveErr := s.d.ChangeRepo.Save(ctx, c); saveErr == nil {
                // Iron Law D1.2: envelope persisted upstream by
                // runPhaseCompletion; change terminal state durable above;
                // safe to emit caller-visible event.
                phaseID := /* resolve from c.CurrentPhase() or caller param */
                s.publishEvent(ctx, phaseID, contract.EventPhaseArchived, inbound.PhaseArchivedPayload{
                    ChangeID:   c.ID().String(),
                    ChangeName: c.Name(),
                    PhaseType:  string(phase.PhaseArchive),
                    ArchivedAt: s.d.Clock.Now(),
                })
            }
        }
    }
}
```

Spec phase will lock the exact `phaseID` resolution mechanism — either threading `phaseID` as a new parameter to `advanceChange` (caller already has it) or reading it from `c.CurrentPhase()`. Both are mechanically simple; design defers the choice to tasks for minimum-diff selection.

**Mirror (CLI).** Add `EventPhaseArchived = "phase.archived"` to `sophia-cli/pkg/contract/events.go` Section 1 (around L19, after `EventPhaseNeedsContext`) and add `EventPhaseArchived: {}` to `knownEvents` map.

**Test plan.**
- **Unit (orch)** — new test in `internal/application/phase/service_test.go` (or a new `archive_event_test.go`):
  - Setup: build a `Service` with a fake `EventPublisher` that records all published events, a fake `Clock` returning a fixed `time.Time`, a fake `ChangeRepo` that accepts `Save`, and a `change.Change` in archive-running state.
  - Act: invoke `advanceChange(ctx, change, phase.PhaseArchive)`.
  - Assert: exactly one event of type `EventPhaseArchived` was published; payload matches `PhaseArchivedPayload{ChangeID, ChangeName, "archive", clock.Now()}`.
  - Negative case: call `advanceChange` with `phase.PhaseTasks` (non-archive) → assert NO `EventPhaseArchived` published.
  - Failure path: `ChangeRepo.Save` returns error after `MarkCompleted` → assert NO `EventPhaseArchived` published (Iron Law D1.2 — event only after durable state).
  - Idempotency: call `advanceChange` twice with `PhaseArchive` → first emits, second emits OR is suppressed (spec phase locks behavior; design records that the test must assert one of two well-defined behaviors).
- **Unit (CLI)** — existing `TestWireAlignment_OrchEventsMirrored` automatically validates the mirror. Additionally add a one-line assertion in `events_test.go` (if present) that `IsKnownEvent("phase.archived") == true`.
- **Integration** — none required for PRE-0. M-KNOW-INIT-0 will cover end-to-end orch→worker delivery once a transport is chosen.

### 3. Worker skeleton (memory-engine)

**Files.**
- `sophia-memory-engine/cmd/workers/main.go` — modified (replace 5-line stub).
- `sophia-memory-engine/internal/application/consolidation/` — NEW package (proposed name).
  - `consolidation/subscriber.go` — defines `EventSubscriber`, `EventHandler`, `PhaseArchivedReceived`.
  - `consolidation/handler.go` — defines the PRE-0 stub handler that logs receipt.
  - `consolidation/fake_subscriber.go` — `FakeSubscriber` for tests.
  - `consolidation/handler_test.go` — exercises handler via `FakeSubscriber`.

**Subscriber interface.**

```go
// internal/application/consolidation/subscriber.go
package consolidation

import (
    "context"
    "time"
)

// EventSubscriber subscribes a handler to a remote orchestator event
// stream. Transport (SSE / webhook / message bus) is chosen at wiring
// time in cmd/workers/main.go; see V4.1 §16 M2 for the transport
// decision (deferred from M-KNOW-PRE-0).
//
// Cancellation of ctx stops the subscription; concrete implementations
// MUST exit cleanly when ctx is done.
type EventSubscriber interface {
    Subscribe(ctx context.Context, eventType string, handler EventHandler) error
}

// EventHandler processes a single received event. A non-nil error
// triggers the subscriber's transport-specific retry/backoff policy
// (defined by the M2 implementation); PRE-0's stub handler always
// returns nil.
type EventHandler func(ctx context.Context, payload PhaseArchivedReceived) error

// PhaseArchivedReceived is the worker-side mirror of the orch's
// inbound.PhaseArchivedPayload. The shape MUST stay byte-compatible
// with the orch-side JSON wire format. Field names match the orch
// struct exactly; the three repos are independent Go modules so this
// duplication is intentional (cf. explore.md:108-114).
type PhaseArchivedReceived struct {
    ChangeID   string    `json:"change_id"`
    ChangeName string    `json:"change_name"`
    PhaseType  string    `json:"phase_type"`
    ArchivedAt time.Time `json:"archived_at"`
}

// PhaseArchivedEventType is the string the subscriber filters on. Kept
// as a local literal (not imported from orch) because the three Go
// modules are independent.
const PhaseArchivedEventType = "phase.archived"
```

**Stub handler.**

```go
// internal/application/consolidation/handler.go
package consolidation

import (
    "context"
    "log/slog"

    "github.com/sophia-engine/memory-engine/internal/domain/shared"
)

// Handler is the consolidation pipeline's entrypoint for archive
// completion. PRE-0 stub: log receipt + return nil. M2 will replace
// the body with the actual consolidation work (episodic → semantic
// promotion, heuristic emission, ProjectDNA update).
type Handler struct {
    log   *slog.Logger
    clock shared.Clock
}

func NewHandler(log *slog.Logger, clock shared.Clock) *Handler {
    return &Handler{log: log, clock: clock}
}

// Handle is the EventHandler the worker registers with the
// EventSubscriber. M2 will inject the consolidation use-cases here;
// PRE-0 logs and returns nil.
func (h *Handler) Handle(ctx context.Context, payload PhaseArchivedReceived) error {
    h.log.InfoContext(ctx, "phase.archived received",
        slog.String("change_id", payload.ChangeID),
        slog.String("change_name", payload.ChangeName),
        slog.Time("archived_at", payload.ArchivedAt),
        slog.Time("received_at", h.clock.Now()),
    )
    return nil
}
```

**Fake subscriber for tests.**

```go
// internal/application/consolidation/fake_subscriber.go
package consolidation

import (
    "context"
    "sync"
)

// FakeSubscriber is a test fake for EventSubscriber. Subscribe records
// the handler; Emit synchronously drives a synthetic payload into the
// recorded handler. Used by handler_test.go and any future test that
// needs to exercise the consolidation pipeline without a real transport.
type FakeSubscriber struct {
    mu       sync.Mutex
    handlers map[string]EventHandler
}

func NewFakeSubscriber() *FakeSubscriber {
    return &FakeSubscriber{handlers: make(map[string]EventHandler)}
}

func (f *FakeSubscriber) Subscribe(ctx context.Context, eventType string, handler EventHandler) error {
    f.mu.Lock()
    defer f.mu.Unlock()
    f.handlers[eventType] = handler
    return nil
}

// Emit drives payload synchronously into the handler registered for
// eventType. Returns the handler's error verbatim, or nil if no handler
// was registered (subscribe-before-emit is the test's responsibility).
func (f *FakeSubscriber) Emit(ctx context.Context, eventType string, payload PhaseArchivedReceived) error {
    f.mu.Lock()
    h, ok := f.handlers[eventType]
    f.mu.Unlock()
    if !ok {
        return nil
    }
    return h(ctx, payload)
}
```

**Worker entrypoint.**

```go
// cmd/workers/main.go
package main

import (
    "context"
    "log/slog"
    "os"
    "os/signal"
    "syscall"

    "github.com/sophia-engine/memory-engine/internal/application/consolidation"
    "github.com/sophia-engine/memory-engine/internal/domain/shared"
)

func main() {
    log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
    clock := shared.SystemClock{}

    ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer cancel()

    handler := consolidation.NewHandler(log, clock)

    // TODO(M2): replace with concrete EventSubscriber (SSE client,
    // webhook receiver, or message bus consumer). PRE-0 ships a noop
    // subscriber so the binary builds, the graceful lifecycle is
    // exercised, and tests can substitute FakeSubscriber.
    var subscriber consolidation.EventSubscriber // nil — see TODO above
    if subscriber == nil {
        log.WarnContext(ctx, "worker started with no EventSubscriber wired; awaiting M2 transport decision",
            slog.String("event_type", consolidation.PhaseArchivedEventType),
        )
        <-ctx.Done()
        log.InfoContext(ctx, "worker shutting down", slog.String("cause", ctx.Err().Error()))
        return
    }

    if err := subscriber.Subscribe(ctx, consolidation.PhaseArchivedEventType, handler.Handle); err != nil {
        log.ErrorContext(ctx, "subscribe failed", slog.String("err", err.Error()))
        os.Exit(1)
    }

    <-ctx.Done()
    log.InfoContext(ctx, "worker shutting down", slog.String("cause", ctx.Err().Error()))
}
```

**Clock injection.** `shared.SystemClock{}` is the production clock; tests pass a fake `shared.Clock`. No `time.Now()` is called in `consolidation/`.

**Test plan.**
- **Unit** — `consolidation/handler_test.go`:
  - Setup: `FakeSubscriber`, `Handler` with a captured `slog.Logger` (use `slog.NewJSONHandler` over a `bytes.Buffer`) and a `shared.FakeClock`.
  - Subscribe: `sub.Subscribe(ctx, PhaseArchivedEventType, handler.Handle)`.
  - Emit: `sub.Emit(ctx, PhaseArchivedEventType, PhaseArchivedReceived{ChangeID: "...", ChangeName: "...", PhaseType: "archive", ArchivedAt: fixedTime})`.
  - Assert: log buffer contains `"phase.archived received"`, change_id, change_name, archived_at, and received_at (clock.Now()).
  - Assert: handler returned nil.
  - Negative: emit with no handler subscribed → `Emit` returns nil, log buffer empty.
- **Unit** — `cmd/workers/main_test.go`:
  - Build-only test (verify package compiles) — ship as `TestMainBuilds` that imports `main` via `go test ./cmd/workers/...`.
  - Optional: smoke test the graceful shutdown by sending SIGTERM to a goroutine running the body (deferred to spec phase if cost vs benefit warrants).

### 4. `mcp_providers[]` config + allowlist (agent-mcp)

**Files.**
- `sophia-agent-mcp/internal/infrastructure/config/config.go` — modified (add struct + field).
- `sophia-agent-mcp/internal/infrastructure/config/loader.go` — modified (add validation block).
- `sophia-agent-mcp/internal/infrastructure/mcp/allowlist.go` — NEW.
- `sophia-agent-mcp/internal/infrastructure/mcp/allowlist_test.go` — NEW.
- `sophia-agent-mcp/internal/infrastructure/config/loader_test.go` — modified or NEW (test the new validation path).

**Config struct addition.**

```go
// internal/infrastructure/config/config.go

// MCPProviderConfig declares one external MCP provider the bridge is
// permitted to spawn and proxy. V4.1 §16 / M-KNOW-INIT-0 ships Graphify
// as the first concrete provider; PRE-0 only defines the schema +
// validation + allowlist enforcement, no specific provider values.
//
// In TOML:
//
//   [[mcp_providers]]
//   id = "graphify"
//   package = "graphifyy[mcp]==0.8.35"
//   command = "graphify serve graphify-out/graph.json"
//   transport = "stdio"
//   tools_allowed = ["query_graph", "get_node", ...]
//   lifecycle = "spawned_per_change"
type MCPProviderConfig struct {
    // ID is a short, unique identifier for the provider (e.g. "graphify").
    // Used as the lookup key in the allowlist enforcer. Required, non-empty.
    ID string `toml:"id"`

    // Package is the installable package spec (e.g. PyPI). Optional —
    // documentation only; the bridge does NOT install packages itself.
    Package string `toml:"package"`

    // Command is the shell command the bridge spawns to launch the MCP
    // server process. Required, non-empty.
    Command string `toml:"command"`

    // Transport identifies the MCP transport protocol. Allowed values:
    // "stdio" (PRE-0 only). Future values ("sse", "http") will be added
    // when the bridge gains transport-specific dispatch.
    Transport string `toml:"transport"`

    // ToolsAllowed is the closed allowlist of tool names the provider is
    // permitted to expose. Calls to tool names NOT in this slice are
    // rejected by the AllowlistEnforcer. Required, non-empty.
    ToolsAllowed []string `toml:"tools_allowed"`

    // Lifecycle controls when the provider process is spawned and reaped.
    // Allowed values:
    //   "spawned_per_change" — fresh process per SDD change (default)
    //   "long_lived"         — single process reused across changes
    Lifecycle string `toml:"lifecycle"`
}

// Add to Config (after EngramConfig field):
//
//     // MCPProviders declares external MCP providers the bridge is permitted
//     // to spawn. Empty slice = no providers (PRE-0 default; Graphify wires
//     // in at M-KNOW-INIT-0).
//     MCPProviders []MCPProviderConfig `toml:"mcp_providers"`
```

**Loader validation block.**

```go
// internal/infrastructure/config/loader.go — add to validate(cfg Config):

const (
    transportStdio              = "stdio"
    lifecycleSpawnedPerChange   = "spawned_per_change"
    lifecycleLongLived          = "long_lived"
)

var validTransports = map[string]struct{}{transportStdio: {}}
var validLifecycles = map[string]struct{}{
    lifecycleSpawnedPerChange: {},
    lifecycleLongLived:        {},
}

// Inside validate(), after existing checks:
seenIDs := make(map[string]struct{}, len(cfg.MCPProviders))
for i, p := range cfg.MCPProviders {
    if p.ID == "" {
        return fmt.Errorf("config: mcp_providers[%d].id must not be empty", i)
    }
    if _, dup := seenIDs[p.ID]; dup {
        return fmt.Errorf("config: mcp_providers[%d].id %q is duplicated", i, p.ID)
    }
    seenIDs[p.ID] = struct{}{}
    if p.Command == "" {
        return fmt.Errorf("config: mcp_providers[%d].command must not be empty (id=%q)", i, p.ID)
    }
    if _, ok := validTransports[p.Transport]; !ok {
        return fmt.Errorf("config: mcp_providers[%d].transport %q is not allowed (id=%q); valid: stdio", i, p.Transport, p.ID)
    }
    if len(p.ToolsAllowed) == 0 {
        return fmt.Errorf("config: mcp_providers[%d].tools_allowed must not be empty (id=%q)", i, p.ID)
    }
    if _, ok := validLifecycles[p.Lifecycle]; !ok {
        return fmt.Errorf("config: mcp_providers[%d].lifecycle %q is not allowed (id=%q); valid: spawned_per_change, long_lived", i, p.Lifecycle, p.ID)
    }
}
```

**Allowlist enforcer.**

```go
// internal/infrastructure/mcp/allowlist.go
package mcp

import (
    "errors"
    "fmt"

    "github.com/sophia-engine/agent-mcp/internal/infrastructure/config"
)

// ErrToolNotAllowed signals that a tool invocation was rejected because
// the tool name is not in the provider's tools_allowed list.
var ErrToolNotAllowed = errors.New("mcp: tool not in provider allowlist")

// ErrUnknownProvider signals that a tool invocation referenced a
// provider id that is not in the loaded mcp_providers[] config.
var ErrUnknownProvider = errors.New("mcp: unknown provider")

// AllowlistEnforcer rejects MCP tool invocations whose (provider, tool)
// pair is not declared in the loaded config. Constructed once at boot
// from cfg.MCPProviders; safe for concurrent read-only access.
type AllowlistEnforcer struct {
    // provider id -> set of allowed tool names
    allowed map[string]map[string]struct{}
}

// NewAllowlistEnforcer builds an enforcer from the validated config.
// providers MUST have already passed config.validate (no empty IDs, no
// duplicates, non-empty tools_allowed per provider).
func NewAllowlistEnforcer(providers []config.MCPProviderConfig) *AllowlistEnforcer {
    a := &AllowlistEnforcer{allowed: make(map[string]map[string]struct{}, len(providers))}
    for _, p := range providers {
        tools := make(map[string]struct{}, len(p.ToolsAllowed))
        for _, t := range p.ToolsAllowed {
            tools[t] = struct{}{}
        }
        a.allowed[p.ID] = tools
    }
    return a
}

// Authorize returns nil if (providerID, toolName) is allowed, else
// ErrUnknownProvider or ErrToolNotAllowed. The error is wrapped with
// %w so callers can errors.Is against the sentinels.
func (a *AllowlistEnforcer) Authorize(providerID, toolName string) error {
    tools, ok := a.allowed[providerID]
    if !ok {
        return fmt.Errorf("%w: provider=%q", ErrUnknownProvider, providerID)
    }
    if _, ok := tools[toolName]; !ok {
        return fmt.Errorf("%w: provider=%q tool=%q", ErrToolNotAllowed, providerID, toolName)
    }
    return nil
}
```

**Test plan.**
- **Unit (config loader)** — `internal/infrastructure/config/loader_test.go`:
  - Table-driven cases:
    - Valid: full TOML fixture with 2 providers (Graphify-like + a second) → loads + validates.
    - Empty id → error matches `mcp_providers[%d].id must not be empty`.
    - Duplicate id → error matches `is duplicated`.
    - Empty command → error matches `command must not be empty`.
    - Unknown transport (e.g. `"sse"`) → error matches `transport %q is not allowed`.
    - Empty `tools_allowed` → error matches `tools_allowed must not be empty`.
    - Unknown lifecycle (e.g. `"forever"`) → error matches `lifecycle %q is not allowed`.
  - Empty `MCPProviders` (PRE-0 default) → loads successfully.
- **Unit (allowlist)** — `internal/infrastructure/mcp/allowlist_test.go`:
  - Setup: `NewAllowlistEnforcer([]config.MCPProviderConfig{{ID: "graphify", ToolsAllowed: []string{"query_graph", "get_node"}}})`.
  - Authorize("graphify", "query_graph") → nil.
  - Authorize("graphify", "delete_graph") → `errors.Is(err, ErrToolNotAllowed) == true`.
  - Authorize("unknown_provider", "anything") → `errors.Is(err, ErrUnknownProvider) == true`.
  - Empty providers list: any Authorize call → `ErrUnknownProvider`.

---

## Test strategy

- **Strict TDD enforced.** Each component above starts with a failing test in the apply phase. Spec phase locks the test acceptance contracts; apply phase implements test-first per `strict-tdd.md`.
- **Per repo runners** (from `sdd-init/2026` cache):
  - `sophia-memory-engine`: `make test-unit` for handler / fake subscriber / loader; `make test-integration` (testcontainers PG, `-tags=integration`, 5m) for migration 005.
  - `sophia-orchestator`: `make test-unit` for `advanceChange` emission (fake `EventPublisher` + fake `Clock`); no integration test required for PRE-0.
  - `sophia-cli`: `make test` — existing `TestWireAlignment_OrchEventsMirrored` covers the constant mirror automatically.
  - `sophia-agent-mcp`: `make test` — unit-only for config loader + allowlist enforcer.
- **Patterns** (from explore.md:160-165):
  - Table-driven subtests + `testify/require`.
  - testcontainers PG for FTS integration; reuse the existing test harness in `sophia-memory-engine`.
  - Goroutine + per-subscriber channel pattern is NOT required for PRE-0 worker — the `FakeSubscriber.Emit` is synchronous because the production transport is deferred.
- **No new test infra**: no new dependencies, no new test fixtures beyond what each repo already has.
- **Wire alignment**: rely entirely on `sophia-cli/pkg/contract/wire_alignment_test.go`. No new cross-repo test infrastructure.

---

## Risks revisited (from proposal)

| Risk | Mitigation in this design |
|---|---|
| FTS migration scope wider than V4.1 stated (3 tables) | **D-PRE-1** — migration 005 explicitly covers `memories`, `decisions`, `heuristics` with idempotent `WHERE fts_language = 'spanish'` predicate; integration test asserts all 3 defaults after migration. |
| Wire alignment hard CI gate (orch + CLI must land together) | **D-PRE-3** — single cross-repo PR is mandatory, no feature flag bypass; `wire_alignment_test` is the unconditional gate; design lists exact mirror file/section/line in CLI. |
| Worker transport unresolved at PRE-0 | **D-PRE-4** — `EventSubscriber` interface + `FakeSubscriber` lets M2 plug any transport without touching the handler or `cmd/workers/main.go` structure; production wiring is an explicit `// TODO(M2)` with a logged warning. |
| TOML vs YAML mismatch with V4.1 doc examples | **D-PRE-5** — choose TOML (existing loader, no new dep); document the format in TOML comment; V4.1 doc patch is a separate non-PRE-0 follow-up. |
| Go toolchain drift across repos (1.26.2 vs 1.26.3) | Each repo's `go.mod toolchain` directive is unchanged; PRE-0 introduces no version pin changes. Non-blocking; tracked in proposal risks. |
| FTS migration applied without rebuilding legacy rows | **D-PRE-1** — `UPDATE ... SET fts_language = 'simple'` triggers the existing `BEFORE INSERT OR UPDATE` per-row trigger which rebuilds `search_vector` automatically. No manual reindex; verified by integration test asserting `plainto_tsquery('simple', 'english query')` matches the updated fixture rows. |
| Iron Law D1.2 violation (event before envelope/state durable) | **D-PRE-2** — emission is INSIDE `advanceChange`, AFTER `c.MarkCompleted` + `ChangeRepo.Save` returns nil; unit test asserts no emission on `Save` failure. |
| Clock direct call in domain/application | **D-PRE-2 / D-PRE-4** — `ArchivedAt` uses `s.d.Clock.Now()`; worker handler accepts `shared.Clock` via constructor. Forbidigo lint already blocks direct `time.Now()` per `golangci.yml`. |

---

## Out of scope reaffirmed

- Worker transport choice (SSE / webhook / message bus). DEFERRED to M2 per `proposal.md:40`.
- Any Graphify-specific provider config values. M-KNOW-INIT-0 owns Graphify wiring.
- Any consolidation business logic inside the worker handler. PRE-0 logs receipt only.
- V4.1 strategy doc YAML→TOML patch. Separate documentation change.
- FTS query reformulation, ranking change, or query parser change beyond the language switch.
- New index strategies on `memories` / `decisions` / `heuristics`. The trigger + existing GIN index continue to apply.
- Cross-module replace directives, `go.work` changes, shared-module extraction. The three Go modules remain independent (cf. `proposal.md:46`).
- Wiring the `AllowlistEnforcer` into the MCP dispatch path. Module exists + is tested in PRE-0; integration into the request pipeline is M-KNOW-INIT-0's responsibility when Graphify becomes the first concrete provider.
