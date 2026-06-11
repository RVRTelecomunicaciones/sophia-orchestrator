# Proposal: graphify-live (M4)

## Intent

Graphify is build-time-only today: INIT runs `graphify update` and persists `StructuralContext.GraphSummary`, but nothing consumes the live code graph during a change. M4 makes Graphify **live** along two consumer paths: (A) LLM phases query the graph through an allowlist-gated MCP proxy on agent-mcp; (B) prompts carry deterministic graph routines built from the already-persisted `GraphSummary`. Unblocks the `AllowlistEnforcer` (LIVE, 0 callsites) and `RoutineOutput` (empty stub) that have shipped dormant since PRE-0/INIT. (explore §1-2)

## Scope

### In Scope
- **PR1 (sophia-agent-mcp, ~650 LoC, `size:exception`)**: `ExternalMCPProxy` service + go-sdk MCP stdio client adapter (`mcp.NewClient` + `CommandTransport` + `CallTool`, v1.6.1 verified) + `AllowlistEnforcer` wired (one `Authorize` per proxied call) + `buildSDKServer` proxy tool registration + graphify provider in `configs/example.toml`. (explore §1, §2 item 1-2, §7)
- **PR2 (sophia-orchestator, ~160 LoC, within budget)**: concrete `RoutineOutput{Source, Content}` + `renderRoutines` + `buildPriorContext` populating 2 routines from `StructuralContext.GraphSummary` (zero subprocess). (explore §2 item 11, §5-6)

### Out of Scope (stay in backlog)
HTTP/SSE transport; `list_prs` + `triage_prs` tools; `affected_nodes` upstream PR; routines beyond the 2; item #7 GET /usage skill_id; all loop-hardening + governance cluster items. (explore §3 OUT)

## Capabilities

### New Capabilities
- `mcp-stdio-client-adapter` (PR1): go-sdk `CommandTransport` wrapper that connects to a stdio MCP subprocess and dispatches `CallTool`.
- `external-mcp-proxy` (PR1): per-change sidecar lifecycle (`spawned_per_change`) — spawn graphify serve on first tool call, kill on change close, hot-reload via mtime.
- `allowlist-dispatch-wiring` (PR1): `AllowlistEnforcer.Authorize` gates every proxied tool call; non-allowed tool → `ErrToolNotAllowed`.
- `graphify-provider-registration` (PR1): 8 tools registered on the SDK server + graphify provider config.
- `routines-layer-concrete` (PR2): concrete `RoutineOutput` + render path fed from `GraphSummary`.

### Modified Capabilities
None — both PRs introduce dormant-stub completions, not spec-level requirement changes to existing live behavior.

## Approach

PR1: `ExternalMCPProxy` owns the sidecar lifecycle. On first proxied tool call per change it spawns `graphify serve graphify-out/graph.json` via `exec.Command` + `mcp.CommandTransport`, runs the MCP initialize handshake (`client.Connect`), and forwards `CallTool`. Every call passes through `AllowlistEnforcer.Authorize` first; rejected tools return `ErrToolNotAllowed` without spawning/calling. `buildSDKServer` registers the 8 allowed tools as proxy entries. Sidecar is killed on change close (`cs.Close()` guarded against context-cancel leak). Hot-reload is graphify-native (mtime). Orchestrator does NOT spawn graphify for Path A. (explore §1, §4, §7)

PR2: `buildPriorContext` reads `StructuralContext.GraphSummary` (populated by INIT) → emits 2 `RoutineOutput`: `graphify.graph_stats` ("Graph: N nodes, E edges, C communities", all phases) + `graphify.god_nodes` ("Top blast-radius nodes: ...", EXPLORE+APPLY only). `Render()` emits them with source attribution. Zero subprocess, deterministic. (explore §5-6)

## Affected Areas

| Area | Impact | Description |
|------|--------|-------------|
| agent-mcp `allowlist.go:58` | Modified | `Authorize` gets first production callsite |
| agent-mcp `server.go:233` `buildSDKServer` | Modified | Register 8 proxy tools |
| agent-mcp `config.go:184,306` | Modified | graphify `MCPProviderConfig` populated |
| agent-mcp `ExternalMCPProxy` | New | Sidecar lifecycle + dispatch service |
| agent-mcp MCP stdio client adapter | New | `CommandTransport` wrapper |
| agent-mcp `wire.go` | Modified | Wire enforcer + proxy (~40) |
| agent-mcp `configs/example.toml` | Modified | graphify provider block |
| orch `prior_context.go:44,103` | Modified | Concrete `RoutineOutput`, populate + render |
| orch `prior_context_test.go:148` | Modified | Marshal assertion update (same commit) |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| `prior_context_test.go:148` marshal assertion breaks (`{}` → `{"source":"","content":""}`) | High | Mechanical same-commit fix; called out in PR2 acceptance (explore §6, §9.1) |
| go-sdk v1.6.1 not extracted from module cache | Low | First `go build` triggers extraction (explore §9.2) |
| `ExternalMCPProxy` goroutine/process leak if `cs.Close()` unguarded on ctx cancel | Med | Guarded close + no-leak test in PR1 acceptance (explore §9.3) |
| graphify serve startup latency on large `graph.json` | Med | `startup_timeout_s` config field honored (explore §9.4) |

## Rollback Plan

- **PR1**: independent Go module; revert the PR. `AllowlistEnforcer` + `MCPProviderConfig` return to dormant (0 callsites), `buildSDKServer` back to server-only (agent.run + agent.health). No persisted state, no migration.
- **PR2**: independent module; revert the PR. `RoutineOutput` returns to empty stub, `Routines` unpopulated/unrendered. `GraphSummary` (INIT-owned) untouched. Revert the marshal-test edit in the same revert.

## Dependencies

- INIT-0 persists `StructuralContext.GraphSummary` (delivered — PR2 reads it). (explore §2)
- graphify CLI + go-sdk v1.6.1 vendored in agent-mcp go.mod (delivered). (explore §1)
- PRs independent (separate modules); PR1 recommended first as cluster core. (explore §7)

## Success Criteria

**PR1**
- [ ] Proxy spawns `graphify serve` per-change on first tool call
- [ ] 8 tools callable through agent-mcp (query_graph, get_node, get_neighbors, get_community, god_nodes, graph_stats, shortest_path, get_pr_impact)
- [ ] Tool outside allowlist rejected with `ErrToolNotAllowed`
- [ ] Subprocess killed on change close — no-leak test passes
- [ ] `startup_timeout_s` honored
- [ ] golangci-lint + tests green

**PR2**
- [ ] `PriorContext.Routines` populated from `GraphSummary`
- [ ] `Render()` emits routines with source attribution
- [ ] nil `GraphSummary` → empty routines (degraded INIT safe)
- [ ] `prior_context_test.go:148` marshal assertion updated
- [ ] golangci-lint + tests green

## Strict TDD Note

strict_tdd ACTIVE. Both PRs: test-first per strict-tdd.md (no Standard Mode fallback). PR1 must include the subprocess no-leak test and the `ErrToolNotAllowed` rejection test before implementation. PR2 must update the marshal assertion and add nil-`GraphSummary` degraded test before populating. golangci-lint pre-push; conventional commits, no Co-Authored-By / AI attribution.

## Open Questions

None. explore §8 Q1 (single PR + `size:exception`), Q2 (`spawned_per_change`), and Q3 (8 tools, exclude `list_prs`/`triage_prs`) are all operator-locked.
