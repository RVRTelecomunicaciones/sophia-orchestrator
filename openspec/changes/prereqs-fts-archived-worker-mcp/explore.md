# Exploration — prereqs-fts-archived-worker-mcp (M-KNOW-PRE-0)

**Strategy ref:** V4.1, locked decisions D12 + D13, milestone M-KNOW-PRE-0.
**Mode:** SDD explore. NO production code changes; investigation only.
**Cross-repo scope:** sophia-orchestator + sophia-memory-engine + sophia-agent-mcp + sophia-cli (wire mirror).
**Engram artifact:** `sdd/prereqs-fts-archived-worker-mcp/explore` (topic_key).

---

## 1. Current state — per item

### Item 1: FTS config (D12)

**Files**:
- `sophia-memory-engine/internal/adapters/outbound/search/postgres_fts.go:110, 112, 117` — three `'spanish'` literals inside SQL strings
- `sophia-memory-engine/internal/domain/memory/memory.go:119` — `FTSLanguage: "spanish"` default
- `sophia-memory-engine/migrations/postgres/001_initial_schema.up.sql:78-91` — trigger `trg_memories_fts` is `BEFORE INSERT OR UPDATE`, body reads `NEW.fts_language` dynamically
- `sophia-memory-engine/migrations/postgres/001_initial_schema.up.sql:105` — `decisions.fts_language REGCONFIG NOT NULL DEFAULT 'spanish'`
- `heuristics` table also has `fts_language` default `'spanish'` per the same migration

**Critical structural fact**: `search_vector` is a plain `TSVECTOR`, NOT `GENERATED ALWAYS AS STORED`. It is populated by a trigger that reads `NEW.fts_language` per row. So fixing existing rows is just:

```sql
UPDATE memories SET fts_language = 'simple';
UPDATE decisions SET fts_language = 'simple';
UPDATE heuristics SET fts_language = 'simple';
```

The trigger fires per row and rebuilds `search_vector`. Zero downtime.

**Scope expansion vs V4.1**: V4.1 D12 implied only `memories`. The audit was correct narrowly but reality covers **three tables**: `memories`, `decisions`, `heuristics`. Migration must touch all three.

### Item 2: phase.archived event (D13)

**Files**:
- `sophia-orchestator/internal/ports/inbound/event_types.go` — `EventPhaseArchived` does NOT exist
- `sophia-orchestator/internal/application/phase/service.go:1057-1069` — `eventTypeForStatus()` maps to `EventPhaseCompleted`, no archive variant
- `sophia-orchestator/internal/application/phase/service.go:911` — `advanceChange()` detects `completed == phase.PhaseArchive` and calls `c.MarkCompleted` — this is the correct emission point for `EventPhaseArchived`
- `sophia-cli/pkg/contract/wire_alignment_test.go:177` — `TestWireAlignment_OrchEventsMirrored` AST-parses every `Event*` constant in orch and verifies they exist in CLI's `knownEvents`

**Mandatory CI gate**: orch + CLI event constants must land in the SAME PR. The wire_alignment test fails the build otherwise.

### Item 3: Worker skeleton

**File**: `sophia-memory-engine/cmd/workers/main.go` — 5-line stub:

```go
package main
func main() {
    // TODO: initialize and start background workers
}
```

**Event bus reality**: memory-engine has `InProcessEventPublisher` for its own domain events. **It is NOT usable for orchestator events** (different process). The worker must consume orch events via one of:
- SSE client subscribing to orchestator's HTTP event stream
- Webhook receiver (orch POSTs to worker)
- Message bus (deferred; not built today)

**PRE-0 decision**: keep skeleton noop with a stub handler. **Transport choice is explicitly deferred to M2**. The acceptance criterion ("worker starts without error, logs receipt of a fake phase.archived payload in unit tests") can be satisfied by a fake/in-memory publisher in tests.

### Item 4: mcp_providers[] config skeleton

