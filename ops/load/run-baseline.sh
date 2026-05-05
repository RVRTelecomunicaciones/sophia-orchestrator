#!/usr/bin/env bash
# CI helper: runs k6 baseline against the local docker-compose stack.
# Exits non-zero if any threshold is breached.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

if ! command -v k6 >/dev/null 2>&1; then
  echo "k6 not installed. brew install k6 (mac) or see https://k6.io/docs/get-started/installation/" >&2
  exit 2
fi

BASE_URL="${BASE_URL:-http://localhost:18080}"
API_KEY="${API_KEY:-demo-key}"

echo "Running baseline against $BASE_URL ..."
k6 run \
  -e BASE_URL="$BASE_URL" \
  -e API_KEY="$API_KEY" \
  ops/load/k6/baseline.js
