# Sophia Surface Inventory — Pre-V4 Implementation

## Metadata

- Inventoried at: 2026-06-07
- Sophia orchestator HEAD: `1f34425d0b3e75355474ff48a13a5b1b8c2019f2`
- Sophia memory-engine HEAD: `fb07775bed624e55cffcda49478056afbdd92658`
- Strategy ref: V4 (locked decisions D1-D11)

---

## 1. Skills aggregate

### 1.1 File paths

| Artifact | Path |
|---|---|
| Domain aggregate | `internal/domain/skill/skill.go` |
| Technique enum | `internal/domain/skill/technique.go` |
| Port interface (SkillRepository) | `internal/ports/outbound/repository.go:86-103` |
| Port interface (SkillProvider) | `internal/application/discipline/skill_provider.go` |
| PG repository adapter | `internal/adapters/outbound/pg/skill_repo.go` |
| PG SkillProvider adapter | `internal/adapters/outbound/pg/skill_provider.go` |
| Seed bootstrap | `internal/bootstrap/seed_skills.go` |
| Wire composition root | `internal/bootstrap/wire.go:100,193-199,362-365` |

### 1.2 Current SQL schema (migration 009)

File: `migrations/postgres/009_skills.up.sql`

```sql
CREATE TABLE IF NOT EXISTS skills (
    id          CHAR(26)    NOT NULL,
    name        TEXT        NOT NULL,
    phases      TEXT[]      NOT NULL,
    content     TEXT        NOT NULL,
    techniques  TEXT[]      NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT  skills_pkey PRIMARY KEY (id),
    CONSTRAINT  skills_name_unique UNIQUE (name)
);

CREATE INDEX IF NOT EXISTS skills_phases_gin ON skills USING GIN (phases);
```

Columns: `id, name, phases, content, techniques, created_at, updated_at`
Indexes: GIN on `phases`, UNIQUE on `name`

### 1.3 Upsert definition and callers

`Upsert` is defined at `internal/adapters/outbound/pg/skill_repo.go:56-77`.
It performs `INSERT ... ON CONFLICT (id) DO UPDATE SET name, phases, content, techniques, updated_at`.

Non-test callers of `Upsert`: **zero**. The only write path at boot uses `InsertIfAbsent` (seed). No application code calls `Upsert` today.

`InsertIfAbsent` is called by `SeedSkills` in `internal/bootstrap/seed_skills.go:36`.

### 1.4 The 9 hybrid seed skills

All defined in `internal/bootstrap/seed_skills.go:58-285`. All use `phase.PhaseXxx` (one phase each).

| # | Name | Phase | Technique tags | Content summary |
|---|---|---|---|---|
| 1 | `init-bootstrap-context` | init | step_back, inline_why | Anchor change name/project; surface ambiguities as open questions |
| 2 | `explore-investigate` | explore | react, inline_why | ReAct cycle: observe first, reason second; record WHERE/WHAT/WHY per finding |
| 3 | `proposal-draft-options` | proposal | skeleton_of_thought, inline_why | Skeleton-of-Thought with ≥2 approaches, tradeoff table (complexity/testability/risk) |
| 4 | `spec-write-requirements` | spec | skeleton_of_thought, inline_why | Skeleton before filling; GIVEN-WHEN-THEN; no TBD; observable outcomes |
| 5 | `design-architect-system` | design | extended_thinking, step_back, inline_why | Aggregate-boundary view; enumerate what is NOT done; persistence before interface |
| 6 | `tasks-decompose-work` | tasks | extended_thinking, inline_why | Dependency graph first; 2-5 min tasks; explicit depends_on; non-overlapping files_pattern |
| 7 | `apply-implement-safely` | apply | constitutional_self_critique, inline_why | Self-critique checklist after each task before marking done |
| 8 | `verify-chain-validation` | verify | chain_of_verification, inline_why | Claim → command → exact output; cite test name per spec scenario |
| 9 | `archive-finalize-deltas` | archive | step_back, inline_why | Re-read proposal intent; delta summary; document deviations |

