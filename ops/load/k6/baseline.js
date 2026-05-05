// k6 load baseline for sophia-orchestator (spec § 10.7).
//
// Targets the apply-phase happy path through the full stack — hits
// /changes (create) + /phases/{type}/run (202) at a sustained rate. The
// p99 < 500ms threshold gates the SLO recording rule
// `sophia_orchestator_phase_api_latency_p99` (ops/slo/phase_api_latency.yaml).
//
// Run:
//   k6 run -e BASE_URL=http://localhost:18080 \
//          -e API_KEY=demo-key \
//          ops/load/k6/baseline.js
//
// CI gate (ops/load/run-baseline.sh): fails the build if any threshold
// is breached.

import http from "k6/http";
import { check, sleep } from "k6";
import { Counter, Trend } from "k6/metrics";

// Custom metrics so we can assert on per-endpoint trends.
const createDuration = new Trend("change_create_duration_ms", true);
const runDuration = new Trend("phase_run_duration_ms", true);
const errorCounter = new Counter("sophia_errors");

const BASE_URL = __ENV.BASE_URL || "http://localhost:18080";
const API_KEY = __ENV.API_KEY || "demo-key";
const HEADERS = {
  "Content-Type": "application/json",
  "X-Sophia-API-Key": API_KEY,
};

export const options = {
  // Smoke phase: 1 VU for 30s — sanity that the API responds at all.
  // Baseline phase: 5 VUs for 2m — ~50 requests/min sustained.
  // Burst phase: 10 VUs for 30s — verifies the spawn-governor backoff.
  scenarios: {
    smoke: {
      executor: "constant-vus",
      vus: 1,
      duration: "30s",
      tags: { phase: "smoke" },
      gracefulStop: "15s",
    },
    baseline: {
      executor: "constant-arrival-rate",
      rate: 50,
      timeUnit: "1m",
      duration: "2m",
      preAllocatedVUs: 10,
      maxVUs: 20,
      startTime: "30s",
      tags: { phase: "baseline" },
      gracefulStop: "15s",
    },
    burst: {
      executor: "constant-vus",
      vus: 10,
      duration: "30s",
      startTime: "2m30s",
      tags: { phase: "burst" },
      gracefulStop: "15s",
    },
  },

  thresholds: {
    // Spec § 9.4: POST /run returns 202 in <500ms p99.
    "phase_run_duration_ms{phase:baseline}": ["p(99)<500"],
    // Sane upper bound on Change.Create (no async work).
    "change_create_duration_ms": ["p(99)<300"],
    // Error rate below 1% across the entire run.
    "http_req_failed": ["rate<0.01"],
    // Custom error counter: trip if more than 5 application-level errors.
    "sophia_errors": ["count<5"],
  },
};

export function setup() {
  // Sanity-check the orchestator is up before generating load.
  const r = http.get(`${BASE_URL}/api/v1/health`);
  if (r.status !== 200) {
    throw new Error(`/api/v1/health returned ${r.status}; cannot run baseline`);
  }
}

export default function () {
  // 1. Create a change with a unique name.
  const name = `k6-${__VU}-${__ITER}-${Date.now()}`;
  const createPayload = JSON.stringify({
    name,
    project: "k6-load",
    artifact_store_mode: "memory-engine",
    base_ref: "main",
  });
  const createRes = http.post(`${BASE_URL}/api/v1/changes`, createPayload, {
    headers: HEADERS,
    tags: { endpoint: "changes_create" },
  });
  createDuration.add(createRes.timings.duration);
  if (
    !check(createRes, {
      "create 201": (r) => r.status === 201,
    })
  ) {
    errorCounter.add(1);
    return;
  }

  const changeID = JSON.parse(createRes.body).change_id;

  // 2. Run the explore phase (single-agent flow exercises governance →
  //    discipline → dispatcher → memory → audit pipeline).
  const runRes = http.post(
    `${BASE_URL}/api/v1/changes/${changeID}/phases/explore/run`,
    JSON.stringify({ task_description: "k6 baseline", retry_budget: 3 }),
    { headers: HEADERS, tags: { endpoint: "phases_run" } },
  );
  runDuration.add(runRes.timings.duration);
  if (
    !check(runRes, {
      "run 202": (r) => r.status === 202,
    })
  ) {
    errorCounter.add(1);
    return;
  }

  // 3. Brief breather before the next iteration (matches realistic client
  //    pacing; arrival-rate scenario controls the actual rate anyway).
  sleep(0.1);
}

export function handleSummary(data) {
  // Print a compact stdout summary for CI artifacts.
  return {
    stdout: textSummary(data),
    "ops/load/k6/baseline-summary.json": JSON.stringify(data, null, 2),
  };
}

// Minimal textSummary so we don't depend on k6/x/textSummary.
function textSummary(data) {
  const m = data.metrics;
  const lines = [
    "=== sophia-orchestator k6 baseline ===",
    `change_create_duration_ms p99=${fmt(m.change_create_duration_ms?.values?.["p(99)"])}`,
    `phase_run_duration_ms     p99=${fmt(m.phase_run_duration_ms?.values?.["p(99)"])}`,
    `http_req_failed           rate=${fmt(m.http_req_failed?.values?.rate)}`,
    `sophia_errors             count=${m.sophia_errors?.values?.count ?? 0}`,
  ];
  return lines.join("\n") + "\n";
}

function fmt(n) {
  return typeof n === "number" ? n.toFixed(2) : "n/a";
}
