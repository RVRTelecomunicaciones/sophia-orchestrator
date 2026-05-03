# AGENTS.md — sophia-orchestator

This document gives an AI agent everything it needs to make a meaningful contribution to this repo without reading through history.

## Quick start

```bash
# Verify Go toolchain (must report go1.26.2; auto-downloaded by toolchain directive)
go env GOVERSION

go mod download
docker compose -f ops/local/compose.yaml up -d
make migrate-up
make build
make test-unit
```

**Go version**: this repo pins **Go 1.26.2** via `toolchain go1.26.2`. If your
system Go is 1.24.x or 1.25.x, that's fine — Go 1.21+ with `GOTOOLCHAIN=auto`
auto-downloads and uses the pinned toolchain. All commands compile/run on 1.26.2.

## Repository purpose

The orchestrator of the Sophia SDD workflow. See `CLAUDE.md` for principles, `docs/superpowers/specs/2026-05-03-sophia-orchestator-design.md` for the V1 design, `docs/rules.md` for R-rules, `docs/domain-invariants.md` for I-invariants.

## How to add a phase behavior

1. Update the relevant aggregate in `internal/domain/{change,phase,apply}/`. Add a state transition. Cover with unit tests (≥85% domain coverage gate; aim 100%).
2. If touching governance contract: write a new ADR in `docs/adr/`.
3. Update `internal/application/phase/run.go` use case if the run flow changes.
4. Update SSE event types in `internal/adapters/inbound/http/handlers/sse.go` if needed.
5. Update integration tests in `test/integration/`.

## How to add a dispatcher provider (V2+)

1. Implement `internal/ports/outbound/dispatcher.go` interface.
2. Add adapter at `internal/adapters/outbound/dispatcher/<name>/dispatcher.go`.
3. Wire in `internal/bootstrap/wire.go` based on config `DISPATCHER_PROVIDER=<name>`.
4. Add contract test at `test/contract/dispatcher_<name>_test.go`.
5. ADR documenting the provider's specifics (rate limiter ceiling, prompt format, envelope parsing).

## Local development

- Postgres on `localhost:5432`, DB `sophia_orchestator`, user/pass `sophia/sophia`.
- Orchestrator on `localhost:8080`.
- Stub services (governance/memory/runtime) under `ops/local/stubs/` (V1.1).

## Testing layers

| Layer | Build tag | Coverage gate |
|---|---|---|
| Unit | (none) | domain 100%, app 85%, infra 75% |
| Integration | `integration` | (gated by feature areas) |
| Contract | (none, in `test/contract/`) | green required |
| E2E | `e2e` | green required main |
| E2E SSE | `e2e_sse` | green required main |
| Chaos | `chaos` | one passing per phase boundary |
| Load | k6 | doesn't regress baseline |

Run targets via `make test-*`.

## Hard project rules

1. NEVER add `Co-Authored-By` or AI attribution to commits. Conventional commits only.
2. NEVER bypass `SpawnGovernor` when launching dispatcher subprocesses.
3. NEVER call `time.Now()` / `ulid.Make()` in `internal/domain` or `internal/application` (lint enforces).
4. NEVER persist after returning — Envelope persistence happens BEFORE caller-visible state change.
5. NEVER skip `make lint` before commit if you touched Go files.
6. NEVER reference `engram` as a backend — sophia uses `sophia-memory-engine` (HTTP). See ADR-0003.

## Commit prefixes

`feat`, `fix`, `chore`, `docs`, `test`, `refactor`, `ci`, `perf`. Scope from `{domain, application, bootstrap, change, phase, apply, session, pg, http, governance, memory, runtime, dispatcher, discipline, audit, ci, docs, test}`.

## Where decisions live

- Architectural: `docs/adr/`. Required for new dispatcher providers, persistence layer changes, observability changes that affect SLOs.
- Design: `docs/superpowers/specs/`.
- Implementation: `docs/superpowers/plans/`.
- Open questions: end of design spec § "Open Questions / Future ADRs".
