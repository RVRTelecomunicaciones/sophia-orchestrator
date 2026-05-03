---
title: Sophia Orchestrator — V1 Design
date: 2026-05-03
status: Draft (awaiting user review)
version: 1.0.0-spec
authors:
  - rfactperu@gmail.com
related:
  - sophia-runtime-adapters/docs/superpowers/specs/2026-04-19-runtime-adapters-phase1-design.md
  - agent-governance-core/docs/architecture.md
  - sophia-memory-engine/docs/architecture.md
research:
  - Gentleman-Programming/gentle-ai
  - lleontor705/cortex-ia
  - lleontor705/cli-orchestrator-mcp
  - obra/superpowers (v5.0.7)
---

# Sophia Orchestrator — V1 Design

## Executive Summary

`sophia-orchestator` is the deterministic coordinator of the SDD (Spec-Driven Development) workflow within the Sophia ecosystem. It is a Go HTTP service that drives a SDD Change through nine canonical phases, enforcing envelope contracts, Iron Laws, and HARD-GATE markers between agent-IA invocations. It does not decide policy (governance does), does not store knowledge (memory-engine does), and does not execute side effects (runtime-adapters does). Its sole responsibility is **coordination with discipline**.

V1 ships SDD-only workflows, OpenCode as the AI dispatcher (pluggable adapter), parallel apply phase coordination via runtime-adapters Phase 2 primitives (mailbox, locks, reservations) plus git worktrees, and a 202 Accepted + SSE pattern for long-running phases.

