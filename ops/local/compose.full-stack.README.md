# Full-Stack Local Compose — Sophia Ecosystem

## Purpose

`compose.full-stack.yaml` brings up the entire Sophia ecosystem locally using
**real services** — NOT stubs. This is the local-first hardening exit per
ADR-0005 Sprint 0. It is the reference stack for end-to-end smoke testing
before promoting builds.

Contrast with `compose.yaml` (stubs file), which uses stub binaries for
governance, memory, and runtime. Both files coexist; neither touches the other.

## Architecture

```
Host                        Docker bridge: sophia-net
─────                       ──────────────────────────────────────────────
:8080  ←→  orchestator      (depends on memory-engine + governance + runtime)
:8081  ←→  memory-engine    (depends on migrate-memory-engine sidecar)
:8082  ←→  governance-core  (depends on migrate-governance-core sidecar)
:8083  ←→  runtime-adapters (self-migrates via pg.Migrate() on boot)

:5434  ←→  pg-orchestator
:5435  ←→  pg-memory-engine
:5436  ←→  pg-governance-core
:5437  ←→  pg-runtime-adapters
```

Each service has its own isolated Postgres instance (separate ports, separate
named volumes). The orchestator is the only consumer that calls all three
downstream services.

## Migration strategy

| Service | Approach |
|---------|----------|
| orchestator | `SOPHIA_DB_MIGRATE_ON_BOOT=true` — built-in golang-migrate runs before server starts |
| memory-engine | `migrate-memory-engine` init container (`ghcr.io/golang-migrate/migrate`) reads `migrations/postgres/*.up.sql` from the sibling repo via bind-mount |
| governance-core | `migrate-governance-core` init container (`ghcr.io/pressly/goose`) handles `-- +goose Up/Down` format |
| runtime-adapters | `pg.Migrate()` called in `bootstrap/wire.go` on boot — embedded golang-migrate |

## Pre-requisites

- Docker Desktop 4.x or Docker Engine 24+ with Compose v2 (`docker compose version`)
- All 5 Sophia repos checked out as siblings under one parent directory:

```
<parent>/
  sophia-orchestator/          ← this repo
  sophia-memory-engine/
  agent-governance-core/
  sophia-runtime-adapters/
```

The compose file references sibling repos via relative paths from `ops/local/`.
If repos are NOT siblings, the build contexts will fail.

## Bring up

```bash
# From the sophia-orchestator root:
docker compose -f ops/local/compose.full-stack.yaml up -d --build

# First build: ~3-5 minutes (4 Go service images, cold module cache).
# Subsequent builds: ~30-60s (layer cache hits on go mod download layers).
```

## Verify

```bash
# orchestator — health always 200 if process is running
curl -s http://localhost:8080/api/v1/health

# memory-engine — health route registered at /health
curl -s http://localhost:8081/health

# governance-core — NO /health route; probe any valid API path
# 405 Method Not Allowed means the service is alive and routing
curl -s -o /dev/null -w "%{http_code}" http://localhost:8082/api/v1/approvals/pending

# runtime-adapters — healthz route
curl -s http://localhost:8083/healthz

# Check container status
docker compose -f ops/local/compose.full-stack.yaml ps
```

Expected outputs:
- `orchestator`: `{"status":"ok"}` or similar
- `memory-engine`: `{"status":"ok"}` or `200 OK`
- `governance-core`: `200 OK` on `/api/v1/approvals/pending`
- `runtime-adapters`: `200 OK` on `/healthz`

## Tear down

```bash
# Stop containers and remove volumes (database data is deleted):
docker compose -f ops/local/compose.full-stack.yaml down -v

# Stop only (keep volumes for next run):
docker compose -f ops/local/compose.full-stack.yaml down
```

## Troubleshooting

### Postgres not healthy

```bash
docker compose -f ops/local/compose.full-stack.yaml logs pg-memory-engine
```

Port conflicts: ensure host ports 5434–5437 are free. The orchestator's
stub compose uses 5434 — if that stack is running, it will conflict.

### Build context not found

Error: `failed to read dockerfile: open .../sophia-memory-engine/Dockerfile: no such file`

The sibling repos must exist at the expected relative paths. Verify:

```bash
ls ../../../sophia-memory-engine/Dockerfile   # from ops/local/
ls ../../../agent-governance-core/Dockerfile
ls ../../../sophia-runtime-adapters/Dockerfile
```

### Migration sidecar exits non-zero

```bash
docker compose -f ops/local/compose.full-stack.yaml logs migrate-memory-engine
docker compose -f ops/local/compose.full-stack.yaml logs migrate-governance-core
```

Common cause: Postgres healthcheck retries exhausted before PG accepted
connections. Re-run with `up -d` — docker compose will restart failed sidecars.
If the DB already has the schema (e.g. volume not cleaned), "no change" is
logged and the sidecar exits 0.

### Service crashes immediately

```bash
docker compose -f ops/local/compose.full-stack.yaml logs orchestator
docker compose -f ops/local/compose.full-stack.yaml logs runtime-adapters
```

For `orchestator`: `SOPHIA_DB_URL` misconfigured or PG not ready.
For `runtime-adapters`: `RUNTIME_POSTGRES_DSN` required — missing causes panic at startup.
For `runtime-adapters`: `RUNTIME_ALLOWED_COMMANDS_PATH` / `RUNTIME_ALLOWED_WORKING_DIRS` /
`RUNTIME_ALLOWED_FILESYSTEM_ROOTS` must all be non-empty (validated by `config.Validate()`).

### governance-core has no /health route

This is intentional — the governance-core router (`internal/adapters/inbound/http/router.go`)
registers no health route. Its liveness is confirmed by any `/api/v1/*` response.
The compose stack uses `depends_on: service_completed_successfully` on the migration
sidecar, which guarantees PG is healthy before the service starts.
