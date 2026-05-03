# CLAUDE.md — sophia-orchestator

## What this repo is

`sophia-orchestator` is the **deterministic SDD workflow coordinator** of the Sophia ecosystem. Its sole responsibility is to drive a SDD Change through the 9 canonical phases, enforcing envelope contracts, Iron Laws, and HARD-GATE markers between agent invocations.

## What this repo is NOT

- Not a memory engine (memory-engine handles that).
- Not a policy/approval engine (governance handles that).
- Not a side-effect executor (runtime-adapters handles that).
- Not an AI provider (LLM calls happen inside OpenCode subprocess).
- Not a generic workflow builder (V1 = SDD only).
- Not a distributed task scheduler (V1 uses goroutines + Postgres advisory locks).

## Required mindset

> **Coordinate with discipline. Do not invent state machines. Do not collapse boundaries.**

Every phase transition produces an Envelope. Every Envelope is persisted before any caller-visible state change. Every Iron Law is enforced at boundaries. The orchestrator never decides policy (governance does), never stores knowledge (memory-engine does), never executes side effects (runtime-adapters does).

## Must-read files before coding

1. `docs/superpowers/specs/2026-05-03-sophia-orchestator-design.md` — V1 spec, authoritative.
2. `docs/rules.md` — R-rules.
3. `docs/domain-invariants.md` — I-invariants.
4. `AGENTS.md`.

## Core design principles

- **D1.1** — Orchestrator coordinates. It does not decide policy, store memory, or execute side effects.
- **D1.2** — Every phase produces a validated Envelope before any state change.
- **D1.3** — The 5 Iron Laws are non-rationalizable. Anti-rationalization tables in spec Appendix A.
- **D1.4** — Apply phase parallelism is bounded by the Spawn Governor (default 2×2=4, cap 6).
- **D1.5** — Long-running phases use 202 Accepted + SSE; never request-thread.
- **D1.6** — Dispatcher is pluggable; OpenCode is V1 default, others V2.
- **D1.7** — Worktrees are managed by orchestrator via runtime-adapters; sophia is AI-provider-agnostic.

## Tech stack

- Language: Go 1.26+ (toolchain `go1.26.2`).
- DB: PostgreSQL 15+ via `pgx/v5`.
- HTTP router: `chi/v5`.
- Migrations: `golang-migrate`.
- Observability: OpenTelemetry + slog.
- Testing: `testify` + `testcontainers-go`.
- Lint: `golangci-lint` with `forbidigo`, `wrapcheck`, `errorlint`.

## Output style

Conventional commits (`feat(scope)`, `fix(scope)`, `chore(scope)`, `docs(scope)`, `test(scope)`). NEVER `Co-Authored-By` or AI attribution. Scope = layer (`domain`, `application`, `bootstrap`) or component (`change`, `phase`, `apply`, `session`, `pg`, `http`, `governance`, `dispatcher`, `discipline`).

## Never do this

1. Mix governance with orchestrator (governance decides, orchestrator orchestrates).
2. Store memory locally — use memory-engine via outbound port.
3. Execute side effects — call runtime-adapters via outbound port.
4. Bypass Iron Laws under operational pressure.
5. Direct `time.Now()` or `ulid.Make()` in domain/application — use injectable `Clock` and `IDGenerator`.
6. Run long-running phases on the request thread — use goroutine + SSE.
7. Spawn dispatcher subprocesses without going through `SpawnGovernor`.
8. Persist after returning — every phase persists Envelope BEFORE caller-visible state change.