### 1.5 SkillsForPhase callsites (non-test)

| File | Line | Context |
|---|---|---|
| `internal/adapters/outbound/pg/skill_provider.go` | 28 | Adapter delegates to `FindByPhase` |
| `internal/application/phase/service.go` | 361 | Phase execution path — hydrates skills fail-soft before prompt build |
| `internal/application/apply/teamlead.go` | 587 | `hydrateSkills` helper — called at lines 382 and 481 for team-lead and implement dispatches |

### 1.6 Prompt rendering

File: `internal/application/discipline/prompt_builder.go`

- `renderSkillSection` at line 263 is called from `Build` at line 100.
- Rendered AFTER HARD-GATE markers and BEFORE `# Prior Context`.
- Format: `# Skill` header → per-skill `## <name>`, `Techniques: tag-a, tag-b`, verbatim content block.
- Empty/nil skills → section omitted entirely (byte-identical to pre-skills baseline).

---

## 2. Memory engine — MemoryTypeEpisodic + FTS

### 2.1 File paths

| Artifact | Path |
|---|---|
| Domain enums (MemoryType, MemoryStatus, IngestMethod, EventType) | `internal/domain/shared/enums.go` |
| MemoryRecord aggregate | `internal/domain/memory/memory.go` |
| PG persistence adapter | `internal/adapters/outbound/persistence/memory_pg.go` |
| FTS search adapter | `internal/adapters/outbound/search/postgres_fts.go` |
| Ingest service | `internal/application/ingest/service.go` |
| Retrieval service | `internal/application/retrieval/search.go` |
| Context builder | `internal/application/retrieval/context_builder.go` |
| HTTP router (endpoint listing) | `internal/adapters/inbound/http/server.go` |

### 2.2 Current SQL schema (memory-engine tables)

Migration 001 (`migrations/postgres/001_initial_schema.up.sql`):

**Table: `memories`**

```
id, type, content, summary, tags[], topic_key, fts_language (REGCONFIG),
tenant_id, project_id, repo_id, agent_id, session_id, environment,
source, source_uri, ingest_method, parent_id (FK self),
valid_from, valid_until, last_accessed, freshness,
importance_score, importance_computed_at, importance_factors (JSONB),
status, archived_by, archive_reason,
search_vector (TSVECTOR), created_at, updated_at
```

Constraints: `type IN ('episodic','semantic')`, `ingest_method IN ('direct','derived','imported','worker_generated')`, `status IN ('active','archived','purged')`, `freshness IN ('fresh','aging','stale','expired')`, episodic requires `valid_from IS NOT NULL`.

Indexes: GIN on `search_vector` (FTS), GIN on `content gin_trgm_ops` (trigram), `(project_id, type)`, `(project_id, repo_id, agent_id, session_id, environment)`, `topic_key`, `status`, `freshness`, `tenant_id`, `created_at DESC`.

**Tables also in 001**: `decisions`, `heuristics`, `relations`, `purge_records`, `project_profiles`, `domain_events`.

Migration 002 (`002_retrieval_feedback.up.sql`): `retrieval_feedback` table (target, feedback_type, query, scope fields, result_ids, selected_ids, actor, metadata).

Migration 003 (`003_create_api_keys.up.sql`): `api_keys` table.

Migration 004 (`004_memories_topic_key_unique.up.sql`): Partial unique index on `topic_key` for active rows (idempotent upsert by topic_key).

### 2.3 FTS implementation

File: `internal/adapters/outbound/search/postgres_fts.go`

- `plainto_tsquery('spanish', $1)` with `ts_rank` as primary sort.
- `similarity(content, $1) > 0.3` as `pg_trgm` fallback (OR condition).
- `ts_headline('spanish', ...)` for snippets.
- Scope filters: `project_id` (required), optional `tenant_id`, `repo_id`, `agent_id`, `session_id`, `environment`.
- Only searches `memories` table. Decisions and heuristics are mentioned in the comment as "Phase 1 only" to be added via `UNION ALL` in the search service layer.
- FTS language default: `'spanish'` (hardcoded in `memory.go:119`).

