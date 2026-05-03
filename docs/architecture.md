# Architecture — sophia-orchestator

## Overview

`sophia-orchestator` follows hexagonal (clean) architecture with eight bounded
contexts. Inbound adapters depend on inbound port interfaces; application
services depend on outbound port interfaces; concrete outbound adapters depend
on nothing inside the domain. The dependency arrow always points inward.

## Dependency diagram

```
┌────────────────────────────────────────────────────────────────────┐
│ adapters/inbound  (http, sdk, [mcp v2])                            │
│                          │                                         │
│                          ▼                                         │
│ ports/inbound  (ChangeService, PhaseService, ApplyService,         │
│                  EventStream)                                      │
│                          │                                         │
│                          ▼                                         │
│ application/services  (one use case per command, transactional)    │
│                          │                                         │
│                          ▼                                         │
│ ports/outbound  (Repositories, GovernanceClient, MemoryClient,     │
│                  RuntimeClient, AgentDispatcher,                   │
│                  ArtifactStore, AuditLog, SpawnGovernorRepo)       │
│                          │                                         │
│      ┌───────────────────┼─────────────────────┐                   │
│      ▼                   ▼                     ▼                   │
│ adapters/outbound       pg                  http-clients           │
│ (engram, openspec,    (changes, phases,    (governance, memory,    │
│  hybrid stores;        boards, tasks,       runtime; CB per-target)│
│  opencode-disp,        sessions,                                   │
│  worktree-mgr)         worktrees, audit)                           │
└────────────────────────────────────────────────────────────────────┘

         domain/  (aggregates: Change, Phase, ApplyBoard, Group,
                  Task, AgentSession, Worktree, Envelope, IronLaw)
         ▲     ▲     ▲                                  ▲
         │     │     │                                  │
         └─────┴─────┴──── consumed by everything above
```

## Composition rule

`internal/bootstrap/wire.go` is the **only** file that imports concrete adapter
and infrastructure implementations. Domain and application packages never
import adapters or infrastructure. Violation = build-time architecture error
(golangci-lint `forbidigo` rule).

## Bounded contexts

| Context | Responsibility |
|---|---|
| **Change** | SDD Change aggregate root: lifecycle, current phase pointer, status, artifact-store mode |
| **Phase** | One phase execution: envelope, status, confidence, retry budget, attempts |
| **Apply** | Sub-aggregate of Phase apply: Board, Groups, Tasks, claim/release |
| **AgentDispatch** | A single AI CLI invocation: prompt, worktree, captured envelope, exit code |
| **Worktree** | Lifecycle of a git worktree: create, lock, release, cleanup |
| **Artifact** | Abstraction over engram / openspec / hybrid; topic-key resolution |
| **Discipline** | Iron Laws + HARD-GATE injection + envelope validation + Spawn Governor |
| **Audit** | Append-only trail of every transition, mirrored to memory-engine ledger |

## Integration points

- **agent-governance-core** — consumed via outbound port `GovernanceClient` (HTTP).
- **sophia-memory-engine** — consumed via outbound port `MemoryClient` (HTTP).
- **sophia-runtime-adapters** — consumed via outbound port `RuntimeClient` (HTTP); exposes Phase 1 (shell, git, fs, http) and Phase 2 (locks, mailbox, reservations) capabilities.

## Process layout

V1 ships a single binary `cmd/sophia-orchestator`. Goroutines spawned by the
HTTP handler execute long-running phases asynchronously, with progress streamed
via SSE. Postgres advisory locks coordinate across goroutines. There is no
external queue in V1.

V1.1 may split off `cmd/sophia-orchestator-worker` if load characteristics
demand. ADR pending after V1 production data.

## Source of truth

Spec: [`docs/superpowers/specs/2026-05-03-sophia-orchestator-design.md`](superpowers/specs/2026-05-03-sophia-orchestator-design.md)
