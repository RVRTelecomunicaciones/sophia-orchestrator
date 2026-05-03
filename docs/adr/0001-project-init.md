# ADR 0001: Project init

- **Status:** accepted
- **Date:** 2026-05-03
- **Deciders:** rfactperu

## Context

V1 of `sophia-orchestator` follows the spec at
`docs/superpowers/specs/2026-05-03-sophia-orchestator-design.md`. We need to
lock in foundational decisions to avoid drift during implementation, and to
maintain consistency with the rest of the Sophia ecosystem
(`agent-governance-core`, `sophia-memory-engine`, `sophia-runtime-adapters`).

## Decision

1. **Language**: Go 1.26.2 (toolchain pinned in `go.mod` via the `toolchain go1.26.2`
   directive — matches sibling repos `agent-governance-core`, `sophia-memory-engine`,
   `sophia-runtime-adapters`). System Go 1.21+ auto-downloads the pinned toolchain
   transparently; effective compile version is always `go1.26.2` regardless of
   what `which go` resolves to. Verify with `go env GOVERSION`.
2. **DB**: PostgreSQL 16+ via `pgx/v5` (recommended target PG 17 LTS-style; PG 18
   feature-flagged). See [ADR-0004](0004-postgresql-version-target.md). No SQLite
   in V1 (parity with prod).
3. **HTTP router**: `chi/v5`.
4. **Observability**: OpenTelemetry traces + metrics, slog for structured logs.
5. **Testing**: `testify` + `testcontainers-go` for integration.
6. **Migrations**: `golang-migrate`.
7. **IDs**: `oklog/ulid/v2`. ULIDs stored as `CHAR(26)`.
8. **Architecture**: Hexagonal / clean. `internal/bootstrap/wire.go` is the
   only file that imports adapter implementations.
9. **Lint**: `golangci-lint` with `forbidigo` guards against `time.Now()`,
   `ulid.Make()`, and adapter imports from domain/application.
10. **Commits**: Conventional commits without AI attribution. Scope from a
    closed set documented in `AGENTS.md`.

## Consequences

### Positive

- Stack consistency with the rest of the Sophia ecosystem.
- `forbidigo` guards prevent the most common architectural drift bugs.
- Toolchain pinning means contributors with older Go installed still build
  the right version (Go 1.21+ auto-downloads).

### Negative

- Adding stack components requires a new ADR.
- `forbidigo` guards add friction for naive migrations from runtime-adapters
  helpers; we accept this in exchange for layer integrity.

### Neutral

- ULIDs over UUIDs: chosen for sortability and consistency with siblings;
  no significant difference for our scale.

## Alternatives considered

- **gRPC instead of HTTP**: rejected — runtime/governance/memory all expose
  HTTP, consistency wins. We can add gRPC later if needed.
- **SQLite for local dev**: rejected — divergence from prod; testcontainers
  gives parity at the cost of Docker.
- **Embedded Anthropic SDK**: rejected — orchestrator is dispatch-only; LLM
  calls live inside the OpenCode subprocess we dispatch.
- **Temporal as workflow engine**: rejected for V1 — overkill operationally.
  Hatchet evaluation is on the V2 roadmap (ADR-pending).