**Files**:
- `sophia-agent-mcp/internal/infrastructure/config/config.go` — `Config` struct has NO `MCPProviders` field
- `sophia-agent-mcp/internal/infrastructure/config/loader.go:24` — uses `toml.DecodeFile` (BurntSushi/toml)

**Format reality**: agent-mcp uses **TOML**, not YAML. V4.1 examples used YAML. They are semantically equivalent.

TOML syntax for the V4.1 example:

```toml
[[mcp_providers]]
id = "graphify"
package = "graphifyy[mcp]==0.8.35"
command = "graphify serve graphify-out/graph.json"
transport = "stdio"
tools_allowed = [
  "query_graph", "get_node", "get_neighbors", "get_community",
  "god_nodes", "graph_stats", "shortest_path", "get_pr_impact",
  "triage_prs", "list_prs"
]
lifecycle = "spawned_per_change"
```

**No new parser dependency required** — adding `MCPProviders []MCPProviderConfig` to `Config` is enough for the existing TOML decoder.

---

## 2. Affected areas (concrete file list)

| File | Action | Item |
|---|---|---|
| `sophia-memory-engine/internal/adapters/outbound/search/postgres_fts.go` | modify (3 literals: spanish→simple) | 1 |
| `sophia-memory-engine/internal/domain/memory/memory.go:119` | modify (default change) | 1 |
| `sophia-memory-engine/migrations/postgres/005_fts_simple.up.sql` | NEW | 1 |
| `sophia-memory-engine/migrations/postgres/005_fts_simple.down.sql` | NEW | 1 |
| `sophia-orchestator/internal/ports/inbound/event_types.go` | add `EventPhaseArchived` | 2 |
| `sophia-orchestator/internal/ports/inbound/event_payloads.go` | add `PhaseArchivedPayload` | 2 |
| `sophia-orchestator/internal/application/phase/service.go` | emit at archive completion (around L911) | 2 |
| `sophia-cli/pkg/contract/events.go` | mirror `EventPhaseArchived` constant | 2 |
| `sophia-memory-engine/cmd/workers/main.go` | skeleton with stub handler | 3 |
| `sophia-agent-mcp/internal/infrastructure/config/config.go` | add `MCPProviders []MCPProviderConfig` | 4 |
| `sophia-agent-mcp/internal/infrastructure/config/loader.go` | validation for mcp_providers | 4 |
| `sophia-agent-mcp/internal/infrastructure/mcp/allowlist.go` (NEW) | tools_allowed enforcement | 4 |

---

## 3. Cross-repo coupling map

- Three modules are **independent Go modules**: no go.work, no replace directives, no shared imports.
- Event type string `"phase.archived"` must be a **string literal or local constant** in the worker; cannot be imported from orchestator.
- Only automated cross-repo gate: `wire_alignment_test.go` (orch ↔ CLI). Other coupling is by-convention.
- sophia-memory-engine is the HTTP backend for orchestator memory — separate from engram (which is SDD artifact store only).

---

## 4. Migration concerns

**Migration 005 in sophia-memory-engine**:

Up (idempotent):
```sql
UPDATE memories   SET fts_language = 'simple' WHERE fts_language = 'spanish';
UPDATE decisions  SET fts_language = 'simple' WHERE fts_language = 'spanish';
UPDATE heuristics SET fts_language = 'simple' WHERE fts_language = 'spanish';

ALTER TABLE memories   ALTER COLUMN fts_language SET DEFAULT 'simple';
ALTER TABLE decisions  ALTER COLUMN fts_language SET DEFAULT 'simple';
ALTER TABLE heuristics ALTER COLUMN fts_language SET DEFAULT 'simple';
```

Down (idempotent):
```sql
UPDATE memories   SET fts_language = 'spanish' WHERE fts_language = 'simple';
UPDATE decisions  SET fts_language = 'spanish' WHERE fts_language = 'simple';
UPDATE heuristics SET fts_language = 'spanish' WHERE fts_language = 'simple';

ALTER TABLE memories   ALTER COLUMN fts_language SET DEFAULT 'spanish';
ALTER TABLE decisions  ALTER COLUMN fts_language SET DEFAULT 'spanish';
ALTER TABLE heuristics ALTER COLUMN fts_language SET DEFAULT 'spanish';
```

