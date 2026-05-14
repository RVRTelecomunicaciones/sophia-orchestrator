# Runbook: M-E0 Local Execution Mode

Run `orchestator` and `runtime-adapters` on the macOS host while keeping the
four Postgres instances and two backend services (`memory-engine`,
`governance-core`) inside Docker Compose.

This mode exists because the `runtime-adapters` distroless container ships no
`opencode` or `claude` binary — it is sealed at build time. Running
`runtime-adapters` locally lets it spawn whatever LLM binaries live on the
host at dispatch time, inheriting the user's full PATH and environment variables
(including `ANTHROPIC_API_KEY`, `OPENCODE_*`, etc.). See ADR-0005, M-E0
milestone scope.

---

## 1. Prerequisites

| Item | Required version / location |
|------|----------------------------|
| Docker Desktop (or Engine) | Compose v2.20+ |
| Go toolchain | 1.26.2 (pinned in `go.mod`; auto-downloaded via `GOTOOLCHAIN=auto`) |
| `pg_isready` | ships with PostgreSQL client tools (`brew install libpq`) |
| `opencode` | `/Users/russell/.opencode/bin/opencode` |
| `claude` (optional) | `/Users/russell/.local/bin/claude` |
| `ANTHROPIC_API_KEY` | exported in shell, or opencode already logged in |

All five Sophia repos must be checked out as siblings:

```
/Users/russell/Documents/2026/
  sophia-orchestator/        ← this repo
  sophia-memory-engine/
  agent-governance-core/
  sophia-runtime-adapters/
```

---

## 2. Bring up the partial compose stack

Start only the services that stay in Docker. Do NOT start `orchestator` or
`runtime-adapters` containers — those run locally.

```bash
cd /Users/russell/Documents/2026/sophia-orchestator

docker compose -f ops/local/compose.full-stack.yaml up -d \
  pg-orchestator \
  pg-memory-engine \
  pg-governance-core \
  pg-runtime-adapters \
  migrate-memory-engine \
  migrate-governance-core \
  memory-engine \
  governance-core
```

Wait approximately 30 seconds for migrations to complete and all services to be
healthy. Verify:

```bash
docker compose -f ops/local/compose.full-stack.yaml ps
```

Expected: 6 containers running (4 pg-* + memory-engine + governance-core),
2 migration sidecars exited 0.

---

## 3. Run orchestator locally

Open **Terminal A**:

```bash
cd /Users/russell/Documents/2026/sophia-orchestator
./scripts/local-mode/run-orchestrator-local.sh
```

The script checks:
- `pg-orchestator` reachable on `localhost:5434`
- Port `8080` not already occupied

Then starts `go run ./cmd/sophia-orchestator`. Wait for the log line:

```
level=INFO msg="HTTP server listening" addr=:8080
```

Override any default:

```bash
SOPHIA_LOG_LEVEL=debug ./scripts/local-mode/run-orchestrator-local.sh
```

---

## 4. Run runtime-adapters locally

Open **Terminal B**:

```bash
cd /Users/russell/Documents/2026/sophia-orchestator
./scripts/local-mode/run-runtime-local.sh
```

The script checks:
- `pg-runtime-adapters` reachable on `localhost:5437`
- Port `8083` not already occupied
- `opencode` in PATH (soft warning if missing — not fatal)

Then `cd`s into `sophia-runtime-adapters` and runs `go run ./cmd/runtime-adapters`.
Wait for the log line:

```
level=INFO msg="HTTP server listening" addr=:8083
```

Override the repo path:

```bash
RUNTIME_ADAPTERS_REPO=/path/to/other/clone ./scripts/local-mode/run-runtime-local.sh
```

---

## 5. Smoke verification

Open **Terminal C** and curl all four service health endpoints:

```bash
# orchestator (runs locally on 8080)
curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/api/v1/ready
# Expected: 200

# memory-engine (compose container, port 8081)
curl -s -o /dev/null -w "%{http_code}" http://localhost:8081/health
# Expected: 200

# governance-core (compose container, port 8082; no /health — probe the API)
curl -s -o /dev/null -w "%{http_code}" http://localhost:8082/api/v1/tasks
# Expected: 404 or 405 — both confirm the service is alive

# runtime-adapters (runs locally on 8083)
curl -s -o /dev/null -w "%{http_code}" http://localhost:8083/healthz
# Expected: 200
```

