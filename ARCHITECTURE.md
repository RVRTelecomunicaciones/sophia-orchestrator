# Architecture — sophia-orchestator

> Grounded in the code (read directly + an AST knowledge graph of the 372 Go
> files), **not** in the openspec artifacts — those drift from the code. Every
> non-obvious claim cites `file:line`.

## 1. What this service is

`sophia-orchestator` is the **deterministic SDD workflow coordinator** of the
Sophia ecosystem. Its single responsibility is to drive a *Change* through the
canonical SDD phases, emitting a **validated Envelope** at every transition and
persisting it **before** any caller-visible state change.

It does NOT decide policy (→ agent-governance-core), does NOT store knowledge
(→ sophia-memory-engine), does NOT execute side effects (→ sophia-runtime-adapters),
and is NOT the LLM (the model runs inside the dispatcher: opencode subprocess or
the host MCP bridge).

## 2. Hexagonal layering

Dependencies point inward only; the AST graph shows **zero import cycles**.

```
cmd/sophia-orchestator/main.go        process entrypoint (serve | reeval)
        │
internal/bootstrap/wire.go            composition root — Wire() assembles everything
        │
internal/adapters/inbound/http/       HTTP boundary (chi router + handlers)
        │
internal/application/                 use-case services (orchestration logic)
        │
internal/domain/                      aggregates + value objects + the phase state machine
        │
internal/ports/{inbound,outbound}     interfaces
        │
internal/adapters/outbound/           pg · governance · memory · runtime · dispatcher · ...
internal/infrastructure/              config · observability (slog/otel)
```

Core abstractions (graph god nodes, by edge degree): `Wire()` (DI bridge, highest
betweenness), `newRunService()` (apply engine), `NewPromptBuilder()` (per-phase
prompt assembly), `ParseChangeID()`/`ParsePhaseID()` (ULID parsing, ubiquitous).

## 3. The SDD phase lifecycle

The 9 canonical phase types and their **strictly sequential** order are defined
in `internal/domain/phase/type.go` (`PhaseType` enum + `NextValid()`):

```
init → explore → proposal → spec → design → tasks → apply → verify → archive
```

`NextValid()` is the single source of transition truth — the run handler
(`internal/application/phase/service.go`, `isNextValidTransition`) delegates to
it, so there is no separate hardcoded map to drift.

> **spec → design are SEQUENTIAL** (design depends on / reads spec), matching the
> reference `cortex-ia` and the broader SDD field (Kiro, BMAD, SPARC, OpenSpec).
> Earlier the domain modeled them as either/or (`proposal → {spec, design}`,
> both → tasks), which made a single `currentPhase` change run only ONE of them.
> Fixed so both always run.

Per-phase confidence gates (`type.go` `ConfidenceThreshold`): explore 0.5;
proposal/design/apply 0.7; spec/tasks 0.8; verify/archive 0.9. Below threshold
the phase becomes `done_with_concerns` or `blocked`.

## 4. Phase execution flow

`POST /api/v1/changes/{id}/phases/{phase}/run` → `PhasesHandler.Run`
(`internal/adapters/inbound/http/handlers/phases.go`) → `phase.Service` runs
(`internal/application/phase/service.go`):

1. Validate the transition against `currentPhase.NextValid()`; reject otherwise
   (`ErrInvalidTransition`). Only one phase runs per change at a time
   (`ErrPhaseRunning`).
2. Create + **persist** the Phase row as `pending` BEFORE the work goroutine
   (`service.go:314,349`).
3. Governance decision (`s.d.Governance.EvaluatePhase`, `service.go:394`) →
   allow / allow_with_constraints / require_approval (HARD-GATE) / deny.
4. Dispatch to the LLM (`s.d.Dispatcher.Dispatch`, `service.go:578`); the agent
   returns an Envelope (JSON).
5. Validate the Envelope (schema + confidence threshold).
6. Complete + **persist the Phase before returning** — `service.go:711` carries
   the literal invariant comment *"Iron Law #1: persisted-before-return"*
   (`PhaseRepo.Save`, `service.go:716`), then `advanceChange` moves
   `currentPhase` forward.

Long-running phases respond `202 Accepted` + SSE (`/api/v1/phases/{id}/events`),
never on the request thread.

## 5. The apply phase

The most complex phase, centered on `newRunService()`
(`internal/application/apply`). It reads the tasks artifact, builds a **board**
of **groups**, creates per-group **git worktrees** (via `shell.exec` to
runtime-adapters), and dispatches **implement agents** in parallel, bounded by
the **Spawn Governor** (`internal/application/discipline/spawn_governor.go`).

Concurrency caps are real values in `internal/infrastructure/config/config.go`
(`SpawnConfig`, ~line 418): global `Max: 6` slots (raised from 4 so the ceiling
exceeds the apply demand of `MaxParallelGroups × MaxParallelImplementsPerGroup`
= 2×2 = 4), stagger 200–500 ms, wait interval 250 ms, max wait 30 s. Overridable
via `SOPHIA_SPAWN_MAX`.

## 6. The dispatcher (pluggable LLM)

Selected in `internal/bootstrap/wire.go` by `cfg.Dispatcher.Provider`:
`opencode` (in-container subprocess, default) or `mcp` (host-side MCP bridge
`sophia-agent-mcp` over Streamable HTTP at `host.docker.internal:7775`). Other
adapters (ollama, aider) live under `internal/adapters/outbound/dispatcher/`.

## 7. Outbound ports (cross-service)

| Port | Service | Purpose |
|------|---------|---------|
| Governance | agent-governance-core (`SOPHIA_GOVERNANCE_URL`) | per-phase policy/approval |
| Memory | sophia-memory-engine (`SOPHIA_MEMORY_URL`) | persist SDD artifacts; learning loop (skill metrics via PATCH /skills) |
| Runtime | sophia-runtime-adapters (`SOPHIA_RUNTIME_URL`) | execute shell/git/side effects |
| Dispatcher | opencode / MCP bridge | the per-phase LLM |

## 8. Persistence

PostgreSQL via `pgx/v5` (`internal/adapters/outbound/pg`). Migrations under
`migrations/postgres/` (golang-migrate), auto-applied on boot when
`SOPHIA_DB_MIGRATE_ON_BOOT=true`. Tables: changes, phases (with `envelope`,
`concerns`), apply board (groups/tasks/sessions/worktrees), audit, spawn
governor state, skills + skill_usage, outbox, reeval_runs, phase_concerns.

## 9. Key invariants

- **D1.2 / Iron Law #1** — every phase persists its Envelope BEFORE any
  caller-visible state change (`service.go:711`).
- **5 Iron Laws** — enforced at phase boundaries (`internal/application/discipline`).
- **Determinism** — no direct `time.Now()` / `ulid.Make()` in domain/application;
  injectable `Clock` + `IDGenerator`.
- **Boundary discipline** — orchestrator never decides policy, stores memory, or
  executes side effects directly; always via an outbound port.

## 10. Where to start reading

1. `cmd/sophia-orchestator/main.go` — entrypoint.
2. `internal/bootstrap/wire.go` — how everything is wired (`Wire()`).
3. `internal/domain/phase/type.go` — the phase state machine.
4. `internal/application/phase/service.go` — the phase execution flow.
5. `internal/application/apply` — the apply board + spawn governor.

> Tip: a queryable AST knowledge graph lives in `graphify-out/` — run
> `graphify query "<question>"` to navigate the code structure.
