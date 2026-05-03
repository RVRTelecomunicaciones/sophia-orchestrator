# sophia-orchestator

Deterministic coordinator of the Sophia SDD (Spec-Driven Development) workflow.

> **Status:** V1 in development. See [`docs/superpowers/specs/2026-05-03-sophia-orchestator-design.md`](docs/superpowers/specs/2026-05-03-sophia-orchestator-design.md) for the authoritative V1 design.

## What it does

Drives an SDD Change through 9 canonical phases (`init → explore → proposal → spec → design → tasks → apply → verify → archive`) with Iron-Law-enforced envelope contracts and parallel coordination of the apply phase via team-leads + implements in git worktrees.

## What it does NOT do

- Decide policy → [`agent-governance-core`](https://github.com/russellcxl/agent-governance-core).
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

Hexagonal / clean. Eight bounded contexts: Change, Phase, Apply, AgentDispatch, Worktree, Artifact, Discipline, Audit. See [`docs/architecture.md`](docs/architecture.md).

## Stack

Go 1.26 · PostgreSQL 15 · chi/v5 · pgx/v5 · OpenTelemetry · slog · testify · testcontainers-go · golangci-lint.

## Tests

```bash
make test-unit          # always-on; race + count=1
make test-integration   # testcontainers-go (Postgres)
make test-e2e           # full SDD cycle on fixture project
make test-e2e-sse       # SSE streaming
make test-chaos         # crash + manual resume
```

## Where to start reading

1. [`docs/superpowers/specs/2026-05-03-sophia-orchestator-design.md`](docs/superpowers/specs/2026-05-03-sophia-orchestator-design.md) — V1 design (authoritative).
2. [`docs/superpowers/plans/2026-05-03-sophia-orchestator-v1.md`](docs/superpowers/plans/2026-05-03-sophia-orchestator-v1.md) — V1 implementation plan (~90 tasks, 13 milestones).
3. [`CLAUDE.md`](CLAUDE.md) — for AI agents working in this repo.
4. [`AGENTS.md`](AGENTS.md) — quickstart + conventions.
5. [`docs/rules.md`](docs/rules.md) — R1..R12.
6. [`docs/domain-invariants.md`](docs/domain-invariants.md) — I1..I20.

## Companion services in the Sophia ecosystem

| Service | Role | Repo |
|---|---|---|
| `agent-governance-core` | Policy / approval / routing | [russellcxl/agent-governance-core](https://github.com/russellcxl/agent-governance-core) |
| `sophia-memory-engine` | Episodic / semantic memory | [sophia-engine/memory-engine](https://github.com/sophia-engine/memory-engine) |
| `sophia-runtime-adapters` | Side-effect execution + Phase 2 coordination primitives | [sophia-ecosystem/runtime-adapters](https://github.com/sophia-ecosystem/runtime-adapters) |
| `sophia-orchestator` | **(this repo)** SDD workflow coordinator | this |
| `sophia-cli` | User-facing CLI | (planned) |

## License

See [`LICENSE`](LICENSE).
