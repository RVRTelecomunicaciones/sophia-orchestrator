# ADR 0004: PostgreSQL version target (16+ minimum, 17/18 recommended)

- **Status:** accepted
- **Date:** 2026-05-03
- **Deciders:** rfactperu

## Context

PostgreSQL upstream support timeline (verified Feb 2026):

| Major | First release | Status (May 2026) | EOL (planned)   |
|-------|---------------|--------------------|-----------------|
| 18    | Sept 2025     | Current major      | Sept 2030       |
| 17    | Sept 2024     | Supported          | Nov 2029        |
| 16    | Sept 2023     | Supported          | Nov 2028        |
| 15    | Oct 2022      | Supported          | Nov 2027        |
| 14    | Sept 2021     | Supported          | Nov 2026        |
| 13    | Sept 2020     | **EOL Nov 2025**   | unsupported     |

Earlier drafts of this design targeted PG 15+. That tracked sophia-runtime-adapters,
which was started in early 2025 when PG 15 was still the most-deployed LTS line.
Two years later (May 2026), PG 15 has 18 months of support left, and PG 16/17/18
all add features that materially benefit sophia-orchestator:

- **PG 16**: Logical replication of standbys; parallelism for FULL/RIGHT outer joins; SQL/JSON `JSON_TABLE`; `psql \watch` + count; subscription stats; copy-from-stdin progress.
- **PG 17**: Incremental backup (`pg_basebackup --incremental`); `MERGE … RETURNING`; SQL/JSON `JSON_TABLE` enhancements; logical replication slot persistence; lots of vacuum + WAL throughput improvements; `EXPLAIN (memory)`; `random()` parameterizable.
- **PG 18**: Asynchronous I/O (`io_method`); virtual generated columns; `OAUTHBEARER` SASL; `pg_stat_io` enhancements; per-backend statement timeouts; UUIDv7 (`uuidv7()`).

For our spec, the highest-leverage features are:

- **MERGE … RETURNING** (PG 17) — simplifies upsert + idempotency-cache return.
- **UUIDv7** (PG 18) — identifier alternative if we ever migrate off ULIDs.
- **Asynchronous I/O** (PG 18) — meaningful throughput improvement under chaos/load.
- **`pg_stat_io`** (PG 18) — improves observability for the SLO dashboards.

None of these are blocking for V1, but they materially shape the V1 ceiling.

## Decision

1. **Minimum supported version: PostgreSQL 16.** Migrations and SQL must
   compile and execute on PG 16+. This gives us 30 months of support runway.

2. **Recommended target for new deployments: PostgreSQL 17 (LTS-style).**
   Production docker compose, helm chart defaults, and CI integration tests
   pin to PG 17.

3. **Optional opt-in: PostgreSQL 18.** A feature flag in config
   (`db.target_version=17|18`) gates use of PG-18-only features (e.g.,
   `MERGE … RETURNING` extensions exclusive to 18, async I/O tuning).
   V1 ships nothing PG-18-only.

4. **No PG 15 fallback.** sophia-runtime-adapters and friends may run on
   PG 15 — that is their ADR. sophia-orchestator does not need parity here:
   we operate on a different DB schema, deployed independently.

5. **CI matrix**: integration tests run on PG 16 (lower bound) and PG 17
   (recommended). Optional PG 18 job marked `experimental`.

## Consequences

### Positive

- 30-month support runway on the minimum (PG 16 EOL Nov 2028).
- Modern features available without conditional SQL gating: SQL/JSON
  `JSON_TABLE`, `MERGE` (PG 15+), incremental backup (PG 17+), virtual
  generated columns (PG 18+).
- Dropping PG 15 simplifies our migration story: we never have to backport
  PG-16-only syntax.

### Negative

- Operators standardized on PG 15 must upgrade to deploy sophia-orchestator.
  Acceptable because the orchestrator is a new service; existing PG-15 data
  stores are not migrated.
- Dependency on PG-17 features (`MERGE ... RETURNING`) in some idempotency
  paths means PG 16 paths use a slightly less efficient SELECT-then-UPSERT.

### Neutral

- We don't gain a meaningful throughput edge in V1 over PG 15 — the load is
  modest. The version choice is forward-looking, not performance-driven.

## Alternatives considered

- **Track sophia-runtime-adapters at PG 15+**: rejected — PG 15 EOL is Nov 2027,
  shorter runway, and we lose the recent improvements.
- **Target PG 18-only**: rejected — too aggressive for a service that may be
  deployed on customer infra. Recommendation only.
- **Support PG 13+**: rejected — PG 13 EOL was Nov 2025; running EOL DBs in
  prod is irresponsible.
