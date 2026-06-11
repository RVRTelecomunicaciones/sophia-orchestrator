# Design: graphify-live (M4)

## Technical Approach

Two independent PRs. **PR1 (agent-mcp)** adds an outbound stdio MCP client adapter (go-sdk `CommandTransport`), an `ExternalMCPProxy` application service that owns the graphify sidecar and dispatches `CallTool` through `AllowlistEnforcer.Authorize`, and registers the 8 allowed tools onto `buildSDKServer`. **PR2 (orch)** makes `RoutineOutput` concrete and populates 2 routines from the already-loaded `StructuralContext.GraphSummary` in `buildPriorContext` — zero subprocess. Maps to proposal Path A (LLM proxy) + Path B (deterministic routines).

## Verified Code-Reality Corrections (drive the decisions below)

| Proposal assumption | Reality (verified) | Impact |
|---|---|---|
| Session map keyed by `change_id`; close-on-change-end | `agent.run` req (`agentrun/request.go:43`) has NO change_id; proxied tools are **stateless HTTP MCP calls** on the SDK server; **nothing in agent-mcp signals change end** | Lifecycle redesigned → D-M4-2 |
| `startup_timeout_s` honored | Field does NOT exist on `MCPProviderConfig` (`config.go:184-213`) | Must add field → D-M4-5 |
| `buildSDKServer` registers proxy tools | It is a `*Server` METHOD with static `AddTool` (`server.go:233`) | Inject proxy into `Server` → D-M4-4 |
| `buildPriorContext` reads GraphSummary | Already loads `structuralCtx` (`service.go:1104-1109`) but does NOT receive phase `p` | Pass `p.Type()` → D-M4-7/8 |

## Architecture Decisions

### D-M4-1 Stdio client adapter
**Choice**: New pkg `internal/adapters/outbound/mcpclient`. Type `StdioClient` wraps `mcp.NewClient` + `*mcp.CommandTransport`. Command parsed by `strings.Fields` (graphify command is a fixed simple argv, no shell metachars). `Connect(ctx)` enforces startup timeout via `context.WithTimeout`. Exposes `CallTool(ctx, name, args)` and `Close()` (SDK does stdin-close→SIGTERM→SIGKILL, `cmd.go:69`). **Alternatives**: define a port in application layer (rejected — single concrete adapter, YAGNI; proxy owns it directly via a small local interface for test seams). **Rationale**: matches hexagonal outbound-adapter convention already used (`adapters/outbound/subprocess`).

### D-M4-2 ExternalMCPProxy lifecycle — **persistent-per-process (lazy spawn, reap on App.Stop)**
**Choice**: pkg `internal/application/mcpproxy`. `Proxy` holds one lazily-spawned `StdioClient` per provider id, guarded by `sync.Mutex`. First proxied tool call spawns+connects; subsequent calls reuse. `Close(ctx)` (called from `App.Stop`, wire.go:218) reaps the sidecar, guarded so double-close / ctx-cancel cannot leak (`closed bool` flag under mutex). **Alternatives**: (a) `spawned_per_change` keyed by change_id — REJECTED, no change_id exists in the request flow and no change-end signal; (b) idle-TTL reaper goroutine — REJECTED for M4, adds a timer/goroutine with no consumer demand. **Rationale**: graphify `serve` hot-reloads `graph.json` via mtime (`serve.py:535`), so one long-lived process per bridge correctly serves every change without restart. Config keeps `lifecycle = "spawned_per_change"` as a documented intent label; the bridge implements it as process-scoped because the bridge IS per-change in deployment. SDK `ClientSession` is concurrency-safe (`client.go:30` mutex), so the single session serves concurrent tool calls.

### D-M4-3 Allowlist wiring
**Choice**: `Proxy.CallTool(ctx, providerID, tool, args)` calls `enforcer.Authorize(providerID, tool)` FIRST, before any spawn/connect. On `ErrToolNotAllowed`/`ErrUnknownProvider` → return SDK error result (`IsError:true`) via existing `errorResult`, mapping to code `tool_not_allowed`. **Rationale**: reject-before-spawn prevents wasted subprocess; reuses `mapDomainError` pattern.

### D-M4-4 SDK tool registration + naming
**Choice**: In `buildSDKServer`, after native `agent.run`/`agent.health`, loop `cfg.MCPProviders` → for each `ToolsAllowed` entry register a proxy `Tool` named **`<providerID>.<tool>`** (e.g. `graphify.graph_stats`) with a handler closure calling `proxy.CallTool(ctx, providerID, rawTool, args)`. **Alternatives**: raw tool names (`graph_stats`) — REJECTED, risks future collision across providers and with native dotted names. **Rationale**: dotted prefix matches native `agent.run` convention, guarantees no collision with `agent.*`, and the prefix maps 1:1 to the allowlist provider id.

### D-M4-5 Provider config
**Choice**: Add `StartupTimeoutS int \`toml:"startup_timeout_s"\`` to `MCPProviderConfig`; default 10 when zero (applied in adapter, not config defaults, to keep zero-value meaningful). Add graphify block to `configs/example.toml` (id, command, transport=stdio, tools_allowed=8, lifecycle, startup_timeout_s=10, env GRAPHIFY_QUERY_LOG_DISABLE=1). **Rationale**: risk mitigation in proposal requires the field; additive, backward compatible.

