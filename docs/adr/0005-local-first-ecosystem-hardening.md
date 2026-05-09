# ADR 0005: Local-First Ecosystem Hardening before SaaS or Ecosystem Installer

- **Status:** accepted
- **Date:** 2026-05-09
- **Deciders:** rfactperu

## Context

A cross-ecosystem integration analysis (2026-05-09) of the five Sophia
components (`sophia-orchestator`, `sophia-cli`, `sophia-memory-engine`,
`agent-governance-core`, `sophia-runtime-adapters`) surfaced a critical
finding: **the ecosystem cannot run end-to-end locally today**, and the
`memory-engine` integration documented in ADR-0003 is functionally severed
between the spec and apply phases.

### Smoking-gun finding (the integration is fake at runtime)

`internal/application/apply/run.go:359-394` (`loadTasksList` +
`synthesizeFallbackTasksList`) silently falls back to a hardcoded
single-group / single-task DAG whenever the spec phase's task list cannot be
deserialized from `memory-engine`. The fallback is reached on **every** real
run because:

1. `memory-engine`'s `Get(id)` HTTP handler **does** return the full record
   body — `internal/adapters/inbound/http/memory_handler.go:115,225` — including
   the `Content` field via `toMemoryResponse`. The defect is on the orchestrator
   side: `internal/ports/outbound/memory.go:67-73` defines a `MemoryRecord`
   that **does not model `Content`**, so the HTTP adapter discards it during
   deserialization. ADR-0003's note "real content fetch comes via
   Search-by-topic-key in V2" reinforced this gap on the consumer side.
2. The promised "Search-by-topic-key" endpoint **does not exist** in
   `memory-engine`'s HTTP surface — neither GET nor POST. Only
   `POST /api/v1/search` (full-text + ranking) exists.
3. The fallback path is not flagged as degraded — it returns
   `*tasksList` with no warning, so the apply phase looks healthy while
   producing a fake DAG.

Operational consequences observed:

- All apply-phase parallelization verified in prior smoke runs is **fake**.
  Real spec → apply data flow is severed.
- The `Group.dependsOn` DAG, `DAGCoordinator`, `SpawnGovernor` (2×2=4),
  and team-lead/implement parallelism are correct in the domain model but
  **never exercised on real task lists**.
- Per-task `filesPattern` (used for future glob-locks) is ignored because
  no real tasks reach the board.

### Other ecosystem-wide gaps

| # | Finding | Impact |
|---|---------|--------|
| F1 | `sophia-memory-engine` has migrations (`migrations/postgres/{001_initial_schema,002_retrieval_feedback}.{up,down}.sql` + `migrations/sqlite/`) but **coverage is incomplete relative to the domain model**: only the `memories` aggregate (001) and `retrieval_feedback` (002) are migrated. Decisions, heuristics, relations, project_profiles, purge_records, domain_events tables are created lazily via persistence-adapter behavior or are missing FTS / topic-key indexes. | Schema drift between code and migrations; `goose up` from a clean DB does not guarantee the production schema. |
| F2 | `sophia-memory-engine` has **no docker-compose / Dockerfile** at the repo root. The only compose files in the ecosystem are per-service test harnesses (orchestator/runtime-adapters use `ops/local/compose.yaml` with stub memory). | Full stack cannot be brought up with a single command. |
| F3 | All four backend services default to **port 8080**. CLI's compose uses 9080-9083 but assumes externally-running services. No coordinated port allocation across the ecosystem. | Services collide if started in parallel. |
| F4 | `agent-governance-core` ships a **degradable memory-context provider stub** that returns empty context regardless of the real memory-engine being reachable. | Governance audit-context is empty in V1. |
| F5 | **No end-to-end correlation-ID propagation** from `sophia-cli` → orchestator → memory/governance/runtime. Each service generates its own request IDs. | Distributed debugging requires manual log stitching. |
| F6 | `memory-engine` has **no API authentication** — `scope.tenant_id` is advisory but not enforced at the DB or middleware layer. | Cannot be exposed to multiple consumers safely. |
| F7 | `memory-engine.api/openapi/memory-engine.yaml` is **0 bytes**. | No machine-readable contract; clients reverse-engineer from code. |
| F8 | `memory-engine` topic-key upsert has **no uniqueness constraint** in the DB. Concurrent ingests with the same `(project_id, topic_key)` can create duplicates. | Race condition on retry. |
| F9 | `memory-engine.health` endpoint **does not check Postgres connectivity**. | Compose `service_healthy` orchestration produces false positives. |
| F10 | `runtime-adapters` Phase 1 has **no memory-engine integration** (out of scope per `runtime-adapters/CLAUDE.md` D1.1); Phase 2 design space exists but no contract draft. | Runtime cannot consume historical context yet. This is acceptable, but must be acknowledged. |

