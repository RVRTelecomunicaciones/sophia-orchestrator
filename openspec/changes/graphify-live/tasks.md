# Tasks: graphify-live (M4)

## Review Workload Forecast

| Field | Value |
|-------|-------|
| PR1 agent-mcp | ~650 LoC | size:exception pre-approved |
| PR2 orch | ~160 LoC | within budget |
| 400-line budget risk | High (PR1) / Low (PR2) |
| Chained PRs recommended | No — two independent PRs, each within their own exception/budget |
| Suggested split | PR1 (sophia-agent-mcp) → PR2 (sophia-orchestator); independent, no cross-repo gate |
| Delivery strategy | ask-on-risk (size:exception pre-approved for PR1 by operator) |
| Chain strategy | stacked-to-main |

Decision needed before apply: No
Chained PRs recommended: No
Chain strategy: stacked-to-main
400-line budget risk: High

### Suggested Work Units

| Unit | Goal | Likely PR | Notes |
|------|------|-----------|-------|
| 1 | MCP stdio client adapter + ExternalMCPProxy + allowlist wiring + SDK registration | PR1 (sophia-agent-mcp) | size:exception pre-approved; ~650 LoC; PR1 first recommended |
| 2 | RoutineOutput concrete + renderRoutines + buildPriorContext routines population | PR2 (sophia-orchestator) | ~160 LoC; within budget; independent |

## Locked Decisions Absorbed

- **D-M4-2**: persistent-per-process lifecycle (lazy spawn first call, session cached process-lifetime, reap on App.Stop); `spawned_per_change` label = documented intent only; no change_id in agent-mcp
- **D-M4-1**: `StdioClient` in `internal/adapters/outbound/mcpclient`; `mcp.NewClient` + `CommandTransport`; `strings.Fields` for command parse
- **D-M4-3**: `Authorize` before every proxied call, before any spawn; reject-before-spawn
- **D-M4-4**: proxy tools named `<providerID>.<tool>` on `buildSDKServer`; loop `cfg.MCPProviders`
- **D-M4-5**: `StartupTimeoutS int` added to `MCPProviderConfig`; default 10 applied in adapter (not config defaults)
- **D-M4-6**: `RoutineOutput{Source, Content string}` with json tags; Layer 5.5 (after BusinessRules, before PhaseIdentity); attribution header `## Routine: <source>`
- **D-M4-7/8**: `buildPriorContext(ctx, c, p.Type())`; 2 routines from GraphSummary; nil GraphSummary → empty; phase gate god_nodes to EXPLORE+APPLY
- go-sdk v1.6.1 verified: `mcp.NewClient` + `CommandTransport` + `CallTool`; SDK `ClientSession` is concurrency-safe
- Strict TDD ACTIVE: tests first, no Standard Mode fallback
- Conventional commits; no Co-Authored-By / AI attribution; golangci-lint pre-push

---

## PR1 Task Groups (sophia-agent-mcp)

### Group A — startup_timeout_s config field
Spec: graphify-provider-registration + external-mcp-proxy (D-M4-5)
Sequential: must land before Group B (adapter reads the field).

- [x] A-1 **RED**: In `infrastructure/config/config_test.go`, add test: parse TOML with `startup_timeout_s = 10` → `MCPProviderConfig.StartupTimeoutS == 10`; parse without field → `StartupTimeoutS == 0` (zero-value default).
- [x] A-2 **GREEN**: Add `StartupTimeoutS int \`toml:"startup_timeout_s"\`` to `MCPProviderConfig` in `infrastructure/config/config.go:184-213`. Tests green.
- [x] A-3 **VERIFY**: `go test ./infrastructure/config/...` + `golangci-lint run ./infrastructure/config/...` pass.

### Group B — MCP stdio client adapter
Spec: mcp-stdio-client-adapter (D-M4-1)
Sequential after A. Parallel with C.