The design adopts patterns validated by Cursor's "Scaling Agents" architecture (Planner/Worker/Judge), cortex-ia's apply-phase board, and obra/superpowers' discipline patterns (Iron Laws, HARD-GATE, status enums). Industry verification (May 2026) corrected initial defaults: parallelism dropped from 2×3=6 to 2×2=4 due to Anthropic's undocumented bulk-spawn rate limiter (issue #53922), and request-thread was replaced with 202+SSE due to Cloudflare's 100s timeout.

## Glossary

| Term | Definition |
|---|---|
| **Change** | A SDD unit of work with a name, project, and 9-phase lifecycle. Aggregate root. |
| **Phase** | One execution of a SDD phase (init/explore/proposal/spec/design/tasks/apply/verify/archive). |
| **Envelope** | The JSON contract every agent returns at end of phase: status, confidence, artifacts, risks. |
| **Apply Board** | Sub-aggregate of Phase apply: groups + tasks coordinated by team-leads. |
| **Group** | A set of tasks within apply that can run together; may depend on other groups. |
| **Task** | A single coding work item within a group, with file patterns and acceptance criteria. |
| **Team-lead** | An AgentSession (role=team-lead) coordinating implements within one group. |
| **Implement** | An AgentSession (role=implement) writing code for one task in its own worktree. |
| **AgentSession** | One invocation of an AI CLI subprocess (OpenCode/Claude/etc.) with a prompt and worktree. |
| **Iron Law** | A non-rationalizable invariant the orchestrator MUST enforce (5 total). |
| **HARD-GATE** | A marker injected into agent prompts that the agent MUST NOT cross unconditionally. |
| **Spawn Governor** | The component enforcing concurrent process limits + stagger+jitter + rate-limit awareness. |
| **Dispatcher** | Pluggable adapter for invoking AI CLIs (OpenCode V1, Claude/Cursor/Gemini V2). |

## 1. Boundaries + Iron Laws

### 1.1. What sophia-orchestator IS

- A Go 1.26+ HTTP service (chi/v5 router, OTEL, Postgres 15+) that owns SDD Change workflow state.
- The orchestrator of the SDD 9-phase canonical lifecycle:
  ```
  init → explore → proposal → spec → design → tasks → apply → verify → archive
                                \      /
                                 (concurrent: both read proposal)
  ```
- A client of governance / memory-engine / runtime-adapters via HTTP outbound ports.
- A dispatcher of AI agents via pluggable Dispatcher port (V1 implementation: OpenCode subprocess via runtime `shell.exec@v1`).
- An injector of disciplined prompts (Iron Laws + HARD-GATE markers + envelope schema).
- An emitter of SSE progress events for long-running phases.

### 1.2. What sophia-orchestator is NOT

- Not a memory engine (memory-engine handles episodic/semantic/decision-ledger).
- Not a policy/approval engine (governance handles routing/policy/approval).
- Not a side-effect executor (runtime-adapters handles shell/git/fs/http and Phase 2 locks/mailbox).
- Not an AI provider (LLM calls happen inside OpenCode subprocess; orchestrator never invokes Anthropic/OpenAI APIs directly).
- Not a generic workflow builder (V1 = SDD only; no runtime-configurable DAGs).
- Not a distributed task scheduler (no Temporal/Celery/Sidekiq in V1; goroutines + Postgres advisory locks).
- Not a skill/agent generator (skills live upstream in superpowers and project conventions; orchestrator only injects them).

### 1.3. Envelope Contract (mandatory per phase)

Every phase agent returns a JSON envelope. Orchestrator validates it before persisting and transitioning.

```json
{
  "schema_version": "v1",
  "phase": "spec",
  "change_name": "user-auth-refresh",
  "project": "ms-cotizacion",
  "status": "DONE | DONE_WITH_CONCERNS | BLOCKED | NEEDS_CONTEXT",
  "confidence": 0.85,
  "executive_summary": "...",
  "artifacts_saved": [{"topic_key": "sdd/user-auth-refresh/spec", "type": "spec"}],
  "next_recommended": ["design", "tasks"],
  "risks": [{"description": "...", "level": "low|medium|high"}],
  "data": {}
}
```

Status enum is borrowed from obra/superpowers `subagent-driven-development` skill. It captures the four real outcomes better than a binary success/failure.

### 1.4. Confidence Thresholds (gating)

Below threshold ⇒ status forced to `DONE_WITH_CONCERNS` or `BLOCKED`; phase does NOT auto-transition.

| Phase | Min confidence |
|---|---|
| explore | 0.5 |
| proposal | 0.7 |
| spec | 0.8 |
| design | 0.7 |
| tasks | 0.8 |
| apply | 0.6 |
| verify | 0.9 |
| archive | 0.9 |

### 1.5. The Five Iron Laws

Iron Laws are non-rationalizable invariants. Orchestrator enforces them at the boundary of every transition. Anti-rationalization tables live alongside (see Appendix A).

1. **NO PHASE TRANSITION WITHOUT PERSISTED ENVELOPE** — extends `A4.3 persistence-before-return` from runtime-adapters to the phase level. The envelope MUST be persisted in Postgres before any caller-visible state change.
2. **NO APPLY WITHOUT TASKS APPROVED** — apply phase requires Phase tasks DONE with confidence ≥ 0.8 and explicit approval bit set.
3. **NO ARCHIVE WITHOUT VERIFY DONE** — archive requires Phase verify DONE with confidence ≥ 0.9.
4. **NO RUNTIME CALL WITHOUT GOVERNANCE DECISION** — every phase transition (and every sensitive runtime capability invocation) goes through governance policy/routing first. Mirror of runtime's `R1`.
5. **NO FIX #4 WITHOUT ARCHITECTURAL ESCALATION** — if any Apply task fails 3 consecutive times, the orchestrator marks status=BLOCKED and emits an architectural-escalation event; it does NOT attempt fix #4. Borrowed from obra/superpowers `systematic-debugging`.

## 2. Architecture

Same hexagonal/clean architecture as governance, memory-engine, and runtime-adapters. Dependency arrow points inward.

```
┌────────────────────────────────────────────────────────────────────┐
│ adapters/inbound  (http, sdk, [mcp v2])                            │
│                          │                                         │
│                          ▼                                         │
│ ports/inbound  (ChangeService, PhaseService, ApplyService,         │
│                  EventStreamService)                               │
│                          │                                         │
│                          ▼                                         │
│ application/services  (one use case per command, transactional)    │
│                          │                                         │
│                          ▼                                         │
│ ports/outbound  (GovernanceClient, MemoryClient, RuntimeClient,    │
│                  AgentDispatcher, ChangeRepository,                │
│                  BoardRepository, ArtifactStore, AuditLog)         │
│                          │                                         │
│      ┌───────────────────┼─────────────────────┐                   │
│      ▼                   ▼                     ▼                   │
│ adapters/outbound   pg                  http-clients               │
│ (engram, openspec,  (changes, phases,   (governance, memory,       │
│  hybrid stores;     boards, tasks,      runtime; CB per-target)    │
│  opencode-disp,     sessions,                                      │
│  worktree-mgr)      worktrees, audit)                              │
└────────────────────────────────────────────────────────────────────┘

         domain/  (aggregates: Change, Phase, ApplyBoard, Group,
                  Task, AgentSession, Worktree, Envelope, IronLaw)
         ▲     ▲     ▲                                  ▲
         │     │     │                                  │
         └─────┴─────┴──── consumed by everything above
```

**Composition rule**: `internal/bootstrap/wire.go` is the ONLY file that imports concrete adapter and infrastructure implementations. Domain and application packages NEVER import adapters/infrastructure. Violation = build-time architecture error (golangci-lint + `forbidigo` rule). Mirror of runtime-adapters R0.

### 2.1. Bounded Contexts

| Context | Responsibility |
|---|---|
| **Change** | SDD Change aggregate root: lifecycle, current phase pointer, status, artifact-store mode |
| **Phase** | One phase execution: envelope, status, confidence, retry budget, attempts |
| **Apply** | Sub-aggregate of Phase apply: Board, Groups, Tasks, claim/release semantics |
| **AgentDispatch** | A single AI CLI invocation: prompt, worktree, captured envelope, exit code |
| **Worktree** | Lifecycle of a git worktree: create, lock, release, cleanup |
| **Artifact** | Abstraction over engram / openspec / hybrid; topic-key resolution |
| **Discipline** | Iron Laws + HARD-GATE injection + envelope validation + Spawn Governor |
| **Audit** | Append-only trail of every transition, mirrored to memory-engine ledger |

### 2.2. Tech Stack (mirrored from runtime-adapters for consistency)

- Go 1.26+ (toolchain pinned `go1.26.2`)
- HTTP router: `chi/v5`
- DB: PostgreSQL 15+ via `pgx/v5`
- Migrations: `golang-migrate`
- Observability: OpenTelemetry (traces + metrics) + slog (structured logs)
- Testing: `testify` + `testcontainers-go`
- Lint: `golangci-lint` (with `forbidigo`, `wrapcheck`, `bodyclose`, `errorlint`)
- Resilience: custom circuit breaker per outbound target (governance, memory, runtime, dispatcher) — separate thresholds for hard failures (3) and timeouts (5), half-open with 1 success → close. Pattern lifted from `cli-orchestrator-mcp`.

### 2.3. Process Layout V1

- `cmd/sophia-orchestator` — main HTTP service binary
- (V1.1) `cmd/sophia-orchestator-worker` — optional dedicated worker for long apply phases

V1 runs phase execution in goroutines spawned by the HTTP handler. SSE streams progress to the caller. Postgres advisory locks coordinate across goroutines. No external queue.

## 3. Domain Model

```go
// Change aggregate root
type Change struct {
    ID            ChangeID
    Name          ChangeName            // unique per project (slug)
    Project       ProjectName
    Status        ChangeStatus          // active | completed | aborted
    CurrentPhase  PhaseType
    ArtifactStore ArtifactStoreMode     // engram | openspec | hybrid | none
    Phases        map[PhaseType]*Phase
    CreatedAt     time.Time
    UpdatedAt     time.Time
}

// Phase
type Phase struct {
    ID            PhaseID
    ChangeID      ChangeID
    Type          PhaseType             // init | explore | ... | archive
    Status        PhaseStatus           // pending | running | done | done_with_concerns | blocked | needs_context | interrupted
    Envelope      *Envelope             // populated on done/blocked
    Confidence    float64
    RetryBudget   int
    Attempts      int
    StartedAt     *time.Time
    CompletedAt   *time.Time
    Sessions      []AgentSessionID
}

// ApplyBoard (only when Phase.Type == apply)
type ApplyBoard struct {
    ID        BoardID
    PhaseID   PhaseID
    Groups    []*Group
    Status    BoardStatus               // building | running | completed | failed
    CreatedAt time.Time
}

type Group struct {
    ID            GroupID
    BoardID       BoardID
    Name          string
    DependsOn     []GroupID
    Tasks         []*Task
    Status        GroupStatus           // pending | running | completed | failed
    WorktreePath  string
    BranchName    string
}

type Task struct {
    ID            TaskID
    GroupID       GroupID
    Description   string
    FilesPattern  []string              // glob patterns for file_reserve
    Status        TaskStatus            // pending | claimed | running | done | failed | blocked
    ClaimedBy     *AgentSessionID
    Attempts      int                   // counts toward Iron Law #5 escalation
    Envelope      *Envelope
}

// AgentSession unified with role discriminator
type AgentSession struct {
    ID           SessionID
    ChangeID     ChangeID
    PhaseID      PhaseID
    AgentRole    string                 // sdd-explore | sdd-proposal | ... | team-lead | implement
    Provider     string                 // opencode | claude-code | ...
    Worktree     *WorktreeID
    Command      string
    PromptSHA256 string                 // dedup + audit
    Status       SessionStatus          // pending | running | done | failed | timeout
    ExitCode     *int
    Envelope     *Envelope
    StartedAt    time.Time
    EndedAt      *time.Time
}

type Worktree struct {
    ID         WorktreeID
    SessionID  *SessionID
    Path       string
    Branch     string
    Status     WorktreeStatus           // created | locked | released | cleaned
    CreatedAt  time.Time
    CleanedAt  *time.Time
}

// Envelope value object (immutable)
type Envelope struct {
    SchemaVersion    string             // "v1"
    Phase            PhaseType
    ChangeName       ChangeName
    Project          ProjectName
    Status           PhaseStatus
    Confidence       float64
    ExecutiveSummary string
    ArtifactsSaved   []ArtifactRef
    NextRecommended  []PhaseType
    Risks            []Risk
    Data             json.RawMessage
}
```

IDs use ULIDs (consistency with runtime-adapters). `change_id+phase_type+attempts` is unique and is the idempotency key (replay-everything semantics).

## 4. Phase Execution Flow (single-agent phases)

For phases that are NOT apply (i.e., 8 of 9 phases), the flow is single-agent and follows 16 deterministic steps.

```
caller (CLI / MCP client / direct HTTP)
   │
   ▼
[1]  POST /api/v1/changes/{id}/phases/{phase_type}/run
[2]  Validate: change exists; phase_type next valid; mutex on (change_id, phase_type) via Postgres SELECT FOR UPDATE
[3]  Create Phase row (status=pending, retry_budget=N)             ← persisted before [4]
[4]  Call governance: ClassifyTask + EvaluatePolicy + RouteDecision (HTTP)
     return: { decision: allow | require_approval | deny, strategy: direct, agent_role }
[5]  Branch:
       require_approval → status=AWAITING_APPROVAL, return 202 with approval_url
       deny             → status=BLOCKED with reason envelope, persist, return 4xx
       allow            → continue
[6]  Discipline pre-flight (5 Iron Laws):
       prior phases DONE with confidence ≥ threshold? (Iron Law #1, transitively)
       no Iron Law violations? (#2, #3, #4, #5)
       retry_budget > 0?
     FAIL → status=BLOCKED, persist, return
[7]  Create AgentSession (worktree=nil unless phase writes code; e.g., spec drafts)
[8]  Discipline.BuildPrompt:
       inject Iron Laws relevant to this phase
       inject HARD-GATE markers ("DO NOT proceed if X")
       pull change context from memory-engine: sdd/{change}/{prior_phase}
       inject task body from request payload
       inject envelope schema (the agent MUST return this JSON)
[9]  RETURN 202 Accepted to caller with { phase_id, events_url }   ← FROM HERE goroutine continues
[10] Goroutine: Dispatcher.Dispatch(prompt) — runtime POST /executions { capability: "shell.exec@v1", payload: { cmd: "opencode run -p '<prompt>'", cwd, timeout: 1800s } }
[11] Runtime returns ExecutionReceipt (success | failure | timeout | cancelled | partial) + stdout + retry_hint
[12] Parse Envelope:
       primary: last fenced ```json block from stdout
       fallback: query memory-engine: GET /observations?topic_key=sdd/{change}/{phase}
       both fail → status=NEEDS_CONTEXT, retry if budget > 0
[13] Discipline.ValidateEnvelope:
       schema_version == "v1"?
       phase matches expected?
       status in enum? confidence ≥ threshold? artifacts_saved present?
     FAIL → status=NEEDS_CONTEXT, retry if budget > 0
[14] Persist Phase: envelope, status, confidence, completed_at  (BEFORE [15] — Iron Law #1)
[15] Update Change.CurrentPhase to next phase (only on DONE + threshold met)
[16] Audit: append to memory-engine ledger + Postgres audit_log
     Emit SSE event phase.completed with envelope
```

**SSE event types** during single-agent phases:
- `phase.started` (after step [3])
- `governance.decision` (after step [4])
- `agent.spawned` (after step [10])
- `agent.envelope.received` (after step [11])
- `phase.validated` (after step [13])
- `phase.completed` or `phase.failed` (after [16])
- `heartbeat` every 5s while running

**Idempotency**: re-POSTing the same `change_id + phase_type` (same attempts counter) returns the cached envelope. To force a retry, bump `attempts` (separate endpoint) or wait for the budget to allow it.

## 5. Apply Phase Parallel Coordination

The apply phase is the only one that parallelizes. Flow has 18 steps. Critical: cortex-ia-style coordination + git worktrees + Spawn Governor + Iron Law #5 escalation.

```
caller: POST /api/v1/changes/{id}/phases/apply/run
   │
   ▼
[1]  Pre-flight (Discipline):
       Phase tasks DONE with tasks-list approved? (Iron Law #2)
       pull tasks list from memory: sdd/{change}/tasks → DAG (groups + deps)
[2]  Create ApplyBoard in Postgres (status=building):
       Groups: parse DAG → independent + dependent
       Tasks: each with files_pattern, description, depends_on (in-group), attempts=0
[3]  Create one git worktree per Group (via runtime git.worktree.create@v1):
       path: /var/sophia/worktrees/{change_id}/group-{group_idx}
       branch: sophia/{change_name}/group-{group_idx}
       base: tip of main (or change.base_ref)
[4]  Board.status=running, persisted
[5]  RETURN 202 Accepted with { phase_id, board_id, events_url } to caller
     ← from here goroutine continues
[6]  Spawn Governor.Acquire for each team-lead BEFORE dispatch (with stagger 200-500ms + jitter ±30%)
[7]  Dispatch ALL team-leads via runtime (one per Group, in same orchestrator turn):
       independent groups: dispatched immediately
       dependent groups:   dispatched immediately too — they self-coordinate via mailbox
       one AgentSession (role=team-lead-{N}, provider=opencode) per group, with worktree assigned
[8]  Each team-lead receives prompt:
       Iron Laws (TDD enforced, "NO TOQUES TASKS DE OTROS GROUPS")
       board_id, group_id, task descriptions + file patterns
       mailbox subscription (poll msg_read_inbox every 30s if depends_on != [])
       worktree path + branch
       envelope schema (must return JSON)
[9]  Team-lead with depends_on != []:
       runtime mailbox.agent_register(team-lead-{N})
       loop poll mailbox.msg_read_inbox every 30s, max 10min:
         if "Group {dep} complete" received → mark dep satisfied
         when all deps satisfied → break
       on timeout: fallback runtime tb.status + dlq.list, escalate if data missing
[10] Team-lead claims tasks from board:
       for each task: runtime tb.claim@v1 (atomic via SELECT FOR UPDATE)
       conflict (other agent claimed) → skip
       record ClaimedBy=session_id
[11] Team-lead launches implements IN PARALLEL within group (Spawn Governor.Acquire each, with stagger):
       one AgentSession (role=implement, provider=opencode) per claimed task
       each implement gets a NESTED worktree (or branch within group worktree)
       prompt: Iron Laws (TDD) + task body + spec/design context
[12] Each implement BEFORE writing:
       runtime lock.acquire(check_only=true, patterns=task.files_pattern) — probe
       overlap with another agent's reservation → BLOCKED, increment attempts
       no overlap → runtime lock.acquire(patterns, agent_id) — actually lock
[13] Implement runs OpenCode subprocess via runtime shell.exec@v1:
       opencode run -p '<implement-prompt>' --cwd <worktree>
       edits files within file_reserve patterns; runs tests (TDD)
       returns Envelope on stdout (status DONE | DONE_WITH_CONCERNS | BLOCKED | NEEDS_CONTEXT)
[14] Implement self-review (Discipline checklist enforced via prompt):
       Completeness: all task acceptance criteria met?
       Quality: tests pass? lint clean?
       Discipline: TDD followed? verification done?
       Testing: actual test runs cited in envelope.data?
     fail self-review → Status=DONE_WITH_CONCERNS
[15] Implement: runtime lock.release(agent_id), commit changes to its branch
[16] Team-lead reviews implement envelope:
       DONE → next task
       DONE_WITH_CONCERNS → mark task with concern, log, continue (caller decides re-review)
       BLOCKED → task.attempts++. attempts == 3 → Iron Law #5: escalate (status=BLOCKED, halt group)
       NEEDS_CONTEXT → team-lead injects more context, re-dispatches implement
     Spawn Governor.Release on each implement completion
[17] When all tasks in group DONE:
       team-lead merges implement-branches → group-worktree (rebase preferred, fallback merge)
       conflicts (theoretically zero by lock.acquire) → BLOCKED + escalate
       persist apply-progress incrementally: memory-engine mem_save sdd/{change}/apply-progress
       team-lead: runtime mailbox.msg_broadcast("Group {N} complete") → unblocks dependent groups
       team-lead returns Envelope to orchestrator
[18] When all groups DONE:
       final merge: group-worktrees → main worktree of change (sequential, conflict resolution per merge strategy)
       persist final Apply Phase envelope (aggregate of all team-lead envelopes)
       cleanup worktrees if cleanup_on_success=true (configurable)
       SSE: phase.completed
     Spawn Governor.Release final
```

### 5.1. Recovery semantics V1

- **Orchestrator crash mid-apply**: on startup, scan Postgres for `Phase.status=running AND Phase.type=apply`. Mark as `INTERRUPTED`. CLI must invoke `POST /resume` to continue. Resume reads board state, re-claims unclaimed tasks (after lock TTL expiry), re-dispatches incomplete team-leads.
- **Team-lead crash**: orchestrator detects via runtime ExecutionReceipt timeout/failure. Mark group as `failed`, attempts++. attempts == 3 → Iron Law #5 escalation.
- **Implement crash**: team-lead's subprocess exit_code != 0 (or runtime timeout). Team-lead retries within group budget. `lock.acquire` has TTL (default 5min) auto-release.

V2 will replace manual resume with automatic startup recovery (1-2 weeks of work; idempotency of side-effects, worktree reconciliation, stale branch handling).

### 5.2. Concurrency model

- One Apply Phase running per Change (mutex via Postgres advisory lock keyed by change_id).
- N team-leads in parallel (one per group, configurable `max_parallel_groups`, default 2).
- M implements in parallel per team-lead (configurable `max_parallel_implements_per_group`, default 2).
- Total parallelism = N×M. **Default V1: 2×2 = 4. Cap: 6.** (Calibrated from issue #53922 evidence.)

## 6. Rate Limiting + Spawn Governor

### 6.1. The threat: undocumented bulk-spawn rate limiter

Anthropic's Claude Code (issue [#53922](https://github.com/anthropics/claude-code/issues/53922), April 2026, still open) imposes a server-side burst limiter that fires at 4-6 concurrent processes with the message "Server is temporarily limiting requests · Rate limited" — distinct from the user's documented rate limit. OpenCode's behavior is **not yet verified**; the V1 default assumes a similar ceiling.

### 6.2. Spawn Governor component

Lives in the **Discipline** bounded context.

Responsibilities:
- Track count of active dispatcher processes via Postgres advisory lock + counter table (`spawn_governor_state` singleton row).
- Apply stagger+jitter at spawn: if ≥ 2 spawns occurred in last 1s, wait `200ms + rand(0..150ms)`.
- Enforce caps:
  - Global cap (`max_concurrent_dispatcher_processes`, default 4, hard ceiling 6).
  - Per-Change cap (`max_concurrent_per_change`, default 4).
  - Per-Provider cap (`max_concurrent_per_provider`, configurable per dispatcher).
- On saturation: callers block (with `ctx.Done()` cancellation) up to `max_wait_ms` (default 30s); after that, fail with HTTP 429 (configurable retry-after).
- Emit metrics: `spawn_governor_active`, `spawn_governor_throttled_total{reason}`, `spawn_governor_wait_ms`.

Interaction:
- Apply phase step [6] and [11] call `SpawnGovernor.Acquire(ctx, provider)` before each `Dispatcher.Dispatch`.
- Dispatcher releases on subprocess exit (success or failure) via `SpawnGovernor.Release`.
- Caps are read from config + per-Change overrides (stored in `changes.config_json`).

### 6.3. Postgres advisory lock vs Redis

V1: Postgres advisory lock. Reasons:
- Zero new infra (Postgres already deployed).
- Transactional, in-memory at PG server, auto-released on connection drop (ideal for crash recovery of the spawn count).
- Sub-millisecond contention (acceptable up to ~100 spawns/min).

V2: Redis evaluation only if multi-instance orchestrator demands cross-process counter ≥ 1000 spawns/min.

## 7. HTTP API Surface V1

All long-running endpoints follow `202 Accepted + SSE`.

### 7.1. Changes

```
POST   /api/v1/changes
  body: { name: string, project: string, artifact_store_mode?: "engram"|"openspec"|"hybrid"|"none", base_ref?: string }
  → 201 { change_id, name, project, current_phase: "init", status: "active", artifact_store_mode }

GET    /api/v1/changes/:id
  → 200 { change_id, name, project, status, current_phase, phases: { [phase_type]: PhaseSummary } }

GET    /api/v1/changes?project=…&status=…
  → 200 { items: [...], next_page_token? }

POST   /api/v1/changes/:id/abort
  body: { reason: string }
  → 200 { change_id, status: "aborted" }
```

### 7.2. Phases

```
POST   /api/v1/changes/:id/phases/:phase_type/run
  body: { task_description?: string, context_overrides?: object, retry_budget?: int }
  → 202 { phase_id, status: "running", events_url, started_at }

GET    /api/v1/changes/:id/phases/:phase_id
  → 200 { phase_id, change_id, type, status, envelope, confidence, attempts, started_at, completed_at }

GET    /api/v1/changes/:id/phases/:phase_id/events
  → SSE stream:
    event types: phase.started | governance.decision | agent.spawned |
                 agent.envelope.received | phase.validated | phase.completed |
                 phase.failed | apply.board.created | apply.group.dispatched |
                 apply.task.claimed | apply.task.completed | apply.group.completed |
                 heartbeat

POST   /api/v1/changes/:id/phases/:phase_id/resume
  → 200 { phase_id, status: "running", events_url }   (V1: manual; V2: auto on startup)
```

### 7.3. Apply-specific

```
GET    /api/v1/changes/:id/phases/:phase_id/board
  → 200 { board_id, status, groups: [{ id, name, status, depends_on, tasks: [...] }] }

GET    /api/v1/changes/:id/phases/:phase_id/board/groups/:group_id
GET    /api/v1/changes/:id/phases/:phase_id/board/tasks/:task_id
```

### 7.4. Approvals (mirrored from governance)

```
POST   /api/v1/changes/:id/phases/:phase_id/approve
  body: { approver: string, reason?: string }
  → 200 { phase_id, status: "running" }

POST   /api/v1/changes/:id/phases/:phase_id/reject
  body: { approver: string, reason: string }
  → 200 { phase_id, status: "blocked" }
```

### 7.5. Operational

```
GET    /api/v1/health     → 200 { status: "ok", version, uptime_s }
GET    /api/v1/ready      → 200 if Postgres reachable, all outbound clients healthy
GET    /metrics           → Prometheus exposition
```

### 7.6. Auth V1

`X-Sophia-API-Key: <key>` header. Keys per project, stored hashed in `api_keys` table. V2: OIDC + workspace tokens.

### 7.7. MCP (V2 only)

Deferred to V2 to keep scope clean. The MCP server will expose tools `sdd_explore`, `sdd_propose`, `sdd_spec`, `sdd_design`, `sdd_tasks`, `sdd_apply`, `sdd_verify`, `sdd_archive` for AI clients to drive the orchestrator without HTTP.

## 8. Persistence Schema (Postgres)

```sql
CREATE TABLE changes (
  id              CHAR(26) PRIMARY KEY,           -- ULID
  name            TEXT NOT NULL,
  project         TEXT NOT NULL,
  status          TEXT NOT NULL,                  -- active | completed | aborted
  current_phase   TEXT,
  artifact_store  TEXT NOT NULL,                  -- engram | openspec | hybrid | none
  config_json     JSONB NOT NULL DEFAULT '{}',    -- per-change overrides (parallelism, thresholds, ...)
  base_ref        TEXT,                           -- optional git ref to branch from
  created_at      TIMESTAMPTZ NOT NULL,
  updated_at      TIMESTAMPTZ NOT NULL,
  UNIQUE (project, name)
);

CREATE TABLE phases (
  id              CHAR(26) PRIMARY KEY,
  change_id       CHAR(26) NOT NULL REFERENCES changes(id),
  phase_type      TEXT NOT NULL,
  status          TEXT NOT NULL,                  -- pending|running|done|done_with_concerns|blocked|needs_context|interrupted
  envelope        JSONB,
  confidence      NUMERIC(3,2),
  retry_budget    INT NOT NULL DEFAULT 3,
  attempts        INT NOT NULL DEFAULT 0,
  started_at      TIMESTAMPTZ,
  completed_at    TIMESTAMPTZ,
  UNIQUE (change_id, phase_type, attempts)        -- idempotency replay-everything
);
CREATE INDEX phases_change_idx ON phases(change_id);
CREATE INDEX phases_status_idx ON phases(status) WHERE status IN ('running','interrupted');

CREATE TABLE apply_boards (
  id              CHAR(26) PRIMARY KEY,
  phase_id        CHAR(26) NOT NULL REFERENCES phases(id) UNIQUE,
  status          TEXT NOT NULL,                  -- building|running|completed|failed
  created_at      TIMESTAMPTZ NOT NULL
);

CREATE TABLE groups (
  id              CHAR(26) PRIMARY KEY,
  board_id        CHAR(26) NOT NULL REFERENCES apply_boards(id),
  name            TEXT NOT NULL,
  depends_on      CHAR(26)[] NOT NULL DEFAULT '{}',
  status          TEXT NOT NULL,                  -- pending|running|completed|failed
  worktree_path   TEXT,
  branch_name     TEXT
);

CREATE TABLE tasks (
  id              CHAR(26) PRIMARY KEY,
  group_id        CHAR(26) NOT NULL REFERENCES groups(id),
  description     TEXT NOT NULL,
  files_pattern   TEXT[] NOT NULL,
  status          TEXT NOT NULL,                  -- pending|claimed|running|done|failed|blocked
  claimed_by      CHAR(26),
  attempts        INT NOT NULL DEFAULT 0,
  envelope        JSONB
);
CREATE INDEX tasks_group_idx ON tasks(group_id);

CREATE TABLE agent_sessions (
  id              CHAR(26) PRIMARY KEY,
  change_id       CHAR(26) NOT NULL REFERENCES changes(id),
  phase_id        CHAR(26) NOT NULL REFERENCES phases(id),
  agent_role      TEXT NOT NULL,                  -- sdd-explore | ... | team-lead | implement
  provider        TEXT NOT NULL,                  -- opencode | claude-code | ...
  worktree_id     CHAR(26),
  prompt_sha256   TEXT NOT NULL,
  command         TEXT NOT NULL,
  status          TEXT NOT NULL,                  -- pending|running|done|failed|timeout
  exit_code       INT,
  envelope        JSONB,
  started_at      TIMESTAMPTZ NOT NULL,
  ended_at        TIMESTAMPTZ
);

CREATE TABLE worktrees (
  id              CHAR(26) PRIMARY KEY,
  session_id      CHAR(26) REFERENCES agent_sessions(id),
  path            TEXT NOT NULL,
  branch          TEXT NOT NULL,
  status          TEXT NOT NULL,                  -- created|locked|released|cleaned
  created_at      TIMESTAMPTZ NOT NULL,
  cleaned_at      TIMESTAMPTZ
);

CREATE TABLE audit_log (
  id              BIGSERIAL PRIMARY KEY,
  change_id       CHAR(26),
  phase_id        CHAR(26),
  session_id      CHAR(26),
  event_type      TEXT NOT NULL,
  payload         JSONB,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX audit_log_change_idx ON audit_log(change_id, created_at DESC);

CREATE TABLE spawn_governor_state (
  id              SMALLINT PRIMARY KEY DEFAULT 1,
  active_count    INT NOT NULL DEFAULT 0,
  max_count       INT NOT NULL DEFAULT 4,
  updated_at      TIMESTAMPTZ NOT NULL,
  CHECK (id = 1)
);

CREATE TABLE api_keys (
  id              CHAR(26) PRIMARY KEY,
  project         TEXT NOT NULL,
  key_hash        TEXT NOT NULL,
  name            TEXT,
  created_at      TIMESTAMPTZ NOT NULL,
  revoked_at      TIMESTAMPTZ
);
CREATE INDEX api_keys_hash_idx ON api_keys(key_hash) WHERE revoked_at IS NULL;
```

Notes:
- ULID stored as `CHAR(26)` (consistent with runtime-adapters).
- `envelope` JSONB validated by application, not by SQL constraint (allows schema evolution).
- `audit_log` mirrored to memory-engine ledger via outbound port (eventual consistency).
- Postgres advisory locks used for: per-change apply mutex (`pg_advisory_xact_lock(hashtext(change_id))`), spawn governor counter sync.

## 9. Observability

### 9.1. Logger

`internal/infrastructure/obs/log/`. slog wrapper, contract-bound. Canonical attributes: `change_id`, `phase_id`, `session_id`, `agent_role`, `provider`, `trace_id`, `span_id`. JSON output to stdout, captured by collector (Loki/Datadog/etc.).

### 9.2. Metrics (OTEL → Prometheus)

Counters:
```
sophia_orchestator_changes_total{project, status}
sophia_orchestator_phases_total{phase_type, status}
sophia_orchestator_agent_sessions_total{role, provider, status}
sophia_orchestator_envelope_validation_failures_total{phase_type, reason}
sophia_orchestator_iron_law_violations_total{law_id}
sophia_orchestator_dispatcher_calls_total{provider, status}
sophia_orchestator_governance_calls_total{op, status}
sophia_orchestator_memory_calls_total{op, status}
sophia_orchestator_runtime_calls_total{capability, status}
sophia_orchestator_spawn_governor_throttled_total{reason}
sophia_orchestator_sse_events_emitted_total{event_type}
sophia_orchestator_apply_groups_total{status}
sophia_orchestator_apply_tasks_total{status}
```

Histograms:
```
sophia_orchestator_phase_duration_ms{phase_type}
sophia_orchestator_phase_confidence{phase_type}
sophia_orchestator_agent_session_duration_ms{role, provider}
sophia_orchestator_apply_task_attempts
sophia_orchestator_spawn_governor_wait_ms
sophia_orchestator_dispatcher_call_duration_ms{provider}
```

Gauges:
```
sophia_orchestator_spawn_governor_active
sophia_orchestator_sse_connections_active
sophia_orchestator_changes_in_flight
sophia_orchestator_phases_running
```

**Cardinality guards** (mirror runtime R16): label whitelist `{phase_type, status, role, provider, op, capability, event_type, law_id, reason, project}`. High-cardinality identifiers (change_id, phase_id, session_id, trace_id) go to logs/exemplars only. `project` capped at 50 unique values; beyond that, sampling.

### 9.3. Traces (OTEL)

Every HTTP handler wrapped in span (chi middleware). Every outbound call wrapped. `trace_id`/`span_id` propagated via `traceparent` (W3C). SSE events include `trace_id` for client correlation.

Span attributes: `phase_type`, `agent_role`, `provider`, `confidence`, `iron_law_violations[]`.

### 9.4. SLOs

| SLO | Objective | Window |
|---|---|---|
| Phase API latency P99 (POST /run → 202) | < 500ms | 7d |
| SSE first-event latency P99 | < 2s | 7d |
| Phase execution success rate | ≥ 95% | 30d |
| Apply phase end-to-end success rate | ≥ 90% | 30d |
| Iron Law violation rate | < 0.1% | 7d |
| Spawn governor throttle ratio | < 5% | 7d |
| Dispatcher availability per provider | ≥ 99.5% | 30d |

Specs in `ops/slo/*.yaml` (Sloth v1). Generates Prometheus rules automatically.

### 9.5. Dashboards (Grafana)

- `sophia-overview` — global health, throughput, error rate, top failing phases
- `sophia-phases` — duration / confidence / status by phase_type
- `sophia-apply` — boards, tasks claimed/done/failed, group dependencies
- `sophia-dispatcher` — calls per provider, latencies, errors, spawn governor activity
- `sophia-audit` — Iron Law violations, validation failures, escalations

### 9.6. Alertmanager (3-tier, mirror runtime)

- T1 (page on-call): availability < SLO, Iron Law violation rate spike, dispatcher down
- T2 (notify Slack): phase success degradation, spawn governor throttle > 10%
- T3 (log only): individual phase failure, retry exhausted, single envelope validation failure

## 10. Testing Strategy

### 10.1. Unit (testify, ≥85% coverage gate)

- **Domain**: 100% (no I/O — easy)
- **Application**: ≥85% (mock outbound ports)
- **Infrastructure**: ≥75%
- Bootstrap excluded.

Critical unit foci:
- `Discipline.ValidateEnvelope` — every failure mode (schema mismatch, status off-enum, confidence below threshold, missing artifacts)
- `Discipline.IronLawCheck` — 5 laws individually
- `Discipline.SpawnGovernor` — concurrency with goroutines + simulated advisory lock
- Phase state machine — every valid/invalid transition
- Apply DAG construction — group dependencies, cycle detection, topological sort

### 10.2. Contract (`internal/adapters/outbound/*/contract_test.go`)

Each outbound HTTP client (governance, memory, runtime, opencode dispatcher) tested against pact-style fakes. Verifies parsing of every documented response shape from each service's OpenAPI.

### 10.3. Integration (testcontainers-go, build tag `integration`)

Postgres 15 container per test class. Migrations applied via `golang-migrate`. HTTP roundtrip via `httptest.NewServer`.

Tests:
- CreateChange + CreatePhase + RunPhase end-to-end (phase mocked)
- Idempotency: two POSTs same key → same envelope
- Concurrent phase creation: two goroutines → one wins, other 409
- Spawn governor under load: 10 goroutines → cap respected

### 10.4. E2E (build tag `e2e`)

Fixture: `test/fixtures/sample-project/` (small Go project). Spin up: orchestrator + Postgres + governance fake + memory fake + runtime fake + opencode fake.

- Full SDD cycle: 9 phases, fixture project, verifies final state in Postgres + memory + filesystem.
- Apply phase: 2 groups parallel, 2 tasks each → 4 implements parallel. Verifies worktrees, locks, branch merges, aggregated envelope.

### 10.5. E2E SSE (build tag `e2e-sse`)

- Spin up orchestrator + fake long-running phase
- SSE client (eventsource lib)
- Asserts: heartbeats every 5s, events in monotonic order, reconnect with `Last-Event-ID` (V2), client disconnect mid-phase doesn't abort server-side, multi-client broadcast

### 10.6. Chaos (build tag `chaos`)

- Kill orchestrator mid-apply → resume API works, Postgres consistent
- Kill at every phase boundary → resume from last checkpoint
- Network partition orchestrator ↔ runtime → circuit breaker fires, metrics correct
- Network partition orchestrator ↔ governance → V1 phase fails BLOCKED; V2 retry queue
- Spawn Governor saturation: 100 goroutines → FIFO order + absolute cap
- Idempotency under chaos: kill during persistence → resume verifies state

### 10.7. Load (k6, `ops/load/`)

- Baseline: 50 phase runs/min sustained, P99 < 500ms
- Burst: 20 concurrent applies → spawn governor effectiveness, throttle ratio
- Soak: 1000 phases / 4h — memory leak / connection leak watch

### 10.8. CI Gates

- Per-package coverage gates: domain 100%, app 85%, infra 75%
- Unit + integration + contract + E2E + E2E-SSE: green required on main
- Chaos: at least one passing test per phase boundary
- Load baseline: doesn't regress (Sloth-based diff)
- `golangci-lint` + `govulncheck` clean
- ADR required when touching an Iron Law or bounded context boundary

## 11. Phasing Roadmap

### V1 — MVP Functional (~8-12 weeks)

Scope:
- All 9 SDD phases
- HTTP API + SSE
- Apply phase parallel coordination (default 2×2=4, cap 6, stagger+jitter)
- Spawn Governor (Postgres advisory lock)
- Manual resume API
- Artifact store: engram default, openspec opt-in
- Worktrees managed by orchestrator via runtime-adapters
- Dispatcher abstraction + OpenCode adapter
- Iron Laws (5) + HARD-GATE injection + envelope validation
- Integration with governance / memory / runtime via HTTP
- OTEL + Prometheus + Grafana baseline (1 overview + 5 dashboards)
- Sloth SLOs + Alertmanager 3-tier
- Tests: unit + contract + integration + E2E + E2E-SSE + chaos basic
- Auth: API key per project
- Docker image + basic helm chart

Exit criteria:
- ✅ E2E test runs all 9 phases on fixture project unattended
- ✅ Apply phase with 4 parallels completes < 30min on medium change (10 files)
- ✅ SLOs defined and met in load baseline
- ✅ Manual resume works after crash in chaos test
- ✅ Documentation: README, ADRs, AGENTS.md, CLAUDE.md, runbooks

### V1.1 — Dedicated Worker Pool (~2 weeks)

- `cmd/sophia-orchestator-worker` separated process
- Asynq vs goroutines+advisory-lock evaluation (ADR)
- Improved chaos test coverage
- SSE: Last-Event-ID resume

### V2 — Production-grade + Multi-AI (~12-16 weeks)

- Auto-resume on startup (idempotency of side-effects, worktrees, stale branches)
- MCP server exposed (`sdd_*` tools)
- Hatchet evaluation vs custom workers (ADR — go-forward decision)
- Runtime capability `ai.cli.run@v1` typed (replaces `shell.exec` for AI dispatch)
- Multi-CLI dispatchers: Claude Code, Cursor, Gemini adapters
- Hybrid artifact store with sync conflict resolution
- Approval workflow extended: per-org rules, queues, delegations
- Auth: OIDC + workspace tokens
- Per-tenant config (parallelism, thresholds, dispatcher provider)

Exit criteria:
- ✅ Auto-resume tested in full chaos suite
- ✅ At least 2 dispatcher providers in production
- ✅ MCP tools used by an external client

### V3 — Multi-tenant + Distributed (~12+ weeks)

- Multi-tenant (workspaces, projects, fine RBAC)
- Distributed orchestrator (multi-instance, leader election Raft or similar)
- Cross-region replication (eventual consistency with audit trail)
- Marketplace for SDD skills/templates
- Public benchmarks vs Cursor / Devin / Aider
- Public API + OAuth2 client credentials
- Self-hosted + SaaS deployment models

Exit criteria:
- ✅ Customer production deployment > 1000 changes/month
- ✅ Multi-region active-active validated

## 12. References & Research

### 12.1. Reference repositories investigated

- **Gentleman-Programming/gentle-ai** — Go ecosystem configurator, marker-based markdown injection pattern, adapter pattern for AI CLIs.
- **lleontor705/cortex-ia** — Go ecosystem configurator with apply-phase pattern (board + parallel team-leads + mailbox + DLQ). Primary inspiration for sophia's apply phase.
- **lleontor705/cli-orchestrator-mcp** — TypeScript MCP server with resilience pipeline (per-provider circuit breaker, global time budget, exponential backoff with jitter). Resilience pattern adopted.
- **obra/superpowers (v5.0.7)** — Plugin for Claude Code/OpenCode/etc. providing 14 skills with discipline enforcement (Iron Laws, HARD-GATE markers, anti-rationalization tables, status enums). Discipline patterns adopted; workflow structure not (sophia uses cortex-ia's 9 phases).

### 12.2. Industry patterns adopted (May 2026 verification)

- **202 Accepted + SSE** — Industry standard for long-running AI orchestration (Cloudflare 100s timeout makes request-thread untenable).
- **Hierarchical pipeline (Planner/Worker/Judge)** — Cursor's "Scaling Agents" architecture; matches team-lead/implement model.
- **Activities-outside-replay-path** — Temporal pattern adapted: LLM calls produce results stored in event log; replay reuses results, doesn't re-call LLM.
- **WAL phase-boundary checkpoints** — Postgres-persisted state at every phase transition; replay possible after crash via manual resume API.
- **Status enum DONE/DONE_WITH_CONCERNS/BLOCKED/NEEDS_CONTEXT** — superpowers' four-state envelope, more useful than binary success/failure.
- **HARD-GATE markers in prompts** — superpowers' anti-rationalization technique for non-skippable invariants.

### 12.3. Industry patterns evaluated and deferred

- **MCP server exposure** — deferred to V2; sophia is a coordinator, not a tool surface for Claude Code clients.
- **Hatchet vs Temporal** — V2 evaluation when scaling beyond single-process orchestrator.
- **LangGraph checkpointer** — checkpointing only between nodes; apply phase as a single "node" makes this a poor fit.
- **Native `claude code -w` worktree flag** — Claude Code-specific; sophia stays AI-provider-agnostic, manages worktrees via runtime-adapters.

## 13. Open Questions / Future ADRs

These are deliberately not closed in V1 and will require ADRs:

1. **OpenCode rate-limiter ceiling** — empirical verification needed. Spawn Governor default (4/cap 6) is a conservative guess based on Claude Code's #53922. ADR after first load test.
2. **OpenCode prompt injection format** — exact CLI flags and stdin/stdout contract for sending prompts and capturing envelopes. Will surface during dispatcher implementation.
3. **Asynq vs goroutines+advisory-lock for V1.1 worker pool** — ADR after V1 load characterization.
4. **Hatchet vs Temporal vs custom for V2** — ADR after V1 production data.
5. **Worktree cleanup policy** — auto-cleanup on success vs retain-for-N-days vs explicit cleanup endpoint. ADR before V1 GA.
6. **SSE Last-Event-ID resume semantics** — V1 is fire-and-forget; V2 should support reconnect mid-stream. ADR for V2.
7. **Per-Change config schema** — what's overridable (parallelism, thresholds, providers, timeouts) and what's not. ADR before V1 GA.

## Appendix A — Iron Law Anti-Rationalization Tables

For each Iron Law, an anti-rationalization table captures the excuses the orchestrator might "encounter" (in agent prompts or operator pressure) and the principled response.

### Iron Law #1: NO PHASE TRANSITION WITHOUT PERSISTED ENVELOPE

| Excuse | Reality |
|---|---|
| "The agent already returned, just transition" | If we crash before persistence, we lose the envelope and cannot resume. |
| "Persistence is slow, skip on hot path" | Persistence latency is part of the phase API budget; design to fit it. |
| "It's a dev environment, doesn't matter" | Dev parity with prod prevents production surprises. |

### Iron Law #2: NO APPLY WITHOUT TASKS APPROVED

| Excuse | Reality |
|---|---|
| "Tasks looked good in tasks phase" | Confidence threshold + approval bit ensures explicit consent. |
| "We're piloting, skip approval" | Pilots without approval are how production gets bad code. |

### Iron Law #3: NO ARCHIVE WITHOUT VERIFY DONE

| Excuse | Reality |
|---|---|
| "Verify is slow, archive in parallel" | Archive without verify means archiving incorrect work. |
| "Caller will verify externally" | The external verification cannot enter our audit trail. |

### Iron Law #4: NO RUNTIME CALL WITHOUT GOVERNANCE DECISION

| Excuse | Reality |
|---|---|
| "Trivial action, skip governance" | "Trivial" is exactly where unaudited side effects compound. |
| "Governance is down, fall back to allow" | If governance is down, the system is degraded — fail closed. |

### Iron Law #5: NO FIX #4 WITHOUT ARCHITECTURAL ESCALATION

| Excuse | Reality |
|---|---|
| "Just one more retry, this time it'll work" | Three failures with same approach = wrong approach. |
| "The error message changed, that's progress" | New error in same family = same architectural flaw. |

---

*End of design document. Review checklist below.*

## Review Checklist (for spec self-review)

- [ ] No "TBD" / "TODO" / placeholders that will leak into implementation
- [ ] All sections internally consistent
- [ ] Scope is single-spec sized (no decomposition needed)
- [ ] No requirement is ambiguous
- [ ] Iron Laws are 5, anti-rationalized, and non-overlapping
- [ ] Domain model matches HTTP API matches Persistence schema
- [ ] All 9 phases addressed
- [ ] Open Questions clearly marked as deferred