### 2.4 HTTP endpoints exposed by memory-engine

All under `/api/v1` (API key required via `middleware.APIKey`):

| Method | Path | Handler |
|---|---|---|
| POST | `/api/v1/memories` | Ingest |
| GET | `/api/v1/memories/by-topic-key` | GetByTopicKey |
| GET | `/api/v1/memories/{id}` | Get |
| POST | `/api/v1/memories/{id}/archive` | Archive |
| POST | `/api/v1/decisions` | Record |
| GET | `/api/v1/decisions/{id}` | Get |
| GET | `/api/v1/decisions/history/{key}` | GetHistory |
| POST | `/api/v1/decisions/{id}/contradict` | Contradict |
| POST | `/api/v1/heuristics` | Create |
| GET | `/api/v1/heuristics/active/{key}` | GetActive |
| GET | `/api/v1/heuristics` | ListByScope |
| POST | `/api/v1/heuristics/{id}/toggle` | Toggle |
| POST | `/api/v1/relations` | Create |
| GET | `/api/v1/relations/from/{id}` | GetFrom |
| GET | `/api/v1/relations/to/{id}` | GetTo |
| POST | `/api/v1/search` | FTS search |
| POST | `/api/v1/search/context` | BuildContext |
| POST | `/api/v1/purge/request` | Request |
| POST | `/api/v1/purge/{id}/execute` | Execute |
| POST | `/api/v1/feedback` | Submit |
| GET | `/health` | Public health check |
| GET | `/ready` | DB ping |

### 2.5 Metadata captured per memory entry (current)

From the `memories` table + `Provenance` struct:

- Scope: `tenant_id`, `project_id`, `repo_id`, `agent_id`, `session_id`, `environment`
- Provenance: `source`, `source_uri`, `ingest_method`, `parent_id`
- Temporal: `valid_from`, `valid_until`, `last_accessed`, `freshness`
- Importance: `importance_score`, `importance_computed_at`, `importance_factors` (JSONB)
- Lifecycle: `status`, `archived_by`, `archive_reason`
- FTS: `fts_language`, `search_vector`, `summary`, `tags[]`, `topic_key`, `content`

### 2.6 Gaps vs V4 section 7 desired metadata for episodic memories

The following fields required by V4 for the archive worker to correlate per-change episodic events are **absent** from the current `memories` schema:

| Missing field | V4 purpose |
|---|---|
| `change_id` | Correlate episodic entries back to a specific SDD change for the worker |
| `phase` (enum typed) | Filter memories by SDD phase (currently only in `tags[]` — untyped, no index) |
| `attempt` (int) | Track which attempt number within a phase produced the memory |
| `event_type` (typed enum) | Machine-readable event classification (vs free-form `source` text) |
| `error_class` | Worker pattern detection for retry-reduction metrics |
| `skill_ids_used` (array) | Which skills were active when the memory was written |

Currently these are partially approximated via `tags[]` (e.g. `["sdd", "apply", "explore"]` written by `persistArtifactsToMemory`), but tags are unindexed free-form strings — not filterable efficiently by the worker and not typed.

---

## 3. PriorContext builder

### 3.1 File path and entry points

There is no dedicated `PriorContext` file or struct. The logic is split across two services:

| Function | File | Line |
|---|---|---|
| `Service.buildPriorContext` (phase service) | `internal/application/phase/service.go` | 857 |
| `RunService.loadPriorContext` (apply service) | `internal/application/apply/run.go` | 844 |
| `RunService.refreshApplyProgress` (apply service) | `internal/application/apply/run.go` | ~805 |

### 3.2 How it gathers context today

