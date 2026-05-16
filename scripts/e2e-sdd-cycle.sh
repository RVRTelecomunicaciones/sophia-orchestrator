#!/usr/bin/env bash
#
# e2e-sdd-cycle.sh — drive a real SDD cycle against the local stack
# with a real LLM dispatch end-to-end. This is the script referenced
# from docs/operations/cycle-validation.md.
#
# What it does (in order):
#   1. Pre-flight checks: docker, opencode auth, sibling repos, env vars.
#   2. Bring up the full stack (compose.full-stack.yaml + e2e-llm overlay).
#   3. Wait for orchestator /api/v1/health to return 200.
#   4. POST /api/v1/changes — create a tiny demo change.
#   5. POST .../phases/explore/run — kick off the cheapest SDD phase.
#   6. Poll GET /api/v1/phases/{id} until status terminates (DONE/FAILED).
#   7. Print the final phase JSON (envelope, dispatcher provider, etc.)
#      and the orch logs filtered by the phase id.
#   8. Tear down by default; pass --keep to leave the stack up for
#      manual inspection.
#
# Exit codes:
#   0 — cycle completed with status=DONE / DONE_WITH_CONCERNS / DONE_WITH_REJECTIONS
#   1 — pre-flight check failed (operator must fix env before retry)
#   2 — stack failed to come up healthy within the timeout
#   3 — orch HTTP API returned an unexpected error during the cycle
#   4 — phase ended in FAILED or timed out polling
#
# Usage:
#   ./scripts/e2e-sdd-cycle.sh                  # run + tear down
#   ./scripts/e2e-sdd-cycle.sh --keep           # run + leave stack up
#   ./scripts/e2e-sdd-cycle.sh --no-build       # skip docker build (use existing images)
#   ./scripts/e2e-sdd-cycle.sh --change <name>  # custom change name
#   ./scripts/e2e-sdd-cycle.sh --phase <type>   # run a different phase (default: explore)
#
# This script is intentionally a single bash file with no dependencies
# beyond `docker`, `curl`, `jq`, and `bash 4+`. It is meant to be
# committable and runnable on any laptop with a docker daemon.

set -euo pipefail

# ---- defaults ---------------------------------------------------------------

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
readonly COMPOSE_DIR="${REPO_DIR}/ops/local"
readonly COMPOSE_FILES=(
  "-f" "${COMPOSE_DIR}/compose.full-stack.yaml"
  "-f" "${COMPOSE_DIR}/compose.e2e-llm.yaml"
)
readonly ORCH_BASE_URL="${ORCH_BASE_URL:-http://localhost:8080}"
readonly ORCH_API_KEY="${ORCH_API_KEY:-full-stack-key}"
readonly OPENCODE_AUTH_PATH="${HOME}/.local/share/opencode/auth.json"

CHANGE_NAME="e2e-validation-$(date +%s)"
CHANGE_PROJECT="e2e-local"
PHASE_TYPE="explore"
KEEP_STACK=false
DO_BUILD=true
HEALTHY_TIMEOUT_S=180
PHASE_POLL_TIMEOUT_S=600
PHASE_POLL_INTERVAL_S=5

# ---- args -------------------------------------------------------------------

while [[ $# -gt 0 ]]; do
  case "$1" in
    --keep)         KEEP_STACK=true; shift ;;
    --no-build)     DO_BUILD=false; shift ;;
    --change)       CHANGE_NAME="$2"; shift 2 ;;
    --phase)        PHASE_TYPE="$2"; shift 2 ;;
    -h|--help)
      sed -n '2,38p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) echo "unknown flag: $1" >&2; exit 1 ;;
  esac
done

# ---- pretty logging ---------------------------------------------------------

ts() { date -u +"%Y-%m-%dT%H:%M:%SZ"; }
log()  { printf '[%s] %s\n' "$(ts)" "$*"; }
warn() { printf '[%s] WARN: %s\n' "$(ts)" "$*" >&2; }
die()  { printf '[%s] ERROR: %s\n' "$(ts)" "$*" >&2; exit "${2:-1}"; }

# ---- pre-flight -------------------------------------------------------------

preflight() {
  log "pre-flight checks..."

  command -v docker >/dev/null    || die "docker not in PATH" 1
  command -v curl >/dev/null      || die "curl not in PATH" 1
  command -v jq >/dev/null        || die "jq not in PATH (brew install jq)" 1
  docker compose version >/dev/null 2>&1 || die "docker compose v2 required" 1

  [[ -f "${OPENCODE_AUTH_PATH}" ]] || die "opencode auth.json not found at ${OPENCODE_AUTH_PATH} — run 'opencode auth login github-copilot' (or your provider) first" 1

  # Sibling repos required by compose builds.
  for sibling in sophia-memory-engine agent-governance-core sophia-runtime-adapters; do
    [[ -d "${REPO_DIR}/../${sibling}" ]] || die "sibling repo not found: ../${sibling} — see ops/local/compose.full-stack.README.md pre-requisites" 1
  done

  log "pre-flight OK"
}

# ---- stack lifecycle --------------------------------------------------------

bring_up() {
  log "bringing up stack (build=${DO_BUILD})..."
  local up_args=()
  ${DO_BUILD} && up_args+=("--build")
  ( cd "${COMPOSE_DIR}" && docker compose "${COMPOSE_FILES[@]}" up -d "${up_args[@]}" ) \
    || die "docker compose up failed" 2
}