- [x] B-1 **RED**: Create `adapters/outbound/mcpclient/client_test.go`. Write failing tests:
  - `TestConnect_Success`: fake `mcp.InMemoryTransports()` server responds to initialize; expect no error.
  - `TestConnect_StartupTimeout`: context timeout before handshake; expect timeout error; no subprocess leak.
  - `TestCallTool_RoundTrip`: connected client; `CallTool` returns result unchanged (`IsError:false`).
  - `TestCallTool_ErrorResult`: subprocess returns `IsError:true`; adapter propagates without conversion.
  - `TestClose_NoLeak`: `Close` after connect; no goroutine leak (goleak or manual goroutine count check).
  - `TestClose_AfterCtxCancel`: parent ctx cancelled; `Close` still terminates subprocess cleanly.
- [x] B-2 **GREEN**: Create `adapters/outbound/mcpclient/client.go`. Implement `StdioClient`:
  - Fields: `cmd []string`, `timeoutS int`, `session *mcp.ClientSession`.
  - `Connect(ctx)`: `context.WithTimeout` using `StartupTimeoutS` (default 10 when zero); `mcp.CommandTransport{Command: exec.Command(fields[0], fields[1:]...)}` via `strings.Fields`; `mcp.NewClient(...).Connect(ctx, transport, nil)`; store session.
  - `CallTool(ctx, name, args)`: delegate to `session.CallTool`.
  - `Close()`: call `session.Close()`.
- [x] B-3 **VERIFY**: `go test ./adapters/outbound/mcpclient/...` + `golangci-lint run ./adapters/outbound/mcpclient/...` pass.

### Group C — ExternalMCPProxy (persistent-per-process, D-M4-2)
Spec: external-mcp-proxy + allowlist-dispatch-wiring (D-M4-2, D-M4-3)
Sequential after A. Parallel with B.

- [x] C-1 **RED**: Create `application/mcpproxy/proxy_test.go`. Write failing tests:
  - `TestProxy_LazySpawn_FirstCall`: no session before first call; after call, session exists.
  - `TestProxy_ReuseSession`: second call for same provider reuses session; spawn called exactly once.
  - `TestProxy_ConcurrentCalls_Safe`: N goroutines call simultaneously on active session; no race (run with `-race`).
  - `TestProxy_ConcurrentFirstCalls_SpawnOnce`: N goroutines race on first call; spawn happens exactly once.
  - `TestProxy_Close_ReapsSession`: `Close` on active session; session `Close` called; cache cleared.
  - `TestProxy_Close_GuardedNoLeak`: ctx cancelled then `Close`; no goroutine leak; subprocess terminated.
  - `TestProxy_Close_WithoutSpawn_Noop`: `Close` before any call; no error, no subprocess interaction.
  - `TestProxy_UnknownProvider_ErrUnknownProvider`: unknown providerID → `ErrUnknownProvider`; no spawn.
- [x] C-2 **GREEN**: Create `application/mcpproxy/proxy.go`. Implement `Proxy`:
  - Fields: `cfg map[string]MCPProviderConfig`, `enforcer *AllowlistEnforcer`, `sessions map[string]mcpToolCaller`, `mu sync.Mutex`, `closed bool`.
  - Local interface `mcpToolCaller { CallTool(...); Close() error }` for test fakes.
  - `CallTool(ctx, providerID, tool, args)`: lock → check closed → `enforcer.Authorize(providerID, tool)` first; if session nil: unlock → spawn → relock; dispatch; unlock.
  - `Close(ctx)`: lock → if closed return nil; mark closed; iterate sessions → `s.Close()`; unlock.
- [x] C-3 **VERIFY**: `go test -race ./application/mcpproxy/...` + `golangci-lint run ./application/mcpproxy/...` pass.

### Group D — Allowlist wiring (enforcer boot construction)
Spec: allowlist-dispatch-wiring (D-M4-3)
Sequential after C (test uses proxy + enforcer together).

- [x] D-1 **RED**: Add tests to `application/mcpproxy/proxy_test.go` (or new `allowlist_wiring_test.go`):
  - `TestAuthorize_AllowedTool_Forwarded`: allowed tool passes enforcer; `CallTool` dispatched.
  - `TestAuthorize_DisallowedTool_Rejected`: `list_prs` not in allowlist → `ErrToolNotAllowed`; no spawn.
  - `TestAuthorize_EmptyAllowlist_RejectsAll`: provider with empty `tools_allowed` → every tool rejected.
  - `TestAuthorize_UnknownProvider`: `ErrUnknownProvider`; no spawn.
