#!/usr/bin/env bash
# run-orchestrator-local.sh
#
# Runs sophia-orchestator on the macOS host (outside Docker) while the
# companion services (pg-orchestator, memory-engine, governance-core,
# pg-runtime-adapters) remain in the compose stack.
#
# Usage:
#   cd /Users/russell/Documents/2026/sophia-orchestator
#   ./scripts/local-mode/run-orchestrator-local.sh
#
# All env vars are overridable. Example override:
#   SOPHIA_LOG_LEVEL=debug ./scripts/local-mode/run-orchestrator-local.sh
#
# Topology:
#   pg-orchestator   → localhost:5434
#   memory-engine    → localhost:8081
#   governance-core  → localhost:8082
#   runtime-adapters → localhost:8083  (also runs locally via run-runtime-local.sh)
#   orchestator      → localhost:8080  (THIS process)

set -euo pipefail

COMPOSE_FILE="ops/local/compose.full-stack.yaml"
REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"

# ---------------------------------------------------------------------------
# ENV — all vars are overridable from the caller's shell
# ---------------------------------------------------------------------------
SOPHIA_DB_URL="${SOPHIA_DB_URL:-postgres://sophia:sophia@localhost:5434/sophia_orchestator?sslmode=disable}"
SOPHIA_DB_MIGRATE_ON_BOOT="${SOPHIA_DB_MIGRATE_ON_BOOT:-true}"
SOPHIA_HTTP_ADDR="${SOPHIA_HTTP_ADDR:-:8080}"
SOPHIA_HTTP_API_KEY="${SOPHIA_HTTP_API_KEY:-full-stack-key}"
SOPHIA_HTTP_API_KEY_PROJECT="${SOPHIA_HTTP_API_KEY_PROJECT:-local}"
SOPHIA_MEMORY_URL="${SOPHIA_MEMORY_URL:-http://localhost:8081}"
# Dev bootstrap key seeded by sophia-memory-engine/migrations/postgres/003_create_api_keys.up.sql
# NEVER use outside local dev.
SOPHIA_MEMORY_API_KEY="${SOPHIA_MEMORY_API_KEY:-memdev_a1b2c3d4e5f60718293a4b5c6d7e8f90123456789abcdef0123456789abcdef}"
SOPHIA_GOVERNANCE_URL="${SOPHIA_GOVERNANCE_URL:-http://localhost:8082}"
SOPHIA_RUNTIME_URL="${SOPHIA_RUNTIME_URL:-http://localhost:8083}"
SOPHIA_ENV="${SOPHIA_ENV:-dev}"
SOPHIA_LOG_LEVEL="${SOPHIA_LOG_LEVEL:-info}"
SOPHIA_METRICS_ENABLED="${SOPHIA_METRICS_ENABLED:-true}"
SOPHIA_TRACES_ENABLED="${SOPHIA_TRACES_ENABLED:-false}"

# ---------------------------------------------------------------------------
# HELPERS
# ---------------------------------------------------------------------------
info()  { printf '[INFO]  %s\n' "$*"; }
warn()  { printf '[WARN]  %s\n' "$*" >&2; }
die()   { printf '[ERROR] %s\n' "$*" >&2; exit 1; }

# ---------------------------------------------------------------------------
# PRE-REQUISITE 1: pg-orchestator reachable on localhost:5434
# ---------------------------------------------------------------------------
info "Checking pg-orchestator (localhost:5434) ..."
if ! pg_isready -h localhost -p 5434 -U sophia -q 2>/dev/null; then
  die "pg-orchestator is not ready on localhost:5434.
  Start the compose stack first:
    docker compose -f ${COMPOSE_FILE} up -d \\
      pg-orchestator pg-memory-engine pg-governance-core pg-runtime-adapters \\
      migrate-memory-engine migrate-governance-core \\
      memory-engine governance-core"
fi
info "pg-orchestator OK"

# ---------------------------------------------------------------------------
# PRE-REQUISITE 2: port 8080 must be free (no orchestator container running)
# ---------------------------------------------------------------------------
PORT="${SOPHIA_HTTP_ADDR#:}"
info "Checking that port ${PORT} is free ..."
if nc -z 127.0.0.1 "${PORT}" 2>/dev/null; then
  die "Port ${PORT} is already in use.
  If the orchestator container is running, stop it first:
    docker compose -f ${COMPOSE_FILE} stop orchestator
  If another process is using the port, check with: lsof -i :${PORT}"
fi
info "Port ${PORT} is free"

# ---------------------------------------------------------------------------
# EXPORT ENV FOR THE CHILD PROCESS
# ---------------------------------------------------------------------------
export SOPHIA_DB_URL
export SOPHIA_DB_MIGRATE_ON_BOOT
export SOPHIA_HTTP_ADDR
export SOPHIA_HTTP_API_KEY
export SOPHIA_HTTP_API_KEY_PROJECT
export SOPHIA_MEMORY_URL
export SOPHIA_MEMORY_API_KEY
export SOPHIA_GOVERNANCE_URL
export SOPHIA_RUNTIME_URL
export SOPHIA_ENV
export SOPHIA_LOG_LEVEL
export SOPHIA_METRICS_ENABLED
export SOPHIA_TRACES_ENABLED

# ---------------------------------------------------------------------------
# SUMMARY + LAUNCH
# ---------------------------------------------------------------------------
info "Starting orchestator local on ${SOPHIA_HTTP_ADDR} talking to memory:8081 governance:8082 runtime:8083 db:5434"

cd "${REPO_ROOT}"
exec go run ./cmd/sophia-orchestator
