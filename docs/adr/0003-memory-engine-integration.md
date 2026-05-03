# ADR 0003: sophia-memory-engine integration contract

- **Status:** accepted
- **Date:** 2026-05-03
- **Deciders:** rfactperu

## Context

sophia-orchestator persists SDD artifacts (proposal, spec, design, tasks, etc.)
and audit-trail events through `sophia-memory-engine`, the production memory
service of the Sophia ecosystem. Earlier drafts of this design referenced
`engram` as a candidate backend; that was a misnomer — `engram` is a
session-level personal memory MCP used by AI assistants and is **not** part of
the Sophia ecosystem. This ADR locks in the real integration.

`sophia-memory-engine` exposes:

- `POST /api/v1/memories` — ingest a `MemoryRecord` (typed, scoped, provenance-stamped, optional `topic_key`, temporal validity)
- `GET /api/v1/memories/{id}` — fetch a record
- `POST /api/v1/memories/{id}/archive` — archive (not hard delete)
- `POST /api/v1/decisions` — record a decision (with rationale + scope + confidence)
- `GET /api/v1/decisions/{id}`, `GET /api/v1/decisions/history/{key}`, `POST /api/v1/decisions/{id}/contradict`
- `POST /api/v1/heuristics`, `GET /api/v1/heuristics/active/{key}`, `GET /api/v1/heuristics`, `POST /api/v1/heuristics/{id}/toggle`
- `POST /api/v1/relations`, `GET /api/v1/relations/from/{id}`, `GET /api/v1/relations/to/{id}`
- `POST /api/v1/search` — FTS + ranking with scope/type/limit
- `POST /api/v1/search/context` — Context bundle (multi-section, token-budgeted)
- `POST /api/v1/purge/request`, `POST /api/v1/purge/{id}/execute` — security-grade hard purge
- `POST /api/v1/feedback`
- `GET /health`

Memory record envelope (relevant fields):

```jsonc
{
  "type":        "sdd_proposal | sdd_spec | sdd_design | sdd_tasks | sdd_apply_progress | sdd_verify | sdd_archive | sdd_audit",
  "content":     "<artifact body>",
  "summary":     "...",
  "tags":        ["sdd", "phase:spec"],
  "topic_key":   "sdd/{change_name}/{phase_type}",   // upsert key
  "scope":       { "tenant_id", "project_id", "repo_id", "agent_id", "session_id", "environment" },
  "provenance":  { "source": "sophia-orchestator", "source_uri": "/api/v1/changes/{id}/phases/{type}", "method": "sdd-phase-output" },
  "valid_from":  "RFC3339",
  "valid_until": "RFC3339"
}
```

## Decision

1. **Outbound port** `internal/ports/outbound/memory.go` defines a
   `MemoryClient` interface with the operations sophia-orchestator needs:
   `Ingest`, `Get`, `Archive`, `Search`, `BuildContext`, `RecordDecision`,
   `RecordRelation`. This is a curated subset of memory-engine's full API —
   we do not expose heuristics or purge from orchestrator.

2. **Topic-key namespace** is `sdd/{change_name}/{phase_type}` for phase
   artifacts and `sdd/{change_name}/apply-progress` for incremental apply
   updates. Re-ingesting the same `topic_key` is treated as upsert by
   memory-engine.

3. **Scope** carried on every ingest:
   - `tenant_id` = caller's tenant (V1: single-tenant, hardcoded; V2: from auth)
   - `project_id` = the SDD `project` field
   - `repo_id`   = optional, when known (e.g., from base_ref)
   - `agent_id`  = `"sophia-orchestator"`
   - `session_id` = the orchestrator's `change_id` (treated as session)
   - `environment` = `dev | staging | prod` (from config)

4. **Provenance**:
   - `source` = `"sophia-orchestator"`
   - `source_uri` = the orchestrator's REST URL that produced the artifact
   - `method` = `"sdd-phase-output"` for phase envelopes; `"sdd-audit"` for audit events; `"sdd-decision"` for governance-decision mirrors

5. **Audit mirror**: every Iron Law violation, phase transition, and apply
   board event is mirrored to memory-engine via `POST /memories` with
   `type=sdd_audit`. This complements the local `audit_log` Postgres table
   (insert-only, R11) — Postgres is the system of record; memory-engine is
   the queryable index.

6. **Artifact store modes** (per `domain.change.ArtifactStoreMode`):
   - `memory-engine` (default) — ingest to memory-engine only
   - `openspec` — write to filesystem only (`openspec/changes/{change_name}/...`)
   - `hybrid` — write to both; reads prefer memory-engine with openspec fallback
   - `none` — return inline; no persistence (transient, mostly for tests)

7. **Failure semantics**:
   - V1: if memory-engine is unreachable on `Ingest`, the orchestrator marks
     the phase `BLOCKED` (Iron Law #1: persisted-before-return must include
     the artifact, not just the orchestrator's own DB row). Caller must
     retry via `/resume`.
   - V2: introduce a write-behind queue with bounded retry budget.

## Consequences

### Positive

- Reuses the existing memory-engine schema (typed records, temporal validity,
  scope, provenance) instead of inventing a parallel artifact registry.
- The same memory-engine that powers `sophia-orchestator` also serves
  `agent-governance-core` and other ecosystem clients — single source of truth.
- `topic_key` upserts give us idempotent re-runs without orchestrator-side
  deduplication.
- The rich scope model lets us query "all proposals for project X" or
  "all spec artifacts created in environment dev" without schema changes.

### Negative

- A memory-engine outage causes orchestrator phase failures (until V2 introduces
  buffered writes). This matches the spec's Iron Law #1 — better to fail loudly
  than persist locally and forget to forward.
- Memory-engine's API is rich (purge, heuristics, project DNA) — orchestrator
  intentionally consumes only the subset above to keep the integration small.

### Neutral

- The orchestrator's `audit_log` Postgres table is duplicated in memory-engine.
  We accept the redundancy: PG is fast for ordered scans; memory-engine is
  queryable with FTS + scope + temporal filters.

## Alternatives considered

- **Embed artifact storage in orchestrator's own Postgres**: rejected — duplicates
  memory-engine's responsibility and creates a parallel system of record.
- **Use `engram` MCP as backend**: rejected — `engram` is a personal/session
  memory tool for AI assistants, not a production service.
- **Use git as the artifact store** (commit each artifact to the repo):
  rejected — couples orchestrator to git semantics and conflicts with
  worktree-managed apply-phase branches.