### D-M4-6 RoutineOutput + renderRoutines
**Choice**: `RoutineOutput{Source, Content string}` with json tags. Add `Routines []RoutineOutput` render as **Layer 5.5 — after BusinessRules, before PhaseIdentity** (V4.1 §12 deterministic layer order). Attribution header `## Routine: <source>` (matches M3 `## Rule:`/`## Episode:` style). Empty slice → layer skipped. **Rationale**: routines are derived context, lower precedence than rules, higher than the apply-path phase identity block.

### D-M4-7/8 buildPriorContext population + phase gating
**Choice**: Change `buildPriorContext(ctx, c)` → `buildPriorContext(ctx, c, p.Type())` (caller `service.go:424` has `p`). After loading `structuralCtx`, if `structuralCtx.GraphSummary != nil` emit `graphify.graph_stats` (all phases) and, when phase ∈ {EXPLORE, APPLY}, `graphify.god_nodes`. nil GraphSummary → no routines (degraded-INIT safe). **Alternatives**: populate at the call site after return — REJECTED, GraphSummary is already in scope inside the func; passing phase is one-param, cheaper than re-loading. **Rationale**: single data path, no new memory round-trip.

## Data Flow

PR1 (Path A):
```
opencode LLM --MCP--> agent-mcp SDK server (graphify.<tool>)
   --> Proxy.CallTool --> Authorize --> [lazy spawn] StdioClient.CallTool
   --> CommandTransport(graphify serve) --> result
App.Stop --> Proxy.Close --> sidecar reaped (guarded)
```
PR2 (Path B):
```
buildPriorContext --> GetByTopicKey(sdd/<name>/init) --> StructuralContext.GraphSummary
   --> []RoutineOutput --> Render (Layer 5.5) --> prompt
```

## File Changes

| File | Action | Description |
|------|--------|-------------|
| agent-mcp `adapters/outbound/mcpclient/client.go` | Create | `StdioClient`: CommandTransport wrapper + timeout Connect + CallTool/Close |
| agent-mcp `application/mcpproxy/proxy.go` | Create | `Proxy`: lazy spawn, mutex, guarded Close, Authorize-first dispatch |
| agent-mcp `infrastructure/config/config.go` | Modify | add `StartupTimeoutS` to `MCPProviderConfig` |
| agent-mcp `adapters/inbound/mcp/server.go` | Modify | `buildSDKServer` registers `<id>.<tool>` proxy tools; `Server` gains proxy dep |
| agent-mcp `bootstrap/wire.go` | Modify | build enforcer + proxy from cfg.MCPProviders; add proxy.Close to stopFn |
| agent-mcp `configs/example.toml` | Modify | graphify `[[mcp_providers]]` block |
| orch `application/discipline/prior_context.go` | Modify | concrete `RoutineOutput` + `renderRoutines` + collectLayers Layer 5.5 |
| orch `application/phase/service.go` | Modify | `buildPriorContext` takes phase; populates 2 routines from GraphSummary |
| orch `application/discipline/prior_context_test.go:148` | Modify | marshal assertion `{}` → `{"source":"","content":""}` (same commit) |

## Interfaces / Contracts

```go
// agent-mcp — proxy seam (proxy owns adapter; small interface for test fakes)
type mcpToolCaller interface {
    CallTool(ctx context.Context, name string, args map[string]any) (*sdkmcp.CallToolResult, error)
    Close() error
}
func (p *Proxy) CallTool(ctx context.Context, providerID, tool string, args map[string]any) (*sdkmcp.CallToolResult, error)

// orch
type RoutineOutput struct {
    Source  string `json:"source"`
    Content string `json:"content"`
}
```

## Testing Strategy

| Layer | What | Approach |
|-------|------|----------|
| Unit (PR1) | client adapter dispatch | `mcp.NewInMemoryTransports()` fake server — no real graphify |
| Unit (PR1) | Authorize-before-spawn; `ErrToolNotAllowed` rejection | proxy with fake caller; assert no spawn on reject |
| Unit (PR1) | guarded Close no-leak; double-close safe | spawn fake, Close twice, assert single reap |
| Integration (PR1) | real `graphify serve` | skip-guarded (`testing.Short`/env) — not in CI default |
| Unit (PR2) | 2 routines from GraphSummary; nil → empty; phase gating | table-driven on `buildPriorContext` |
| Unit (PR2) | Render Layer 5.5 order + attribution | byte-exact golden update |

Strict TDD ACTIVE: tests first. PR1 — no-leak + rejection tests before impl. PR2 — marshal assertion + nil-degraded test before populate.

## Migration / Rollout

No migration. Both PRs additive; revert restores dormant stubs (proposal Rollback). `GraphSummary` (INIT-owned) untouched.

## Open Questions

- [ ] None blocking. Lifecycle reinterpretation (D-M4-2) is operator-visible: `spawned_per_change` label now means process-scoped, not change-keyed — confirm at spec acceptance.