Trigger rebuilds `search_vector` per row. Zero downtime under typical SDD workload.

---

## 5. Test surface

Per sdd-init cache (`sdd-init/2026`):

| Repo | Unit | Integration |
|---|---|---|
| sophia-orchestator | `make test-unit` | `make test-integration` (testcontainers, -tags=integration, 5m) |
| sophia-memory-engine | `make test-unit` | `make test-integration` (testcontainers, -tags=integration) |
| sophia-agent-mcp | `make test` | n/a (unit-only by design) |
| sophia-cli | `make test` | wire_alignment_test runs as part of unit |

**Strict TDD: ENABLED** for orchestator + memory-engine. Tests-first for production logic.

Patterns:
- Table-driven subtests + testify
- testcontainers for PG integration
- Event emission tests: orchestator uses goroutine + per-subscriber chan SSE stream (subscribe + assert payload pattern)
- Worker e2e test can use a fake event publisher OR an in-process test bus (no transport choice required for skeleton)

---

## 6. Risks / blockers

1. **FTS migration scope wider than V4.1 said**: 3 tables (memories, decisions, heuristics) — not just memories. Spec must cover all three.
2. **Wire alignment is a HARD CI gate**: orch + CLI changes for `EventPhaseArchived` MUST land in the same PR. Operator's "no commit without approval" rule means we need a single checkpoint covering both repos.
3. **Worker transport unresolved**: PRE-0 skeleton uses a stub. M2 design needs to choose SSE / webhook / bus. NOT a PRE-0 blocker, but spec must explicitly mark this as deferred.
4. **TOML vs YAML mismatch in V4.1 docs**: agent-mcp uses TOML. V4.1 examples show YAML. Cosmetic only — semantically equivalent. Recommend updating V4.1 examples to TOML in a follow-up doc patch (or accept both formats notation).
5. **Go toolchain version discrepancy** between repos (1.26.2 vs 1.26.3): non-blocking, but worth noting in CI environment.

---

## 7. Approaches considered

### Item 1 — FTS migration

- **A**: change config literals only, no migration → REJECTED (existing rows keep stale fts_language; queries inconsistent)
- **B**: full migration UPDATE + ALTER DEFAULT covering 3 tables → SELECTED
- **C**: drop trigger and recreate with hardcoded `'simple'` → REJECTED (loses flexibility; FTSLanguage is a per-row column for a reason)

### Item 2 — phase.archived

- **A**: filter `phase.completed` with status check → REJECTED by D13 explicitly
- **B**: new `EventPhaseArchived` constant + emission → SELECTED (D13)

### Item 3 — Worker subscription

- **A**: SSE client stub (committed transport) → DEFERRED to M2
- **B**: webhook receiver stub → DEFERRED to M2
- **C**: noop with fake event publisher in tests → SELECTED for PRE-0 skeleton

### Item 4 — mcp_providers config

- **A**: YAML (matches V4.1 examples, adds yaml.v3 dep) → REJECTED (unnecessary dep)
- **B**: TOML array-of-tables (matches existing loader) → SELECTED

---

## 8. Recommendation

**Proceed to sdd-propose.**

All four items have clear scope, file-level surface area, and acceptance criteria derived from V4.1. The only friction is:
- FTS migration scope wider (3 tables) — surface in proposal
- Wire alignment is a hard CI gate — surface as a "single PR" requirement in tasks
- Worker transport deferred — surface as explicit non-goal in proposal

---

## 9. Skill resolution

No project-specific skill registry found at `.atl/skill-registry.md` or in engram. Standard SDD phase skills apply per `sdd-init/2026`:
- Apply will need: go-testing (table-driven + testcontainers), persistence-postgres (golang-migrate), api-contracts (event constants alignment), testing-quality.

skill_resolution status: `none` — proceed with standard SDD phase skills.