- [x] D-2 **GREEN**: Verify `AllowlistEnforcer` (existing `allowlist.go:58`) wired into `Proxy` constructor. No new allowlist logic — `Authorize` is already correct; wire it as enforcer field on `Proxy` (done in C-2). Confirm `mapDomainError` maps `ErrToolNotAllowed` to `tool_not_allowed` SDK error code.
- [x] D-3 **VERIFY**: `go test ./application/mcpproxy/... ./adapters/inbound/mcp/...` pass.

### Group E — SDK server registration + provider config
Spec: graphify-provider-registration (D-M4-4, D-M4-5)
Sequential after C+D (proxy must exist before server registers it).

- [x] E-1 **RED**: Add test to `adapters/inbound/mcp/server_test.go`:
  - `TestBuildSDKServer_ProxyTools_Registered`: build server with graphify provider config; assert all 8 tools (`graphify.query_graph`, `graphify.get_node`, `graphify.get_neighbors`, `graphify.get_community`, `graphify.god_nodes`, `graphify.graph_stats`, `graphify.shortest_path`, `graphify.get_pr_impact`) present.
  - `TestBuildSDKServer_NativeTools_Unaffected`: `agent.run` and `agent.health` still registered after proxy tools added.
  - `TestBuildSDKServer_NoPRTools`: `graphify.list_prs` and `graphify.triage_prs` absent from registered tools.
- [x] E-2 **GREEN**: Modify `adapters/inbound/mcp/server.go:buildSDKServer`: accept `proxy *mcpproxy.Proxy` + `providers []MCPProviderConfig`; after native tools loop `providers` → for each `ToolsAllowed` entry `server.AddTool("<providerID>.<tool>", handler)` where handler calls `proxy.CallTool(ctx, providerID, rawTool, args)`.
- [x] E-3 **GREEN**: Modify `configs/example.toml`: add `[[mcp_providers]]` block with `id = "graphify"`, `package = "graphifyy[mcp]==0.8.35"`, `command = "graphify serve graphify-out/graph.json"`, `transport = "stdio"`, `lifecycle = "spawned_per_change"`, `startup_timeout_s = 10`, `tools_allowed = [8 tools]`, `env = {GRAPHIFY_QUERY_LOG_DISABLE = "1"}`.
- [x] E-4 **VERIFY**: `go test ./adapters/inbound/mcp/...` pass; TOML parses without error.

### Group F — Wire (bootstrap)
Spec: all PR1 specs (D-M4-1..5)
Sequential after B+C+D+E (wires all components).

- [x] F-1 **GREEN**: Modify `bootstrap/wire.go`: construct `AllowlistEnforcer` from `cfg.MCPProviders`; construct `Proxy` (inject enforcer + provider configs); inject `Proxy` into `buildSDKServer`; add `proxy.Close(ctx)` to `stopFn` sequence (after `App.Stop`, wire.go:218).
- [x] F-2 **VERIFY**: `go build ./...` succeeds; no import cycles; `golangci-lint run ./bootstrap/...` clean.

### Group G — PR1 Checkpoint
Sequential after F.

- [x] G-1 **CHECKPOINT**: `go test ./...` all green; `go test -race ./application/mcpproxy/... ./adapters/outbound/mcpclient/...` no race; `golangci-lint run ./...` 0 issues.
- [x] G-2 **COMMIT**: Conventional commit `feat(mcpproxy): add ExternalMCPProxy with stdio client, allowlist wiring, and SDK registration`.
- [ ] G-3 **PR**: Push branch; open PR1 against main with `size:exception` label. [BLOCKED — awaiting operator push approval]

---

## PR2 Task Groups (sophia-orchestator)

### Group H — RoutineOutput concrete + renderRoutines
Spec: routines-layer-concrete (D-M4-6)
Sequential start of PR2.

