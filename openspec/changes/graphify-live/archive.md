# Archive Report: graphify-live (M4)

**Archived**: 2026-06-11T08:30:00Z
**Verdict**: PASS (0 CRITICAL, 2 WARNING, 3 SUGGESTION)
**Scope**: Post-arc backlog cluster "Graphify live" (items 1+2+11)

## Intent

Graphify went from build-time-only (INIT Pattern B) to live: LLM agents query the code graph through an allowlist-gated proxy (8 tools as graphify.<tool>), and every prompt carries deterministic graph routines (graph_stats + god_nodes) from the INIT-persisted GraphSummary. The AllowlistEnforcer shipped in PRE-0 found its first consumer after 4 milestones.

## PRs Landed (2)

| PR | Merged | Commits | LoC | Notes |
|---|---|---|---|---|
| agent-mcp #19 | 2026-06-10T23:49:02Z | 6 | ~650 | proxy + client adapter + allowlist wiring + registration (size:exception pre-approved) |
| orch #88 | 2026-06-11T07:31:57Z | 2 | ~160 | Routines layer concrete + population from GraphSummary |

## 5 Capabilities Delivered

| # | Capability | Evidence |
|---|---|---|
| 1 | mcp-stdio-client-adapter (PR1) | `adapters/outbound/mcpclient/client.go` — CommandTransport wrapper, timeout Connect, CallTool round-trip, no-leak Close |
| 2 | external-mcp-proxy (PR1, D-M4-2 persistent-per-process) | `application/mcpproxy/proxy.go` — lazy spawn, concurrent-safe session cache, guarded reap on App.Stop |
| 3 | allowlist-dispatch-wiring (PR1) | `AllowlistEnforcer.Authorize` pre-forward step 1 in proxy; disallowed tools rejected before spawn |
| 4 | graphify-provider-registration (PR1) | 8 tools registered as `<provider>.<tool>` on buildSDKServer + example.toml config block |
| 5 | routines-layer-concrete (PR2) | `RoutineOutput{Source,Content}` + `renderRoutines` Layer 5.5 + `buildPriorContext` population (graph_stats all phases, god_nodes EXPLORE+APPLY only) |

## Design Correction That Shaped the Milestone

**D-M4-2**: Proposal's per-change lifecycle NOT implementable (no change_id in agent-mcp request flow, nothing signals change end) → persistent-per-process (lazy spawn, App.Stop reap; graphify serve hot-reload makes long-lived correct). **FOURTH consecutive milestone where parallel spec+design checks-and-balances caught a proposal defect** (M1 enums, M3 IncludeTypes, M3-design callsite count, M4 lifecycle). Pattern is now institutional.

## Notable Lasts

- **LAST M0.5 stub retired**: `RoutineOutput` now concrete (was empty since INIT). Every PriorContext layer is now live.
- **PRE-0 blocker unblocked**: `AllowlistEnforcer` wired after 4 milestones of waiting (PRE-0 → INIT-0 → M1 → M2 → M3 → M4).

## Adaptations Approved

- **Env field addition**: `MCPProviderConfig.Env map[string]string` added (not in original design, backward compatible). Documented intent to forward environment variables to subprocess.
- **callerFactory signature evolution**: Design seam `mcpToolCaller` evolved into exported `ToolCaller` + injectable `callerFactory` option (cleaner DI).
- **SDK gotchas discovered**: go-sdk v1.6.1 `ClientSession` is concurrency-safe (relied on for dispatch-outside-mutex pattern). Strict TDD caught marshal gotcha (`{}` → `{"source":"","content":""}`) atomically in same commit as struct change.

## Forwarded to Backlog (Named Priorities)

### Verification Findings (R-1, R-2, R-3)

- **R-1 — env-forwarding-gap** (named priority): `MCPProviderConfig.Env` parsed in config but NOT forwarded to spawned subprocess. `mcpclient.New(command, timeoutS)` has no env parameter; `wire.go:288` drops `provCfg.Env`. Example: GRAPHIFY_QUERY_LOG_DISABLE=1 in example.toml is currently inert. Documented intent unmet. Wire env through `exec.Cmd.Env` in follow-up.
- **R-2 — proxy-tool-schemas**: Placeholder InputSchema `{"type":"object","properties":{}}` on all 8 proxy tools. LLM gets no argument hints; must know graphify tool args out-of-band. Hydrate from graphify `tools/list` on first connect for better ergonomics.
- **R-3 — proxy-spawn-mutex**: Spawn happens WHILE mutex held (not design's "unlock → spawn → relock"). Single global mutex serializes first-call spawns across ALL providers. Single-provider (graphify) M4 is harmless; multi-provider with slow startups would see head-of-line blocking. Use per-provider locks if/when that ships.

### Remaining Backlog Clusters (unchanged)

- **Loop hardening** (items 3+4+5+8+9): digest filter hardening, full-pipeline benchmark, webhook outbox, instrumentation, retry baseline
- **Governance + advisory** (items 6+10): LLM critic opt-in, governance-core HTTP surface
- **Trivial** (item 7): GET /usage skill_id

## SDD Cycle Complete

```
Explore → Propose → Spec (reconciled by D-M4-2) → Design (8 decisions) → Tasks 
→ Apply (2 PRs, stacked-to-main) → Verify (PASS, 0 CRITICAL) → Archive ✅
```

## Artifact References (Traceability)

| Phase | Engram Topic Key | Observation ID |
|---|---|---|
| Explore | `sdd/graphify-live/explore` | (not saved; integration of INIT-0 Surface 2 + graphify-audit.md) |
| Proposal | `sdd/graphify-live/proposal` | (file: proposal.md) |
| Spec | `sdd/graphify-live/spec` | (file: spec.md) |
| Design | `sdd/graphify-live/design` | (file: design.md) |
| Tasks | `sdd/graphify-live/tasks` | (file: tasks.md) |
| Apply progress | `sdd/graphify-live/apply-progress` | #846 |
| Verify report | `sdd/graphify-live/verify-report` | (file: verify.md) |
| Archive report | `sdd/graphify-live/archive-report` | (this document + engram save) |

## Closure

**Change graphify-live is CLOSED and ARCHIVED.** Both PRs merged to main. All 5 capabilities shipped. Strict TDD evidence present. All HARD operator invariants hold. Forwarded findings feed into M4+ backlog priorities.

Next: M4 backlog refinement. Recommended priority for next SDD cycle: R-1 env-forwarding-gap (silent no-op of documented config field).
