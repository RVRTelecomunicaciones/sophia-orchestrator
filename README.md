# sophia-orchestator

Deterministic coordinator of the Sophia SDD (Spec-Driven Development) workflow.

> **Status:** V1 in development. For how it actually works, read **[`ARCHITECTURE.md`](ARCHITECTURE.md)** — a code-grounded reference (the `docs/` and `openspec/` artifacts capture original intent but drift from the code; the code is the source of truth).

## What it does

Drives an SDD Change through 9 canonical phases (`init → explore → proposal → spec → design → tasks → apply → verify → archive`) with Iron-Law-enforced envelope contracts and parallel coordination of the apply phase via team-leads + implements in git worktrees.

## What it does NOT do

- Decide policy → [`agent-governance-core`](https://github.com/RVRTelecomunicaciones/agent-governance-core).
- Store knowledge → [`sophia-memory-engine`](https://github.com/sophia-engine/memory-engine).
- Execute side effects → [`sophia-runtime-adapters`](https://github.com/sophia-ecosystem/runtime-adapters).
- Call LLMs directly → that lives inside the OpenCode subprocess we dispatch.

## Quick start

```bash
docker compose -f ops/local/compose.yaml up -d   # Postgres + downstream stubs
make migrate-up
make build
./bin/sophia-orchestator --config=ops/local/config.yaml
```

## Architecture

Hexagonal / clean, **zero import cycles**. The canonical phase order is strictly
sequential — `init → explore → proposal → spec → design → tasks → apply → verify
→ archive` — where **spec and design both run** (design reads spec). Bounded
contexts: Change, Phase, Apply, AgentDispatch, Worktree, Artifact, Discipline, Audit.

See **[`ARCHITECTURE.md`](ARCHITECTURE.md)** — code-grounded, with `file:line`
citations and an AST knowledge graph (`graphify-out/`, query with `graphify query "..."`).

## Stack

Go 1.26 · PostgreSQL 16+ (recommended PG 17, see [ADR-0004](docs/adr/0004-postgresql-version-target.md)) · chi/v5 · pgx/v5 · OpenTelemetry · slog · testify · testcontainers-go · golangci-lint.

**Memory backend**: [`sophia-memory-engine`](https://github.com/sophia-engine/memory-engine) (HTTP). Not engram. See [ADR-0003](docs/adr/0003-memory-engine-integration.md).

## Tests

```bash
make test-unit          # always-on; race + count=1
make test-integration   # testcontainers-go (Postgres)
make test-e2e           # full SDD cycle on fixture project
make test-e2e-sse       # SSE streaming
make test-chaos         # crash + manual resume
```

## Where to start reading

1. **[`ARCHITECTURE.md`](ARCHITECTURE.md) — code-grounded architecture (source of truth).**
2. [`docs/superpowers/specs/2026-05-03-sophia-orchestator-design.md`](docs/superpowers/specs/2026-05-03-sophia-orchestator-design.md) — original V1 design intent (may drift from code).
3. [`CLAUDE.md`](CLAUDE.md) — for AI agents working in this repo.
4. [`AGENTS.md`](AGENTS.md) — quickstart + conventions.
5. [`docs/rules.md`](docs/rules.md) — R1..R12.
6. [`docs/domain-invariants.md`](docs/domain-invariants.md) — I1..I20.

## Companion services in the Sophia ecosystem

| Service | Role | Repo |
|---|---|---|
| `agent-governance-core` | Policy / approval / routing | [RVRTelecomunicaciones/agent-governance-core](https://github.com/RVRTelecomunicaciones/agent-governance-core) |
| `sophia-memory-engine` | Episodic / semantic memory | [sophia-engine/memory-engine](https://github.com/sophia-engine/memory-engine) |
| `sophia-runtime-adapters` | Side-effect execution + Phase 2 coordination primitives | [sophia-ecosystem/runtime-adapters](https://github.com/sophia-ecosystem/runtime-adapters) |
| `sophia-orchestator` | **(this repo)** SDD workflow coordinator | this |
| `sophia-cli` | User-facing CLI | (planned) |

## License

See [`LICENSE`](LICENSE).