Full topology at this point:

```
COMPOSE (4 services in container):
  pg-orchestator     → localhost:5434
  pg-memory-engine   → localhost:5435
  pg-governance-core → localhost:5436
  pg-runtime-adapters→ localhost:5437
  memory-engine      → localhost:8081
  governance-core    → localhost:8082

LOCAL (2 services on macOS host):
  orchestator        → localhost:8080
  runtime-adapters   → localhost:8083
```

---

## 6. E2E test (M-E0 #4 — placeholder)

TODO: create a Change via the orchestator API, trigger the apply phase, and
observe an `opencode` subprocess appear in Terminal B's output.

Reference: M-E0 #4 milestone ("End-to-end dispatch verification").

---

## 7. Troubleshooting

### `opencode not found in AllowedCommandsPath`

runtime-adapters validates that the spawned binary's resolved path starts with
one of the entries in `RUNTIME_ALLOWED_COMMANDS_PATH`.

Fix: add the directory containing `opencode` to the env var before starting:

```bash
RUNTIME_ALLOWED_COMMANDS_PATH="/Users/russell/.opencode/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin" \
  ./scripts/local-mode/run-runtime-local.sh
```

Verify the binary is actually there:

```bash
ls -la /Users/russell/.opencode/bin/opencode
```

### `failed to connect to database` / `connection refused`

Check compose health:

```bash
docker compose -f ops/local/compose.full-stack.yaml ps
pg_isready -h localhost -p 5434 -U sophia    # orchestator db
pg_isready -h localhost -p 5437 -U runtime   # runtime-adapters db
```

If a pg-* container is unhealthy, restart it:

```bash
docker compose -f ops/local/compose.full-stack.yaml restart pg-orchestator
```

### `401 Unauthorized` against memory-engine

The dev bootstrap API key is seeded by
`sophia-memory-engine/migrations/postgres/003_create_api_keys.up.sql`.

Verify the key set in the script matches:

```
memdev_a1b2c3d4e5f60718293a4b5c6d7e8f90123456789abcdef0123456789abcdef
```

If the memory-engine database was wiped and re-migrated, the key is re-seeded
automatically on the next `migrate-memory-engine` sidecar run. To force
re-migration:

```bash
docker compose -f ops/local/compose.full-stack.yaml rm -f migrate-memory-engine
docker compose -f ops/local/compose.full-stack.yaml up -d migrate-memory-engine
```

### Port already in use

```bash
lsof -i :8080   # should show nothing if orchestator container is stopped
lsof -i :8083   # should show nothing if runtime-adapters container is stopped
```

Stop stray containers:

```bash
docker compose -f ops/local/compose.full-stack.yaml stop orchestator runtime-adapters
```

### `RUNTIME_ENV "development" invalid` or similar config validation error

`RUNTIME_ENV` must be one of `development`, `staging`, `production`. The
scripts default to `development`. If you override it with `dev` (invalid),
the process exits immediately with a config error.

### `go: toolchain go1.26.2 required` / toolchain download

The first `go run` may download the pinned toolchain (~90 seconds on a cold
cache). This is expected. After the first run the toolchain is cached and
subsequent starts are fast.

---

## 8. Tear down

In Terminals A and B: `Ctrl-C` (the scripts use `exec`, so SIGINT goes
directly to the Go process; shutdown is graceful via the 30-second window).

Then stop and remove compose volumes:

```bash
docker compose -f ops/local/compose.full-stack.yaml down -v
```

---

## 9. Why this topology

The `sophia-runtime-adapters` production image uses Google's
`distroless/static` base. It contains no shell, no `opencode`, no `claude`,
and no user home directory. Installing arbitrary LLM binaries into a distroless
image at deploy time is impractical and breaks the sealed-image contract of
ADR-0005.

Running `runtime-adapters` on the macOS host solves this cleanly: the process
inherits the developer's full environment — PATH, `ANTHROPIC_API_KEY`,
`OPENCODE_*` config — so it can locate and spawn `opencode` or `claude`
exactly as the user would from a terminal. The four Postgres instances and the
two stateless backend services (`memory-engine`, `governance-core`) stay in
Docker because they have no host-only dependency and benefit from the
reproducibility of containers.

Reference: ADR-0005 "M-E0 local-first hardening" milestone scope.