tear_down() {
  if ${KEEP_STACK}; then
    log "--keep set: leaving stack up. Tear down later with:"
    log "  cd ${COMPOSE_DIR} && docker compose ${COMPOSE_FILES[*]} down -v"
    return
  fi
  log "tearing down stack (volumes too)..."
  ( cd "${COMPOSE_DIR}" && docker compose "${COMPOSE_FILES[@]}" down -v ) || true
}
trap tear_down EXIT

wait_healthy() {
  log "waiting for orchestator /api/v1/health (timeout ${HEALTHY_TIMEOUT_S}s)..."
  local elapsed=0
  while (( elapsed < HEALTHY_TIMEOUT_S )); do
    if curl -fsS "${ORCH_BASE_URL}/api/v1/health" >/dev/null 2>&1; then
      log "orchestator healthy after ${elapsed}s"
      return
    fi
    sleep 5
    elapsed=$((elapsed + 5))
  done
  ( cd "${COMPOSE_DIR}" && docker compose "${COMPOSE_FILES[@]}" logs --tail=50 orchestator ) >&2 || true
  die "orchestator did not become healthy within ${HEALTHY_TIMEOUT_S}s" 2
}

# ---- API helpers ------------------------------------------------------------

api() {
  local method="$1" path="$2" body="${3:-}"
  local args=(-fsS -X "${method}" -H "X-API-Key: ${ORCH_API_KEY}")
  if [[ -n "${body}" ]]; then
    args+=(-H "Content-Type: application/json" --data "${body}")
  fi
  curl "${args[@]}" "${ORCH_BASE_URL}${path}"
}

# ---- cycle ------------------------------------------------------------------

run_cycle() {
  log "creating change name=${CHANGE_NAME} project=${CHANGE_PROJECT}..."
  local create_resp
  # artifact_store_mode is omitted on purpose so the orch picks its
  # default (memory-engine, the value seeded by the stack). Valid enum
  # values per internal/domain/change/status.go are
  # memory-engine | openspec | hybrid | none — NOT "engram" (engram is
  # the operator-side memory tool, unrelated to the orch's artifact
  # store enum).
  create_resp="$(api POST /api/v1/changes "$(jq -nc \
    --arg n "${CHANGE_NAME}" --arg p "${CHANGE_PROJECT}" \
    '{name:$n, project:$p}')")" \
    || die "POST /api/v1/changes failed" 3

  local change_id
  change_id="$(echo "${create_resp}" | jq -r '.change_id')"
  [[ -n "${change_id}" && "${change_id}" != "null" ]] || die "no change_id in response: ${create_resp}" 3
  log "change_id=${change_id}"

  log "kicking off phase=${PHASE_TYPE}..."
  local run_resp
  run_resp="$(api POST "/api/v1/changes/${change_id}/phases/${PHASE_TYPE}/run" '{}')" \
    || die "phase run failed" 3
  local phase_id
  phase_id="$(echo "${run_resp}" | jq -r '.phase_id')"
  [[ -n "${phase_id}" && "${phase_id}" != "null" ]] || die "no phase_id in run response: ${run_resp}" 3
  log "phase_id=${phase_id}"

  log "polling phase status (timeout ${PHASE_POLL_TIMEOUT_S}s, every ${PHASE_POLL_INTERVAL_S}s)..."
  local elapsed=0 status="" phase_resp=""
  while (( elapsed < PHASE_POLL_TIMEOUT_S )); do
    phase_resp="$(api GET "/api/v1/phases/${phase_id}")" || die "GET phase failed" 3
    status="$(echo "${phase_resp}" | jq -r '.status')"
    log "  t=${elapsed}s status=${status}"
    # The orch returns lowercase status strings (matches the
    # phase.PhaseStatus enum on the wire). `blocked` and `needs_context`
    # ARE terminal — calling /events on them returns
    # `phase_terminal_no_events`. Earlier versions of this case missed
    # those two and polled until timeout against terminal phases.
    case "${status}" in
      done|done_with_concerns|done_with_rejections|blocked|needs_context|failed|aborted|timed_out) break ;;
    esac
    sleep "${PHASE_POLL_INTERVAL_S}"
    elapsed=$((elapsed + PHASE_POLL_INTERVAL_S))
  done

  if (( elapsed >= PHASE_POLL_TIMEOUT_S )); then
    die "phase ${phase_id} did not terminate within ${PHASE_POLL_TIMEOUT_S}s (last status=${status})" 4
  fi

  log "----- final phase JSON -----"
  echo "${phase_resp}" | jq .
  log "----- last 80 lines of orchestator logs -----"
  ( cd "${COMPOSE_DIR}" && docker compose "${COMPOSE_FILES[@]}" logs --tail=80 orchestator ) | grep -E "phase_id=${phase_id}|change_id=${change_id}|level=ERROR|level=WARN" || true

  case "${status}" in
    done|done_with_concerns|done_with_rejections)
      log "cycle PASSED with status=${status}"
      return 0
      ;;
    *)
      die "cycle FAILED with status=${status}" 4
      ;;
  esac
}

# ---- main -------------------------------------------------------------------

main() {
  preflight
  bring_up
  wait_healthy
  run_cycle
}

main "$@"
