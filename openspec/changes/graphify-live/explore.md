# Exploration — graphify-live (M4)

**Strategy ref:** Post-V4.1 backlog (engram `sdd/m4-backlog`), "Graphify live" cluster (items 1+2+11).
**Mode:** SDD explore. NO production code changes; investigation + scoping.
**Scope:** sophia-agent-mcp (primary) + sophia-orchestator (Routines layer). sophia-cli: NO changes needed.
**Engram artifact:** `sdd/graphify-live/explore`.

---

## 1. go-sdk client-mode verdict — CLEARED, Pattern A UNBLOCKED

The load-bearing unknown from INIT-0 is resolved. The vendored `modelcontextprotocol/go-sdk` (v1.6.1 in agent-mcp go.mod) ships a production-ready MCP client:

```go
cmd := exec.Command("graphify", "serve", "graphify-out/graph.json")
transport := &mcp.CommandTransport{Command: cmd}
client := mcp.NewClient(&mcp.Implementation{Name: "sophia-proxy", Version: "1.0"}, nil)
cs, err := client.Connect(ctx, transport, nil)  // MCP initialize handshake handled
result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "graph_stats", Arguments: map[string]any{}})
```

Evidence (SDK source):
- `mcp.NewClient` — `mcp/client.go:40`
- `mcp.CommandTransport` — `mcp/cmd.go:17-47` (handles SIGTERM/SIGKILL per MCP spec)
- `client.Connect` — `mcp/client.go:136`
- `cs.CallTool` — `mcp/client.go:583`
- stdio subprocess test pattern — `mcp/cmd_test.go:37-67`
- usage example — `examples/client/listfeatures/main.go:55-63`

**No custom JSON-RPC client needed.**

---

## 2. Current state per item (file:line)

### Item 1 — Graphify MCP serve (Pattern A)
- `MCPProviderConfig` schema: agent-mcp `config.go:184,306` — LIVE since PRE-0, zero providers registered
- `AllowlistEnforcer`: agent-mcp `allowlist.go:58` — LIVE, 5 unit tests, ZERO production callsites
- `buildSDKServer()`: agent-mcp `server.go:233` — server-only (agent.run + agent.health), no proxy
- ExternalMCPProxy service: DOES NOT EXIST
- Go MCP stdio client adapter: DOES NOT EXIST

### Item 2 — AllowlistEnforcer wiring
Delivered together with the proxy (one Authorize call per proxied tool invocation).

### Item 11 — Routines layer
- `RoutineOutput struct{}` — orch `prior_context.go:103` (empty stub)
- `Routines []RoutineOutput` — `prior_context.go:44` (declared, never populated, never rendered)
- `structural.GraphSummary{GodNodes, TotalNodes, TotalEdges, CommunityCount}` — `domain/structural/context.go:109-121` — **ALREADY POPULATED by INIT**; Routines can feed from it with ZERO subprocess calls

### sophia-cli
GraphifyProber + bootstrap + flag all delivered in INIT-0. NO changes.

---

## 3. THE SCOPING PROPOSAL

### IN — M4 = items 1+2+11 (~820 LoC across 2 repos)

| Component | Repo | LoC |
|---|---|---|
| ExternalMCPProxy service | agent-mcp | ~200 |
| Go MCP stdio client adapter (CommandTransport wrapper) | agent-mcp | ~120 |
| AllowlistEnforcer wired into proxy | agent-mcp | ~30 |
| buildSDKServer proxy tool registration | agent-mcp | ~80 |
| wire.go (enforcer + proxy) | agent-mcp | ~40 |
| configs/example.toml graphify provider | agent-mcp | ~10 |
| Tests (strict TDD) | agent-mcp | ~180 |
| RoutineOutput concrete + renderRoutines | orch | ~55 |
| buildPriorContext Routines population | orch | ~25 |
| Routines tests | orch | ~80 |

### OUT (stay in backlog)
HTTP/SSE transport; `list_prs`/`triage_prs` tools; `affected_nodes` upstream PR; routines beyond the minimal 2; item #7 GET /usage skill_id; all loop-hardening + governance cluster items.

---

## 4. Spawn lifecycle — recommendation: `spawned_per_change`

ExternalMCPProxy (agent-mcp) spawns the sidecar per V4.1 §7-ter lifecycle. Orchestrator does NOT spawn graphify for the LLM path. INIT's Pattern B (`graphify update`, cached) and the proxy's `graphify serve` (hot-reload via mtime) are separate subprocess patterns with no conflict — serve picks up whatever update wrote.

---

## 5. Consumer paths (BOTH ship in M4)

**Path A (primary — LLM)**: the dispatched agent (opencode) calls graphify tools through the proxy registered on agent-mcp's SDK server. AllowlistEnforcer gates every call.

**Path B (secondary — Routines)**: `buildPriorContext()` reads `StructuralContext.GraphSummary` (persisted by INIT) → populates 2 `RoutineOutput` entries → `Render()` emits them. Zero subprocess calls, deterministic, fast.

---

## 6. Routines feed — 2 minimal routines

```go
type RoutineOutput struct {
    Source  string `json:"source"`
    Content string `json:"content"`
}
```

- `graphify.graph_stats`: `"Graph: {N} nodes, {E} edges, {C} communities"` — all phases
- `graphify.god_nodes`: `"Top blast-radius nodes: {n1}, {n2}, ..."` — EXPLORE + APPLY

**Gotcha**: `prior_context_test.go:148` asserts `RoutineOutput{}` marshals to `{}`. Adding fields changes that to `{"source":"","content":""}` — test MUST update in the same commit.

---

## 7. PR delivery

| PR | Repo | Scope | LoC | Note |
|---|---|---|---|---|
| PR1 | sophia-agent-mcp | proxy + client adapter + allowlist wiring + registration | ~650 | size:exception OR split at tasks |
| PR2 | sophia-orchestator | Routines layer concrete | ~160 | within budget |

PRs independent (separate Go modules). Either order works; PR1 first recommended (it's the cluster's core).

---

## 8. Operator decisions for proposal

- **Q1**: PR1 delivery — single PR (size:exception) vs split (client adapter first, proxy second)?
- **Q2**: lifecycle — confirm `spawned_per_change` (vs `spawned_per_session`)?
- **Q3**: tools_allowed — confirm 8 tools (query_graph, get_node, get_neighbors, get_community, god_nodes, graph_stats, shortest_path, get_pr_impact), omitting list_prs + triage_prs?

---

## 9. Risks

1. `prior_context_test.go:148` RoutineOutput marshal assertion — mechanical same-commit fix
2. go-sdk v1.6.1 not yet extracted from module cache — first `go build` triggers it; LOW
3. ExternalMCPProxy goroutine/process leak if `cs.Close()` unguarded on context cancel — must be tested
4. graphify serve startup latency on very large graph.json — `startup_timeout_s` config field covers

---

## 10. Skill resolution

Standard SDD skills. Apply needs: go-testing, api-contracts (MCP tool registration), background-workers (subprocess lifecycle).

`skill_resolution: none`.