**Phase service path** (`buildPriorContext`, line 857):
- Calls `memory.BuildContext(ctx, ContextRequest{Scope: {ProjectID, TenantID}, MaxTokens: 4000})`.
- No `AgentID` or `SessionID` in scope — intentionally project-wide (comment at line 866).
- Returns concatenated `rec.Content` from all `bundle.Sections[].Records[]`.
- No source attribution headers. No per-section type labeling.
- Called at line 355; result passed to `PromptInput.PriorContext` at line 371.

**Apply service path** (`loadPriorContext`, line 844):
- Reads `GetByTopicKey` for `sdd/{change.Name}/spec` and `sdd/{change.Name}/design`.
- Concatenates with `## spec (sdd/...)` and `## design (sdd/...)` section headers.
- Returns `""` when neither is found (ErrNotFound is non-fatal).
- Also augments with `refreshApplyProgress` which reads `sdd/{change.Name}/apply-progress`.

### 3.3 Interaction with persist_artifacts.go

File: `internal/application/phase/persist_artifacts.go`

- Called after each phase completes (`persistArtifactsToMemory`).
- Writes the FULL envelope JSON as content, `type="semantic"`, tags `["sdd", "{phase}", "{ref.Type}"]`.
- `topic_key` = whatever the LLM declared in `artifacts_saved[].topic_key`.
- Scope: `{ProjectID, TenantID, AgentID:"sophia-orchestator", SessionID:change.ID}`.
- The apply-phase `loadPriorContext` reads back these records by topic_key.

### 3.4 Where it gets injected

- Phase service: `PromptInput.PriorContext` field at `internal/application/phase/service.go:371`.
- Apply team-lead: `PriorContext` field in `discipline.PromptInput` at `internal/application/apply/teamlead.go:389,488`.
- `PromptBuilder.Build` writes it as `# Prior Context\n{content}\n\n` at `internal/application/discipline/prompt_builder.go:104-108`.

### 3.5 Current shape of PriorContext

There is **no PriorContext struct**. It is a plain `string` passed through `PromptInput.PriorContext`. The string is either:
- The concatenated content from `memory.BuildContext` (phase service path), or
- The manually assembled `## spec ... ## design ...` sections (apply path).

No token budget enforcement. No source attribution headers on individual blocks. No layered assembly protocol.

### 3.6 Gaps vs V4 section 12 desired capabilities

