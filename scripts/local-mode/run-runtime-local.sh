#!/usr/bin/env bash
# run-runtime-local.sh
#
# Runs sophia-runtime-adapters on the macOS host (outside Docker) so it can
# spawn the user's local opencode / claude binaries with inherited env vars.
#
# This script LIVES in sophia-orchestator/scripts/local-mode/ but the
# go run command happens inside the sophia-runtime-adapters repo.
#
# Usage:
#   cd /Users/russell/Documents/2026/sophia-orchestator
#   ./scripts/local-mode/run-runtime-local.sh
#
# All env vars are overridable. Example override:
#   RUNTIME_HTTP_ADDR=:8084 ./scripts/local-mode/run-runtime-local.sh
#
# Topology:
#   pg-runtime-adapters → localhost:5437
#   runtime-adapters    → localhost:8083  (THIS process)

set -euo pipefail

COMPOSE_FILE="ops/local/compose.full-stack.yaml"
# Location of the runtime-adapters repo — override with RUNTIME_ADAPTERS_REPO
RUNTIME_ADAPTERS_REPO="${RUNTIME_ADAPTERS_REPO:-/Users/russell/Documents/2026/sophia-runtime-adapters}"

# ---------------------------------------------------------------------------
# ENV — all vars are overridable from the caller's shell
# Env var names verified against:
#   sophia-runtime-adapters/internal/infrastructure/config/load.go
# ---------------------------------------------------------------------------
RUNTIME_POSTGRES_DSN="${RUNTIME_POSTGRES_DSN:-postgres://runtime:runtime@localhost:5437/runtime_adapters?sslmode=disable}"
# RUNTIME_HTTP_ADDR controls the listen address (verified: NOT HTTP_PORT).
RUNTIME_HTTP_ADDR="${RUNTIME_HTTP_ADDR:-:8083}"
# Colon-separated list of directories allowed to contain executables.
RUNTIME_ALLOWED_COMMANDS_PATH="${RUNTIME_ALLOWED_COMMANDS_PATH:-/Users/russell/.opencode/bin:/Users/russell/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin}"
# Colon-separated list of allowed working directories for spawned commands.
RUNTIME_ALLOWED_WORKING_DIRS="${RUNTIME_ALLOWED_WORKING_DIRS:-/Users/russell/Documents/2026:/tmp/sophia/worktrees}"
# Colon-separated list of allowed filesystem roots (required by config.Validate()).
RUNTIME_ALLOWED_FILESYSTEM_ROOTS="${RUNTIME_ALLOWED_FILESYSTEM_ROOTS:-/Users/russell:/tmp/sophia}"
RUNTIME_ENV="${RUNTIME_ENV:-development}"
RUNTIME_PROVENANCE_SOURCE="${RUNTIME_PROVENANCE_SOURCE:-http}"
RUNTIME_CHAOS_ENABLED="${RUNTIME_CHAOS_ENABLED:-false}"
OTEL_ENABLED="${OTEL_ENABLED:-false}"

# ---------------------------------------------------------------------------
# HELPERS
# ---------------------------------------------------------------------------
info()  { printf '[INFO]  %s\n' "$*"; }
warn()  { printf '[WARN]  %s\n' "$*" >&2; }
die()   { printf '[ERROR] %s\n' "$*" >&2; exit 1; }

# ---------------------------------------------------------------------------
# PRE-REQUISITE 0: runtime-adapters repo must exist
# ---------------------------------------------------------------------------
if [ ! -d "${RUNTIME_ADAPTERS_REPO}" ]; then
  die "sophia-runtime-adapters repo not found at '${RUNTIME_ADAPTERS_REPO}'.
  Clone it or set RUNTIME_ADAPTERS_REPO to the correct path."
fi

# ---------------------------------------------------------------------------
# PRE-REQUISITE 1: pg-runtime-adapters reachable on localhost:5437
# ---------------------------------------------------------------------------
info "Checking pg-runtime-adapters (localhost:5437) ..."
if ! pg_isready -h localhost -p 5437 -U runtime -q 2>/dev/null; then
  die "pg-runtime-adapters is not ready on localhost:5437.
  Start the compose stack first:
    docker compose -f ${COMPOSE_FILE} up -d pg-runtime-adapters"
fi
info "pg-runtime-adapters OK"

# ---------------------------------------------------------------------------
# PRE-REQUISITE 2: port must be free (no runtime-adapters container on :8083)
# ---------------------------------------------------------------------------
PORT="${RUNTIME_HTTP_ADDR#:}"
info "Checking that port ${PORT} is free ..."
if nc -z 127.0.0.1 "${PORT}" 2>/dev/null; then
  die "Port ${PORT} is already in use.
  If the runtime-adapters container is running, stop it first:
    docker compose -f ${COMPOSE_FILE} stop runtime-adapters
  If another process is using the port, check with: lsof -i :${PORT}"
fi
info "Port ${PORT} is free"

# ---------------------------------------------------------------------------
# PRE-REQUISITE 3: check opencode in expected path (soft warning, not fatal)
# ---------------------------------------------------------------------------
if command -v opencode >/dev/null 2>&1; then
  info "opencode found at: $(command -v opencode)"
else
  warn "opencode not found in PATH."
  warn "M-E0 E2E (spawn via runtime-adapters) will fail until it is available."
  warn "Expected location: /Users/russell/.opencode/bin/opencode"
  warn "RUNTIME_ALLOWED_COMMANDS_PATH currently: ${RUNTIME_ALLOWED_COMMANDS_PATH}"
fi

# ---------------------------------------------------------------------------
# EXPORT ENV FOR THE CHILD PROCESS
# ---------------------------------------------------------------------------
export RUNTIME_POSTGRES_DSN
export RUNTIME_HTTP_ADDR
export RUNTIME_ALLOWED_COMMANDS_PATH
export RUNTIME_ALLOWED_WORKING_DIRS
export RUNTIME_ALLOWED_FILESYSTEM_ROOTS
export RUNTIME_ENV
export RUNTIME_PROVENANCE_SOURCE
export RUNTIME_CHAOS_ENABLED
export OTEL_ENABLED

# ---------------------------------------------------------------------------
# SUMMARY + LAUNCH
# ---------------------------------------------------------------------------
info "Starting runtime-adapters local on ${RUNTIME_HTTP_ADDR} talking to db:5437"
info "AllowedCommandsPath: ${RUNTIME_ALLOWED_COMMANDS_PATH}"
info "AllowedWorkingDirs:  ${RUNTIME_ALLOWED_WORKING_DIRS}"

cd "${RUNTIME_ADAPTERS_REPO}"
exec go run ./cmd/runtime-adapters