### Strategic context

The user is building a SaaS commercial product with a dedicated 3+ person
team. Two prior ADR-class discussions had been opened in the same session:

- A "fork gentle-ai as ecosystem-installer foundation" path
- A "harden each Sophia component for SaaS readiness" path

The gentle-ai fork is the LAST piece of the value chain (an
install/configurator overlay) and it is meaningless if the runtime stack
underneath cannot run end-to-end. ADR-0005 commits to local-first hardening
as the **prerequisite**.

## Decision

We adopt a **local-first ecosystem hardening** posture before any SaaS
preparation work or any external-facing installer fork (e.g., gentle-ai).

The hardening proceeds in three sprints (~6 weeks wall-clock with the
3-person team, ~24 person-days of focused effort) targeting end-to-end
correctness, then operational visibility.

### 1. Sprint 0 — P0 Blockers (Weeks 1-2, ~6.5 days)

**Goal**: `docker compose up` brings the four backend services + 4 Postgres
instances online; the spec → apply data path produces a real task DAG (not
the synthesized fallback).

| Task | Files affected | Effort | Acceptance |
|------|----------------|--------|------------|
| **P0.1** Orchestrator's `MemoryClient` preserves `content`. Memory-engine handler is **NOT** touched (it already returns `content` per `memory_handler.go:115,225`). The orchestrator-side fix: extend `MemoryRecord` (port type) with `Content string`/`Content []byte`, update the HTTP outbound adapter to populate it from the wire response, and update call sites that previously assumed empty content. | `sophia-orchestator/internal/ports/outbound/memory.go` (extend `MemoryRecord`), `sophia-orchestator/internal/adapters/outbound/memoryhttp/*.go` (preserve content during deserialization), call sites in `internal/application/apply/run.go` | 0.5d | Unit + integration tests confirm `MemoryClient.Get` returns a non-empty `Content`; orchestator can deserialize a real artifact end-to-end. |
| **P0.2** Implement topic-key lookup endpoint in memory-engine + adapter wiring in orchestator. Default form: `GET /api/v1/memories/by-topic-key?project_id=...&topic_key=...` (cheap, idempotent, cache-friendly). Use `POST /api/v1/memories/by-topic-key` (body-encoded) if the full `Scope` (tenant + project + repo + agent + session + environment) is required for matching. Orchestator's `loadTasksList` switches from `Get(id)` → topic-key lookup. | `sophia-memory-engine/internal/adapters/inbound/http/retrieval_handler.go` (new endpoint), `sophia-memory-engine/internal/application/services/retrieval_service.go` (new method), `sophia-memory-engine/internal/ports/outbound/repositories.go` (repo method); `sophia-orchestator/internal/adapters/outbound/memoryhttp/by_topic_key.go`, orchestator `loadTasksList` rewrite | 2d | `internal/application/apply/run.go:loadTasksList` returns the real tasks list saved by the spec phase. **`synthesizeFallbackTasksList` is removed in this PR — not just deprecated.** |
| **P0.3** **Audit and complete** existing migrations in `sophia-memory-engine/migrations/postgres/` (current state: `001_initial_schema` for `memories`, `002_retrieval_feedback`). Verify `goose up` from a clean DB reproduces the schema actually used by every persistence adapter, and add migrations for missing aggregates and indexes: `decisions`, `heuristics`, `relations`, `project_profiles`, `purge_records`, `domain_events`, plus FTS indexes, topic-key indexes, and the search-ranking indexes the retrieval service needs. | `sophia-memory-engine/migrations/postgres/{003..00N}_*.{up,down}.sql` (new), CI workflow that runs `goose up` against a clean Postgres + integration tests | 2d | `goose up` on an empty Postgres reproduces every table, index, and constraint exercised by `internal/adapters/outbound/persistence/*` and `retrieval_service`. Coverage matrix attached to the PR (memories ✅, decisions, heuristics, relations, purge, feedback ✅, search indexes). |
| **P0.4** Unify docker-compose for the full stack at the ecosystem level | New file `tools/compose/full-stack.compose.yaml` (location TBD) — 4 services on 8080-8083, 4 dedicated Postgres instances on 5434-5437, healthchecks on `/ready` (see P1.4) | 2d | `docker compose up` brings all four services + 4 PG instances to healthy in <60s; no port collisions |

**Sprint 0 exit criteria**:

1. `synthesizeFallbackTasksList` is **deleted**, not just bypassed.
2. `MemoryRecord` (orchestrator port type) carries non-empty `Content` end-to-end; an integration test asserts the round-trip from spec save to apply load preserves the artifact body.
3. Memory-engine's `migrations/postgres` covers all aggregates exercised by the persistence adapters; CI gate runs `goose up` on a clean DB before integration tests.
4. A smoke test runs the full SDD pipeline locally (CLI → orchestator → memory + governance + runtime) against the unified compose and produces a real multi-group apply board.
5. ADR-0003 is amended (or a follow-up ADR is opened) reflecting that the
   "V2: search-by-topic-key" item is now V1.

### 2. Sprint 1 — P1 Correctness (Weeks 3-4, ~10.5 days)

**Goal**: end-to-end results are observable, authenticated, and free from
silent race conditions.

| Task | Files affected | Effort | Acceptance |
|------|----------------|--------|------------|
| **P1.1** Correlation-ID propagation end-to-end (W3C `traceparent` preferred; `X-Request-ID` accepted as fallback). CLI generates → orchestator extracts in inbound middleware → propagates to memory/governance/runtime via `httpclient.Builder` → memory-engine emits in slog + DB query annotations | `sophia-cli/internal/infrastructure/httpclient/builder.go`, `sophia-orchestator/internal/ports/inbound/http/middleware.go`, `sophia-memory-engine/internal/adapters/inbound/http/middleware.go`, governance core middleware, all four service slog handlers | 3d | E2E test asserts the same `correlation_id` appears in all four service logs and in the Postgres `audit_log` rows |
| **P1.2** API-key auth in memory-engine (header `X-Api-Key`, validated against a `keys` table; key resolves to `(tenant_id, project_id)` for scope enforcement) | `sophia-memory-engine/internal/adapters/inbound/http/middleware.go` (new `apikey.go`), new migration `0008_create_keys.sql` | 3d | Missing/invalid key → 401; valid key → 200; integration test uses keys from compose env |
| **P1.3** Topic-key uniqueness — DB unique index on `(project_id, tenant_id, topic_key) WHERE archived_at IS NULL` + upsert path in `MemoryService.Ingest` | `sophia-memory-engine/migrations/postgres/0009_topic_key_unique.up.sql`, `internal/application/services/memory_service.go` | 1.5d | Concurrent ingest test with same topic_key yields exactly one row |
| **P1.4** `/ready` endpoint that checks Postgres + (when applicable) search index. Health checks in compose switch from `service_started` to `service_healthy` against `/ready` | `sophia-memory-engine/internal/adapters/inbound/http/health_handler.go`, governance + orchestator + runtime equivalents already exist (verify and align) | 1.5d | Stopping Postgres → `/ready` returns 503; compose orchestration respects it |
| **P1.5** Scope enforcement at the persistence layer — every SELECT/UPDATE in memory-engine's persistence adapters must include `WHERE project_id = $X AND tenant_id = $Y` predicates derived from the authenticated key | `sophia-memory-engine/internal/adapters/outbound/persistence/*.go`, plus governance + orchestator scope guards | 1.5d | Integration test confirms a key scoped to project A cannot read records of project B even when given a known record ID |

**Sprint 1 exit criteria**:

1. The unified compose stack is auth-protected.
2. End-to-end requests are traceable in logs by a single correlation_id.
3. Concurrent same-key ingests produce one row.
4. Cross-project reads are blocked at the DB query layer.

### 3. Sprint 2 — P2 Observability (Weeks 5-6, ~7 days)

**Goal**: when something fails on the unified compose, there is a way to see
why and to compare ingest/retrieval performance against budget.

| Task | Files affected | Effort | Acceptance |
|------|----------------|--------|------------|
| **P2.1** Generate the OpenAPI spec from code (oapi-codegen or manual) and validate in CI | `sophia-memory-engine/api/openapi/memory-engine.yaml`, new `Makefile` target `openapi-gen`, CI workflow `.github/workflows/openapi.yml` | 2d | OpenAPI spec round-trips against handler signatures in CI; spec is the single source of truth for client codegen |
| **P2.2** OTEL traces + spans in memory-engine — instrument `Ingest`, `Get`, `Search`, `BuildContext`; export to configurable OTLP endpoint; align span attribute conventions with orchestator + runtime-adapters | `sophia-memory-engine/internal/infrastructure/obs/`, bootstrap wiring, compose otel-collector wiring | 3d | Trace from CLI → orchestator → memory-engine renders as a single trace tree in Jaeger / Tempo |
| **P2.3** Retrieval tuning — B-tree index on `(project_id, topic_key)`, ranking weights tuned for `sdd_*` types (boost exact topic_key match, demote truncated snippets) | `sophia-memory-engine/migrations/postgres/0010_topic_key_index.up.sql`, ranking config in `internal/application/services/retrieval_service.go` | 2d | EXPLAIN ANALYZE shows index use on the hot query; benchmark shows ≥3× speedup on the apply-phase load path |

**Sprint 2 exit criteria**:

1. The OpenAPI spec is committed and CI-validated.
2. A single trace links CLI → orchestator → memory-engine end-to-end.
3. The hot retrieval path (apply-phase task-list load) is index-backed and benchmarked.

### 4. Out-of-scope (explicitly deferred)

- **Phase 2E** of `sophia-runtime-adapters` (auth + multi-tenant). Trigger: shared deployment. Belongs to a future SaaS-readiness ADR.
- **Async execution** in runtime-adapters (Phase 2A). Trigger: long-running capability degrades sync UX.
- **Distributed lock manager** (runtime-adapters Phase 2B). Trigger: real concurrent collision incident.
- **JWT auth** to replace API-key (memory-engine SaaS-readiness). Belongs to a future SaaS ADR.
- **Multi-tenant quota / billing metadata**. Future SaaS ADR.
- **Memory-engine workers** for ProjectProfile regeneration (the `internal/jobs/` scaffold). Reserved as a follow-up; not on the local-first critical path.
- **Embedding provider** (currently `noop.go`). Reserved; cosine-similarity retrieval is not required for the SDD pipeline today.
- **Fork of gentle-ai** as the ecosystem-installer overlay. Reserved as a Phase-after-this ADR. Cannot meaningfully evaluate without a working local stack.
- **Migration of `memory-engine.txt` (249 KB design dialogue dump) into proper docs/ADRs**. Documentation hygiene; not on the critical path but should be queued behind Sprint 2.

## Consequences

### Positive

- The synthesizeFallbackTasksList smoking gun is **removed** in Sprint 0,
  so all apply-phase parallelism documented in the V1 GA spec is exercised
  on real data for the first time.
- The unified compose stack means one command brings the whole ecosystem up
  for development, demos, and CI smoke runs.
- API-key auth + scope enforcement makes memory-engine safe to expose to
  any internal consumer (governance, orchestator, future agents) without
  a SaaS-readiness gate.
- OpenAPI + OTEL + correlation-ID create a coherent debugging story across
  service boundaries — without these, every future P1 incident requires
  manual log-stitching.

### Negative

- A six-week pause on outward-facing work (gentle-ai fork, SaaS plan, agent
  installer marketing). The pause is unavoidable: the runtime stack must
  produce correct results before any value layer on top of it can be sold.
- ADR-0003 must be amended in Sprint 0 (search-by-topic-key moves from V2
  → V1). This is a documentation update, not a behavior regression.
- The orchestator's BLOCKED-on-memory-failure semantics (ADR-0003 §7) are
  exercised more aggressively once the fallback is removed. Failure
  surface increases at first. Sprint 1 mitigates by making correlation IDs
  available so blocked phases are diagnosable in <5 minutes.

### Neutral

- runtime-adapters Phase 1 status is unchanged; this ADR neither accelerates
  nor delays its Phase 2 trigger-driven work.
- governance-core's degradable memory stub is **not replaced** in this ADR;
  it is acknowledged (F4) and will be revisited once memory-engine is
  hardened.

## Alternatives considered

- **A: Push gentle-ai fork forward in parallel.** Rejected — putting an
  installer overlay on top of a stack that silently produces wrong apply
  DAGs (smoking gun) creates a brand liability. The first customer who runs
  the SDD pipeline end-to-end will see fake parallelism, and the trust loss
  is unrecoverable.
- **B: Skip Sprint 2 (observability) and ship Sprint 0 + 1 only.** Rejected
  — without correlation IDs and OpenAPI, every Sprint-1 P1 incident
  requires manual log stitching across four service repos. The 7-day
  Sprint 2 cost is bounded; the operational debt without it is not.
- **C: Replace memory-engine with a third-party (Mem0, Letta, etc.)
  rather than harden the in-house service.** Rejected — the in-house
  schema (typed records, scope, temporal validity, decisions, heuristics,
  relations, hard purge) is purpose-built for the SDD audit story.
  Migrating would require re-modeling the entire domain.
- **D: Repair only the Apply-phase data path (P0.1 + P0.2) and defer the
  rest.** Rejected — without P0.3 (migrations) and P0.4 (compose), the
  fix cannot be validated on a clean local stack. The P0 set is a connected
  unit.

## Follow-ups

- Amend ADR-0003 on completion of P0.2 (move "search-by-topic-key" from
  V2 → V1).
- Open a follow-up ADR proposing the SaaS-readiness scope (Phase 2E of
  runtime-adapters, JWT auth, multi-tenant quotas, billing metadata) once
  Sprint 2 closes.
- Open a follow-up ADR for the gentle-ai fork once the local stack
  produces correct end-to-end results.
- Backfill `memory-engine`'s 249 KB `memory-engine.txt` dialogue dump into
  proper architectural docs and ADRs.