- [x] H-1 **RED**: In `application/discipline/prior_context_test.go:148`, update marshal assertion: `RoutineOutput{}` → `{"source":"","content":""}`. Run test; confirm it fails (struct still empty).
- [x] H-2 **RED**: Add render tests to `prior_context_test.go`:
  - `TestRender_RoutinesLayer55_WithAttribution`: `Routines` with 2 entries; rendered output contains `## Routine: graphify.graph_stats` and `## Routine: graphify.god_nodes`; appears after BusinessRules layer, before PhaseIdentity layer.
  - `TestRender_EmptyRoutines_NoOutput`: empty `Routines`; no `## Routine:` header in rendered output.
- [x] H-3 **GREEN**: In `application/discipline/prior_context.go`: add `Source string \`json:"source"\`` and `Content string \`json:"content"\`` to `RoutineOutput`; implement `renderRoutines(routines []RoutineOutput) string` emitting `## Routine: <Source>\n<Content>\n`; insert call into `collectLayers` at Layer 5.5 position (after BusinessRules, before PhaseIdentity).
- [x] H-4 **VERIFY**: `go test ./application/discipline/...` all green including updated marshal assertion.

### Group I — buildPriorContext routines population + phase gating
Spec: routines-layer-concrete (D-M4-7/8)
Sequential after H.

- [x] I-1 **RED**: In `application/phase/service_test.go` (or new `prior_context_routines_test.go`), add table-driven tests:
  - `TestBuildPriorContext_GraphStats_AllPhases`: non-nil `GraphSummary{TotalNodes:50, TotalEdges:120, CommunityCount:6}`; all 5 phase types; each produces `Routines[0].Source == "graphify.graph_stats"` and `Content == "Graph: 50 nodes, 120 edges, 6 communities"`.
  - `TestBuildPriorContext_GodNodes_ExploreApplyOnly`: non-nil `GraphSummary{GodNodes:["pkg/core","pkg/domain"]}`; EXPLORE + APPLY → `Routines` len 2, second entry `Source == "graphify.god_nodes"`; INIT/DESIGN/VERIFY → `Routines` len 1, no god_nodes entry.
  - `TestBuildPriorContext_NilGraphSummary_EmptyRoutines`: nil `GraphSummary`; all phases; `Routines` empty; no panic.
  - `TestBuildPriorContext_NilStructuralCtx_EmptyRoutines`: nil `StructuralCtx`; no panic; `Routines` empty.
- [x] I-2 **GREEN**: Modify `application/phase/service.go`: change `buildPriorContext(ctx, c)` → `buildPriorContext(ctx, c, phaseType PhaseType)`; update caller at `service.go:424` to pass `p.Type()`; after loading `structuralCtx` populate `Routines`: always append `graphify.graph_stats` if `GraphSummary != nil`; if phaseType ∈ {EXPLORE, APPLY} also append `graphify.god_nodes` (skip if `GodNodes` empty).
- [x] I-3 **VERIFY**: `go test ./application/phase/...` + `go test ./application/discipline/...` all green.

### Group J — PR2 Checkpoint
Sequential after I.

- [x] J-1 **CHECKPOINT**: `go test ./...` all green; `golangci-lint run ./...` 0 issues.
- [ ] J-2 **COMMIT**: Conventional commit `feat(discipline): concrete RoutineOutput, graph_stats and god_nodes routines from GraphSummary`.
- [ ] J-3 **PR**: Push branch; open PR2 against main. [BLOCKED — awaiting operator push approval]

---

## Strict TDD + Checkpoints Discipline

- Every group follows RED → GREEN → VERIFY order. No GREEN step starts before its RED tests exist and fail.
- Goroutine leak checks in B-1 and C-1 are mandatory (not optional). Use `goleak` or explicit `runtime.NumGoroutine` assertions.
- `-race` flag required for C-3 (concurrent proxy tests).
- `golangci-lint` must be clean at each checkpoint (G-1, J-1) before commit.
- No `Co-Authored-By`, no AI attribution in any commit message.
- `prior_context_test.go:148` marshal update (H-1) MUST land in the same commit as H-3 (struct change).

## Out of Scope Reminders

- HTTP/SSE transport for MCP providers
- `list_prs` and `triage_prs` tool registration
- `affected_nodes` upstream PR
- Routines beyond `graph_stats` and `god_nodes`
- Item #7 GET /usage skill_id
- Idle-TTL reaper goroutine (D-M4-2 rejected)
- Per-change-keyed sessions (no change_id available)