| Capability | V4 target | Current state |
|---|---|---|
| Layer 2: active skills | Skills filtered by SkillMatcher | Skills injected BEFORE Prior Context in prompt, but not inside it — separate field. Skills always `status=active` (no filter today since lifecycle columns don't exist) |
| Layer 3: episodic search | FTS on `error_class`/`phase` | No episodic FTS in PriorContext path; `buildPriorContext` calls `BuildContext` which is project-wide semantic retrieval |
| Layer 4: change digests | `change_digest` per project_id | Not implemented; no `change_digest` table or concept exists |
| Layer 5: business rules / ADRs | Memory-engine semantic | `buildPriorContext` does call `BuildContext` (semantic) but with no query — returns generic project context, not change-specific |
| Layer 6: deterministic routines | Graphify, Context7, LSP, git diff | Not implemented |
| Layer 7: Engram auxiliary | Opt-in | Not implemented |
| Token budget per layer | Configurable, enforced | Only `MaxTokens: 4000` globally on `BuildContext`; no per-layer budget |
| Source attribution headers | `## Skill: name v1 (active, source=...)` | No headers on retrieved memory blocks; apply path has minimal `## spec/## design` headers only |
| SkillMatcher integration | `SkillsForContext(SkillQuery)` | Not implemented; today `SkillsForPhase(phase)` only |

---

## 4. cmd/workers stub

### 4.1 File path

Orchestator: `cmd/workers/` directory does **not exist**. There is no workers binary in sophia-orchestator. The V4 strategy places the archive consolidation worker at `sophia-orchestator/cmd/workers/main.go` — this path does not exist yet.

Memory-engine has its own workers stub at `cmd/workers/main.go`:

```go
package main

func main() {
    // TODO: initialize and start background workers
}
```

This is the memory-engine workers stub, not the orchestator's archive consolidation worker.

### 4.2 Any wiring in main.go for background jobs

`cmd/sophia-orchestator/main.go` — no background job wiring. Only `bootstrap.Wire()` and HTTP server start.

### 4.3 Event subscription mechanism and event types

The orchestator uses an in-process SSE event stream (`internal/application/eventstream`). Events are published via `publishEvent` in the phase/apply services. There is **no pub/sub or message broker** — events are dispatched to SSE clients only, not to worker queues.

Defined event types (`internal/ports/inbound/event_types.go`):

| Event | When |
|---|---|
| `phase.started` | Phase moves to running |
| `phase.completed` | Phase reaches DONE |
| `phase.completed_with_concerns` | Phase DONE_WITH_CONCERNS |
| `phase.failed` | Phase BLOCKED or envelope error |
| `phase.needs_context` | Phase NEEDS_CONTEXT |
| `approval.required` | Sensitive phase paused |
| `approval.resolved` | /approve or /reject |
| `governance.decision` | Governance verdict |
| `agent.dispatched` | Dispatcher invoked |
| `agent.envelope.received` | Agent output parsed |
| `apply.board.created` | Apply board persisted |
| `apply.board.save_failed` | Post-completion SaveBoard error |
| `apply.worktree.error` | createWorktrees failure |
| `apply.group.completed` | Group all tasks success |
| `apply.group.failed` | Group has BLOCKED task |
| `apply.group.degraded` | Group continues with failed dependency |
| `apply.team_lead.spawned` | Team-lead session created |
| `apply.implement.spawn_failed` | Implement session construction failed |
| `apply.implement.spawn_governor_error` | SpawnGovernor refused |
| `apply.task.claimed` | AtomicClaimTask succeeded |
| `apply.task.claim_skipped` | Task already owned |
| `apply.task.escalated` | 3rd consecutive implement failure |
| `apply.task.retry` | Non-final implement retry |
| `apply.provider.quota_exceeded` | HTTP 429 from provider |
| `apply.provider.fallback_used` | Fallback model succeeded |
| `apply.phase.quota_aborted` | Circuit breaker tripped |
| `apply.dispatch.error` | Transport-level error |
| `apply.envelope.validation_failed` | Envelope schema invalid |
| `runtime.dispatch_failed` | Agent CLI not executed |
| `apply.build.started` | Build command starting |
| `apply.build.passed` | Build exit code 0 |
| `apply.build.failed` | Build exit non-zero |
| `apply.materialize.started` | Copying successful worktrees |
| `apply.materialize.completed` | Materialize pass done |
| `apply.materialize.error` | Per-group copy failure |
| `memory.artifact_persist_failed` | Ingest to memory-engine failed |

**Notably absent**: `phase.archived` event. The archive phase completion is `phase.completed` (same as all other phases). V4 section 11 requires the worker to subscribe to `phase.archived` — this named event does not exist today. The worker would need to filter `phase.completed` events where `phase_type == archive`.

---

## 5. sophia-agent-mcp provider registration

### 5.1 File paths

| Artifact | Path |
|---|---|
| Config example | `configs/example.toml` |
| Provider arg builders | `internal/adapters/outbound/subprocess/providers.go` |
| Bootstrap / wire | `internal/bootstrap/wire.go` |
| Runner | `internal/adapters/outbound/subprocess/runner.go` |

### 5.2 Currently registered providers

Four providers registered in code (`internal/adapters/outbound/subprocess/providers.go`):

- `opencode` — primary; `attach-spawn` mode (long-lived `opencode serve` child)
- `claude` — subprocess per dispatch
- `codex` — subprocess per dispatch (argv TBD, M4)
- `gemini` — subprocess per dispatch (argv TBD, M4)

Config-level allowlist (`configs/example.toml:54`):
```
provider_allowlist = ["opencode", "claude", "codex", "gemini"]
```

### 5.3 Allowlist mechanism (how tools_allowed is enforced)

The agent-mcp does NOT implement a `tools_allowed` field. The allowlist concept operates at two levels:

1. **Provider allowlist** (`provider_allowlist` in config): any `agent.run` call with a provider not in this list is rejected before subprocess creation.
2. **CWD allowlist** (`cwd_allowlist` + `cwd_prefix_allowlist`): callers must supply a `cwd` matching an exact or prefix-matched path.
3. **Env allowlist** (`env_allowlist`): only named env vars are forwarded to subprocess.

There is no MCP `tools_allowed` per-provider config field. The V4 strategy document describes a `tools_allowed` list for the Graphify provider (section 7-ter.6) — **this capability does not exist in the current MCP bridge config format**.

### 5.4 Lifecycle model

Current: `opencode` uses `attach-spawn` (one persistent `opencode serve` supervised process). All other providers use `run-per-dispatch` (fresh subprocess per call).

V4 section 7-ter.6 specifies Graphify as `lifecycle: spawned_per_change` — a per-change lifecycle that does not map to either current mode.

### 5.5 How a Graphify MCP provider would be added

V4 target config shape (from strategy doc section 7-ter.6):

```yaml
mcp_providers:
  - id: graphify
    command: graphify serve graphify-out/graph.json
    transport: stdio
    tools_allowed: [query_graph, get_node, get_neighbors, get_community, god_nodes, graph_stats, get_pr_impact]
    lifecycle: spawned_per_change
```

Current gaps to support this:
- `mcp_providers` list config key does not exist (only `[provider.opencode]` TOML structure).
- `transport: stdio` is not a current mode (current is HTTP for opencode attach-spawn).
- `tools_allowed` per-provider filtering not implemented.
- `lifecycle: spawned_per_change` not implemented.

Adding Graphify requires: new `mcp_providers[]` config section parser, stdio transport support in the runner, per-provider `tools_allowed` filter middleware, and per-change lifecycle management in the supervisor.

---

## 6. sophia-cli bootstrap (relevant for INIT)

### 6.1 File path

`internal/adapters/inbound/cli/init.go`

### 6.2 What it does today

The `sophia init` command:
- Accepts flags: `--project` (required), `--base-ref` (default `main`), `--artifact-store` (default `memory-engine`), `--force`.
- Calls `application.InitInput{Project, BaseRef, ArtifactStoreMode, Force}` via `d.Initializer.Run(...)`.
- Writes `.sophia.yaml` at the resolved repo root.
- Prints: `wrote <path> (project=<project>, base_ref=<ref>)`.

It does **not** detect Python, does not spawn Graphify, does not run the Sophia structural detector, and does not persist a `StructuralContext` to memory-engine.

### 6.3 Where Python detection would land

Python detection (V4 section 7-ter.7) would be added to the `InitInput` processing path, likely in the application layer that `d.Initializer.Run` calls. The CLI `init.go` itself would not change — only an optional `--no-graphify` flag and degraded-mode output messaging would need to be added.

---

## 7. agent-governance-core integration points

### 7.1 File paths

| Artifact | Path |
|---|---|
| Governance outbound port | `internal/ports/outbound/governance.go` |
| HTTP governance client | `internal/adapters/outbound/governance/client.go` |

### 7.2 Current contracts

The orchestator has a `GovernanceClient` port with three methods:

```go
EvaluatePhase(ctx, EvaluatePhaseInput) (*GovernanceDecision, error)
AwaitApproval(ctx, changeID, phaseID) error
EvaluateSensitiveAction(ctx, changeID, capability string, payload []byte) (*GovernanceDecision, error)
```

Decision outcomes implemented: `allow`, `allow_with_constraints`, `require_approval`, `deny`.

**Nothing resembling `SkillActivationProposal` or `SkillActivationDecision` exists**. The governance integration today covers only phase transition decisions and sensitive runtime capabilities. The skill lifecycle governance path (V4 section 9) is entirely absent.

### 7.3 Transport

HTTP via `internal/adapters/outbound/http_base`. Endpoints:
- `POST /governance/v1/decisions/phase`
- `POST /governance/v1/decisions/sensitive`
- `GET /governance/v1/approvals/{cid}/{pid}/status` (polling)

No message bus or event queue. Approval is synchronous polling with configurable `ApprovalPollEvery` (5s default) and `ApprovalMaxWait` (30min default).

---

## Gap matrix (the actionable output)

| Surface | V4 target | Current state | Gap | Milestone |
|---|---|---|---|---|
| `skills` schema | `status, version, scope JSONB, applies_when JSONB, risk_level, activation_source, metrics JSONB, last_used_at, last_validated_at` | Only `id, name, phases[], content, techniques[], created_at, updated_at` | Migration 010: 9 new columns + 3 CHECK constraints + 3 indexes; UNIQUE(name) → UNIQUE(name,version) | M1 |
| Seeds backfill | `status='active', version='v1', activation_source='legacy_seed', scope={phases:[phase]}, applies_when={}, metrics={}` | Seeds exist as active rows but lack all lifecycle columns | One-time backfill UPDATE after migration 010; idempotent | M1 |
| `SkillMatcher` | `SkillsForContext(SkillQuery)` — filters by scope, applies_when, status='active', token budget | `SkillsForPhase(phase)` — no status filter, no scope, no applies_when | New port + adapter; `SkillsForPhase` becomes deprecated wrapper | M1 |
| `Upsert` callers | At least 1 non-test caller (archive worker) | 0 non-test callers | Wired in archive worker | M2 |
| `cmd/workers/main.go` | Archive consolidation worker: reads phase-archived events, generates change_digest, creates candidate skills, updates metrics, emits governance proposals | Does not exist in orchestator | Full implementation | M2 |
| `phase.archived` event | Trigger for archive consolidation worker | Does not exist; archive is `phase.completed` with phase_type=archive | Add `EventPhaseArchived = "phase.archived"` constant; emit from service when `phase.Type() == archive` | M2 |
| `change_digest` | Deterministic YAML from envelope.status, attempts, retries, verify_report, skills_used | Not implemented | New domain type + deterministic generator + persist to memory-engine | M2 |
| Skill metrics tracking | `skills.metrics JSONB` updated per change outcome | Not implemented | Archive worker writes metrics; M1 schema adds column | M2 |
| `SkillActivationProposal` | Governance proposal struct emitted by worker | Not implemented | New type in governance port; HTTP call to governance | M2 |
| PriorContext layered assembly | 7 layers: identity, active skills, episodic, digests, ADRs, routines, Engram | Single-layer: `BuildContext(project-wide)` in phase service; `spec+design` by topic-key in apply | Full PriorContext builder with layer composition, token budget per layer, source attribution | M3 |
| Episodic FTS in PriorContext | FTS by `error_class`, `phase` from memory-engine | No episodic FTS in PriorContext path | Requires `change_id`, `phase`, `error_class` columns in `memories` table first | M3 |
| Source attribution headers | `## Skill: name v1 (active, source=archive_worker, project=*)` per injected block | No headers on memory blocks; only minimal `## spec/## design` in apply path | Add rendering in `PriorContext` builder | M3 |
| Token budget per layer | Configurable budget; skills ordered by risk/usage; top-K episodic; top-3 digests | Global `MaxTokens: 4000` on `BuildContext` only | Per-layer budget config + enforcement in PriorContext builder | M3 |
| Deterministic routines in PriorContext | Graphify summary, Context7 docs, LSP, git diff | Not implemented | Requires Graphify + detector (M-KNOW-INIT-0) to land first | M3 |
| Graphify MCP provider | `mcp_providers[]` list with `id, command, transport, tools_allowed, lifecycle` | Not implemented; `sophia-agent-mcp` has flat `[provider.opencode]` structure only | New config section; stdio transport; tools_allowed filter; per-change lifecycle | M-KNOW-INIT-0 |
| `StructuralContext` struct | Go struct: Languages, Frameworks, PackageManagers, ArchStyle, GraphSummary, AffectedModules, ConventionHints | Does not exist | New type + Sophia structural detector module | M-KNOW-INIT-0 |
| CLI init Python detection | Detect Python 3.10+; warn/degrade if absent; optionally bootstrap via uv | Not implemented | Add to `d.Initializer.Run` in sophia-cli | M-KNOW-INIT-0 |
| `memories` table: typed episodic metadata | `change_id, phase (enum), attempt, event_type, error_class, skill_ids_used[]` | Absent; approximated via unindexed `tags[]` | Memory-engine migration 005: new columns for episodic metadata | M2 (blocks episodic FTS in M3) |
| `memory_quarantine` table | Secret scan + prompt injection guard + quarantine | Not implemented | Memory-engine migration N; ingest guard in security service | M6 |
| FTS language | `'english'` for code/technical content | Hardcoded `'spanish'` in `memory.go:119` and FTS queries | **Risk**: all FTS ranking is wrong for code artifacts written in English; requires migration to make `fts_language` dynamic per-tenant | Pre-M1 |

---

## Risks discovered during inventory

### R1 — FTS language hardcoded to 'spanish' (CRITICAL for M2/M3)

File: `internal/domain/memory/memory.go:119` and `internal/adapters/outbound/search/postgres_fts.go:112-118`.

All SDD phase artifacts (spec, design, apply envelopes) are written in English. The FTS index uses `to_tsvector('spanish', ...)` and `plainto_tsquery('spanish', ...)`. Spanish stemming on English technical content degrades ts_rank scores unpredictably — the episodic FTS path that V4 relies on in PriorContext and the archive worker will produce low-quality results or miss matches entirely. Fixing requires: migration to change `fts_language` per-row, or a default override, and reindexing existing rows.

### R2 — `phase.archived` event does not exist (blocks M2 worker trigger)

The archive consolidation worker subscribes to `phase.archived` (V4 section 11.1). This event name does not exist in `event_types.go`. The archive phase completes as `phase.completed`. Adding the event is low-effort but must precede worker implementation.

### R3 — No `change_id` or `phase` column in `memories` table (blocks M2 pattern detection)

The archive worker must correlate episodic memory entries back to specific changes and phases. Today this information is in `tags[]` as unindexed strings — not filterable efficiently. A memory-engine migration is required before the worker can query "all episodic records for change_id X, phase apply, attempt 2".

### R4 — `Upsert` has zero non-test callers (M1 acceptance criterion at risk)

The M1 acceptance criterion requires "Upsert has at least 1 caller non-test". Currently `Upsert` exists in the repo but is only exercised via integration tests. The archive worker (M2) would be the natural caller. This means the M1 criterion is technically met only if seed bootstrap is changed to use `Upsert` rather than `InsertIfAbsent`, or if a preliminary wire is added.

### R5 — sophia-agent-mcp lacks `mcp_providers[]` config and `tools_allowed` mechanism

The V4 Graphify integration requires a `mcp_providers` list config section, per-provider `tools_allowed` filter, and `spawned_per_change` lifecycle. None of these exist. Adding Graphify requires non-trivial work in sophia-agent-mcp before the INIT phase can use the graph MCP tools. This is a dependency for M-KNOW-INIT-0.

### R6 — `SkillsForPhase` in apply service uses skill domain without status filter

`teamlead.go:587` and the phase `service.go:361` call `SkillsForPhase` which delegates to `FindByPhase` (SQL: `WHERE $1 = ANY(phases)`). After migration 010 adds the `status` column, the `FindByPhase` query must be updated to add `AND status = 'active'` or skills in `deprecated`/`blocked`/`candidate` state will be injected into prompts. This must be caught during M1 implementation.

### R7 — PriorContext is a plain string with no struct contract

V4 requires layered assembly, token budgets, and source attribution per block. The current implementation is a raw string built inline in `service.go:857` and `run.go:844`. Refactoring to a structured builder without breaking existing behavior requires careful layering. The V4 `PriorContext struct` with source attribution does not exist — it needs to be designed as part of M3.
