# Wire-Contract Matrix — Sophia Ecosystem (M-WA1)

**Status**: living doc · owner: orchestator team · last verified: 2026-05-15

This document is the **single source of truth** for every cross-repo HTTP
contract in the Sophia ecosystem. Each row is a wire dependency that the
orchestator initiates against another service. Drift between the producer
(client URL) and the consumer (router route) shows up here first — and is
caught in CI by the test suite under `test/wirecontract/`.

## Why this exists

The 2026-05-14 cross-repo audit surfaced **two wire bugs** that no unit test
caught because each side was tested in isolation:

1. `events_url` template (`/api/v1/changes/{cid}/phases/{pid}/events`) didn't
   match the actual SSE route (`/api/v1/phases/{pid}/events`) → silent 404
   for every consumer that followed the field.
2. Goroutine async work used `context.Background()` instead of propagating
   the request's `trace.Trace`, so persisted events had `trace_id=''` and
   broke log↔event correlation.

A wire-contract matrix turns these from "bugs found in prod / by manual
audit" into "tests fail in CI".

## How tests use this matrix

Each contract row maps to one or more tests under `test/wirecontract/`. A
test asserts the producer's path is reachable on a real router from the
consumer repo (not a mock). The test fails with a clear error message
pointing at this matrix when the contract drifts.

When you add a new outbound HTTP call from orch → another service, the
expected workflow is:

1. Add a row to the matrix below.
2. Add a wire-contract test under `test/wirecontract/`.
3. CI runs the test on every PR.

## Matrix

### orch → agent-governance-core (M-E0 facade)

| # | Method | Path                                               | Producer (orch)                                    | Consumer (governance)                              | Test                                  |
| - | ------ | -------------------------------------------------- | -------------------------------------------------- | -------------------------------------------------- | ------------------------------------- |
| 1 | POST   | `/governance/v1/decisions/phase`                   | `internal/adapters/outbound/governance/client.go`  | `internal/adapters/inbound/http/router.go:143`     | `wirecontract/governance_test.go`     |
| 2 | POST   | `/governance/v1/decisions/sensitive`               | `internal/adapters/outbound/governance/client.go`  | `internal/adapters/inbound/http/router.go:144`     | `wirecontract/governance_test.go`     |
| 3 | GET    | `/governance/v1/approvals/{change_id}/{phase_id}/status` | `internal/adapters/outbound/governance/client.go` | `internal/adapters/inbound/http/router.go:145`     | `wirecontract/governance_test.go`     |

### orch → sophia-runtime-adapters

| # | Method | Path                | Producer (orch)                                | Consumer (runtime)                                 | Test                              |
| - | ------ | ------------------- | ---------------------------------------------- | -------------------------------------------------- | --------------------------------- |
| 4 | POST   | `/api/v1/execute`   | `internal/adapters/outbound/runtime/client.go` | `internal/adapters/inbound/http/router.go:63`      | `wirecontract/runtime_test.go`    |

### orch → sophia-memory-engine

| #  | Method | Path                                    | Producer (orch)                               | Consumer (memory)                              | Test                          |
| -- | ------ | --------------------------------------- | --------------------------------------------- | ---------------------------------------------- | ----------------------------- |
| 5  | POST   | `/api/v1/memories`                      | `internal/adapters/outbound/memory/client.go` | `internal/adapters/inbound/http/server.go:49`  | `wirecontract/memory_test.go` |
| 6  | GET    | `/api/v1/memories/by-topic-key`         | `internal/adapters/outbound/memory/client.go` | `internal/adapters/inbound/http/server.go:52`  | `wirecontract/memory_test.go` |
| 7  | GET    | `/api/v1/memories/{id}`                 | `internal/adapters/outbound/memory/client.go` | `internal/adapters/inbound/http/server.go:53`  | `wirecontract/memory_test.go` |
| 8  | POST   | `/api/v1/memories/{id}/archive`         | `internal/adapters/outbound/memory/client.go` | `internal/adapters/inbound/http/server.go:54`  | `wirecontract/memory_test.go` |
| 9  | POST   | `/api/v1/search`                        | `internal/adapters/outbound/memory/client.go` | `internal/adapters/inbound/http/server.go:74`  | `wirecontract/memory_test.go` |
| 10 | POST   | `/api/v1/search/context`                | `internal/adapters/outbound/memory/client.go` | `internal/adapters/inbound/http/server.go:75`  | `wirecontract/memory_test.go` |
| 11 | POST   | `/api/v1/decisions`                     | `internal/adapters/outbound/memory/client.go` | `internal/adapters/inbound/http/server.go:57`  | `wirecontract/memory_test.go` |
| 12 | POST   | `/api/v1/relations`                     | `internal/adapters/outbound/memory/client.go` | `internal/adapters/inbound/http/server.go:69`  | `wirecontract/memory_test.go` |

### orch internal (template ↔ router)

| #  | Producer field                        | Consumer route                                 | Test                                                                        |
| -- | ------------------------------------- | ---------------------------------------------- | --------------------------------------------------------------------------- |
| 13 | `phase.DefaultServiceConfig().EventsURLTemplate` | `internal/adapters/inbound/http/router.go` SSE handler | `internal/adapters/inbound/http/router_test.go::TestEventsURL_WireContract` |

## Contract conventions

All cross-repo wire contracts MUST satisfy:

1. **W3C Traceparent propagation**: producer injects `Traceparent` header,
   consumer's TraceW3C middleware extracts it. Validated by middleware tests
   in each repo.
2. **JSON request/response**: `Content-Type: application/json`. Errors
   follow `contract.ErrorResponse{Code, Error, Details?}`.
3. **API key (when present)**: orch provides `X-Sophia-API-Key` /
   `X-API-Key`. Memory enforces it. Governance + runtime are internal-network
   only (no auth in V1, sophia-net Docker bridge boundary).

## Drift detection

Tests under `test/wirecontract/` start a real router from the consumer repo
(via Go workspace import) and assert the producer's URL is reachable.
**404 = drift; the test fails with the matrix row that needs updating.**

To run locally:

```bash
go test -tags=wirecontract ./test/wirecontract/...
```

## CI integration

`.github/workflows/wire-contract.yml` runs the suite on every PR and push to
main. Required check before merge.

## Out of scope (future M-WA milestones)

- AsyncAPI / SSE wire contracts (event payload schemas) — covered by the
  typed payload structs of PR #12, but no formal schema yet.
- gRPC contracts — no gRPC in V1.
- Multi-version migration tests — V1 is implicit-v1 across all paths.

## Appendix — auth / network mapping

| Service     | Inbound auth          | Internal hostname (sophia-net) | External port (compose.full-stack) |
| ----------- | --------------------- | ------------------------------ | ---------------------------------- |
| orchestator | X-Sophia-API-Key      | `orchestator:8080`             | 8080                               |
| memory      | X-API-Key             | `memory-engine:8080`           | 8081                               |
| governance  | none (internal only)  | `governance-core:8080`         | 8082                               |
| runtime     | none (internal only)  | `runtime-adapters:8080`        | 8083                               |
