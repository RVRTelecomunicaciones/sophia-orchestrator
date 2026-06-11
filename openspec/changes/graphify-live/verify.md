# Verify: graphify-live (M4)

## Verdict

**PASS.** Both PRs (agent-mcp #19 merged to main; orch #88 merged to main 656f559) faithfully implement the proposal as reconciled by design D-M4-1..8 plus the 4 verified code-reality corrections. All 5 capabilities are fully covered with test evidence. Zero CRITICAL findings. All HARD operator invariants hold. Strict-TDD evidence present for both PRs. Recommendation: proceed to `sdd-archive`.

- CRITICAL: 0
- WARNING: 2
- SUGGESTION: 3

> Note: tests were NOT executed (CI green per operator handoff). Verification is by reading the merged source, the test corpus, and the commit history.

## Coverage Matrix (capability → requirement → file:line)

### Capability 1 — mcp-stdio-client-adapter (PR1)
| Requirement | Evidence | Status |
|---|---|---|
| Subprocess Connect (CommandTransport + handshake, session held) | `mcpclient/client.go:75-90` (`Connect`, `client.Connect`, stores `c.session`) | PASS |
| Startup timeout typed, no leak | `client.go:76-78` (`context.WithTimeout`), `client.go:127-133` (default 10), test `client_test.go:82 TestConnect_StartupTimeout` | PASS |
| Executable not found → immediate err, no leak | `client.go:84-87` wraps connect error; degenerate argv `client.go:48-50` | PASS |
| CallTool round-trip, no mutation | `client.go:97-109` (delegates to `session.CallTool`, returns result unchanged) | PASS |
| IsError:true propagated as result (not Go error) | `client.go:101-108` returns result; test `client_test.go:141 TestCallTool_ErrorResult` | PASS |
| Clean Close (session.Close → SIGTERM/SIGKILL), no leak | `client.go:114-122`; tests `client_test.go:162 TestClose_NoLeak`, `:188 TestClose_AfterCtxCancel` | PASS |

### Capability 2 — external-mcp-proxy (PR1, per RECONCILED spec D-M4-2 persistent-per-process)
| Requirement | Evidence | Status |
|---|---|---|
| Provider config resolution by ID at boot; unknown → ErrUnknownProxy w/o spawn | `proxy.go:76-79` cfgMap by ID; Authorize-first `proxy.go:103-105` surfaces ErrUnknownProvider before spawn; test `proxy_test.go:244 TestProxy_UnknownProvider` | PASS |
| Lazy spawn first call | `proxy.go:120-141 getOrSpawn`; test `proxy_test.go:108 TestProxy_LazySpawn_FirstCall` | PASS |
| Subsequent calls reuse cached session | `proxy.go:128-130`; test `proxy_test.go:123 TestProxy_ReuseSession` | PASS |
| Concurrent calls on same session safe | dispatch outside mutex `proxy.go:113-114`; test `proxy_test.go:139 TestProxy_ConcurrentCalls_Safe` (-race) | PASS |
| Concurrent first calls spawn exactly once | spawn under mutex `proxy.go:121-141`; test `proxy_test.go:169 TestProxy_ConcurrentFirstCalls_SpawnOnce` | PASS |
| Reap on App.Stop; cache cleared | `proxy.go:145-162` (`closed` flag, iterate + `delete`); test `proxy_test.go:198 TestProxy_Close_ReapsSession` | PASS |
| Close guarded on ctx cancel — no leak | `proxy.go:149-152` idempotent close; test `proxy_test.go:212 TestProxy_Close_GuardedNoLeak` | PASS |
| Close with no session — noop | `proxy.go:149-151` + empty loop; test `proxy_test.go:233 TestProxy_Close_WithoutSpawn_Noop` | PASS |
| Stale-data freshness via sidecar hot-reload | No freshness mgmt in proxy (none present); documented `external-mcp-proxy/spec.md:94-103` + design D-M4-2 | PASS (by design — graphify-native mtime) |

### Capability 3 — allowlist-dispatch-wiring (PR1)
| Requirement | Evidence | Status |
|---|---|---|
| Pre-forward Authorize on EVERY call, before spawn | `proxy.go:101-105` (`enforcer.Authorize` is step 1, before getOrSpawn) | PASS |
| Allowed tool forwarded | test `proxy_test.go:260 TestAuthorize_AllowedTool_Forwarded` | PASS |
| Disallowed tool → ErrToolNotAllowed, no spawn | `Authorize` returns before getOrSpawn; `allowlist.go:64`; test `proxy_test.go:274 TestAuthorize_DisallowedTool_Rejected` | PASS |
| Unknown provider → ErrUnknownProvider, no spawn | `allowlist.go:61`; test `proxy_test.go:314 TestAuthorize_UnknownProvider` | PASS |
| Empty tools_allowed → reject all | test `proxy_test.go:290 TestAuthorize_EmptyAllowlist_RejectsAll` | PASS |
| Enforcer built from cfg.MCPProviders at boot, shared instance | `wire.go:273 NewAllowlistEnforcer(cfg.MCPProviders)`, injected `wire.go:296 mcpproxy.New(...)` | PASS |

### Capability 4 — graphify-provider-registration (PR1)
| Requirement | Evidence | Status |
|---|---|---|
| graphify [[mcp_providers]] block, 8 tools exact, no list_prs/triage_prs | `configs/example.toml:229-248`; grep confirms 8 + none | PASS |
| stdio transport + spawned_per_change label | `example.toml:233-234` | PASS |
| 8 tools registered as `<provider>.<tool>` on buildSDKServer | `server.go:308-339` (`qualifiedName = providerID + "." + toolName`); test `server_test.go:407 TestBuildSDKServer_ProxyTools_Registered` | PASS |
| Proxy tools route through ExternalMCPProxy (raw tool fwd) | `server.go:334 s.proxy.CallTool(ctx, providerID, toolName, args)` | PASS |
| Native agent.run / agent.health unaffected | `server.go:277-307` native first; test `server_test.go:424 TestBuildSDKServer_NativeTools_Unaffected` | PASS |
| list_prs / triage_prs absent from server | test `server_test.go:435 TestBuildSDKServer_NoPRTools` | PASS |

### Capability 5 — routines-layer-concrete (PR2)
| Requirement | Evidence | Status |
|---|---|---|
| RoutineOutput concrete {Source,Content}, marshal `{"source":"","content":""}` | `prior_context.go:104-108` (json:"source"/"content", NO omitempty); test `prior_context_test.go:148-150` updated atomically | PASS |
| buildPriorContext populates graph_stats (all phases) | `service.go:1119-1124`; test `prior_context_routines_test.go:129 TestBuildPriorContext_GraphStats_AllPhases` | PASS |
| god_nodes EXPLORE+APPLY only, empty-skip | `service.go:1126-1131`; test `:178 TestBuildPriorContext_GodNodes_ExploreApplyOnly` | PASS |
| nil GraphSummary / nil StructuralCtx → empty, no panic | `service.go:1118-1119` guard; tests `:253 NilGraphSummary`, `:278 NilStructuralCtx` | PASS |
| Render emits Routines Layer 5.5 (after BusinessRules, before PhaseIdentity) w/ attribution | `prior_context.go:211-226` ordering; `renderRoutines:325-335` (`## Routine: <source>`) | PASS |
| Empty routines → no output | `prior_context.go:217 len>0` + `:218 body!=""` guard | PASS |
| Zero subprocess | grep: no `os/exec` in prior_context.go or service.go routines path | PASS |

## CRITICAL

None.

## WARNING

- **W-1 (PR1, design-deviation, accepted)**: Spawn happens WHILE the mutex is held (`proxy.go:121-141 getOrSpawn`), not the design's "unlock → spawn → relock" (design.md D-M4-2 narrative, tasks.md C-2). Consequence: a slow `Connect` for one provider serializes first-calls for ALL providers (single global mutex, no per-provider lock). For the single-provider (graphify) M4 deployment this is harmless and the in-mutex spawn is what guarantees the spawn-once invariant cleanly. Flagging because the design text said otherwise and a future multi-provider deployment with slow startups would feel head-of-line blocking. Not blocking.
- **W-2 (PR1, schema-stub)**: Proxy tools register a placeholder InputSchema `{"type":"object","properties":{}}` (`server.go:322`) for all 8 tools. The LLM agent sees no argument hints for graphify tools; it must know graphify's tool args out-of-band. Functionally correct (args pass through as a free-form map) but degrades tool-use ergonomics. Backlog candidate: hydrate InputSchema from graphify's own `tools/list` on first connect.

## SUGGESTION

- **S-1**: `AllowlistEnforcer` lives in `internal/infrastructure/mcp` (not `application` as the proposal's `allowlist.go:58` shorthand implied). Placement is fine (it is config-derived infra policy), but the proposal/affected-areas text should be reconciled to avoid future confusion. The line number 58 for `Authorize` did hold.
- **S-2**: `mcpclient.New` empty-command degenerate path (`client.go:48-50`) builds `exec.Command("")` and defers failure to Connect. A direct typed "empty command" error at construction would fail faster and read clearer. Cosmetic.
- **S-3**: `renderRoutines` emits no header when `attr=false` (`prior_context.go:328-330`) — routines content then concatenates with only `\n\n` separators, indistinguishable from adjacent layers in no-attribution mode. Matches existing layer-render convention, but worth a golden test asserting the no-attr shape for routines specifically (current render tests are Group H attribution-on).

## Acceptance Criteria

| # | Criterion (proposal, adjusted by D-M4-2) | Result | Evidence |
|---|---|---|---|
| PR1-1 | Proxy spawns graphify serve lazily on first call | PASS | proxy.go:120-141 + lazy-spawn test |
| PR1-2 | 8 tools callable as `<provider>.<tool>` through agent-mcp | PASS | server.go:308-339 + ProxyTools_Registered |
| PR1-3 | Tool outside allowlist → ErrToolNotAllowed | PASS | allowlist.go:64 + DisallowedTool_Rejected |
| PR1-4 | Subprocess reaped on App.Stop (RECONCILED: not change-close) | PASS | wire.go:346-356 + Close_ReapsSession |
| PR1-5 | startup_timeout_s honored | PASS | config.go:219 + client.go:76-78,127-133 |
| PR1-6 | No goroutine/process leak (test evidence) | PASS | Close_GuardedNoLeak, Client TestClose_NoLeak/AfterCtxCancel |
| PR1-7 | lint + tests green | PASS (CI) | not re-run; checkpoints G-1/G-2 [x] |
| PR2-1 | Routines populated from GraphSummary | PASS | service.go:1119-1131 |
| PR2-2 | Render emits with attribution | PASS | prior_context.go:216-221, 325-335 |
| PR2-3 | nil GraphSummary → empty (degraded safe) | PASS | service.go:1118-1119 + nil tests |
| PR2-4 | god_nodes gated to EXPLORE+APPLY | PASS | service.go:1126 |
| PR2-5 | marshal test updated | PASS | prior_context_test.go:148-150 |
| PR2-6 | lint + tests green | PASS (CI) | not re-run; checkpoint J-1 [x] |

## Operator Invariants (HARD)

| Invariant | Result | Evidence |
|---|---|---|
| Conventional commits, both repos | PASS | 6 PR1 + 2 PR2 commits all `feat(scope):` |
| NO Co-Authored-By / AI attribution | PASS | git log scan both ranges — author Russell, zero AI markers |
| Strict-TDD evidence | PASS | RED test funcs exist for every GREEN unit; leak + -race tests present |
| D-M4-2 enforced — NO change_id in proxy code | PASS | grep: only hit is a comment "No change_id key" |
| 8 tools exactly in example.toml | PASS | grep count 8; list_prs/triage_prs absent |
| Zero subprocess in orch routines path | PASS | grep: no os/exec in prior_context.go / service.go |
| AllowlistEnforcer.Authorize before every forward | PASS | proxy.go:103-105 step 1 before getOrSpawn |

## Adaptations (apply-report + observed)

- **Env field addition**: `MCPProviderConfig.Env map[string]string` (`config.go:225`) landed alongside `StartupTimeoutS` (`config.go:219`). Not in original design file-changes table; additive, backward compatible, consumed by the example.toml `[mcp_providers.env]` block (GRAPHIFY_QUERY_LOG_DISABLE=1). Justified — design D-M4-5 named the env var, the field operationalizes it. Note: wire.go callerFactory (`wire.go:283-293`) does NOT yet forward `provCfg.Env` into `mcpclient.New` (which takes only command + timeout). See R-1.
- **callerFactory signature evolution**: design interface seam named `mcpToolCaller`; implementation exposes exported `ToolCaller` (`proxy.go:29`) + a `callerFactory func(ctx, providerID) (ToolCaller, error)` injected via `WithCallerFactory` option (`proxy.go:62`), with a panic-guard default factory (`proxy.go:168-172`). Cleaner DI than the design sketch; production factory in wire.go:283.
- **SDK gotchas (apply-progress)**: go-sdk v1.6.1 `mcp.NewClient` + `CommandTransport` + `client.Connect` verified working; `ClientSession` concurrency-safe (relied on for dispatch-outside-mutex). PR2 marshal gotcha (`{}` → `{"source":"","content":""}`) fixed atomically in the same commit as the struct change (commit 3bce8b9).

## Risks for Backlog

- **R-1**: `MCPProviderConfig.Env` is parsed but NOT forwarded to the spawned subprocess — `mcpclient.New(command, timeoutS)` (`client.go:45`) has no env parameter and `wire.go:288` drops `provCfg.Env`. So `GRAPHIFY_QUERY_LOG_DISABLE=1` in example.toml is currently inert. Low impact for M4 (graphify works without it), but the documented intent is unmet. Wire env through `exec.Cmd.Env` in a follow-up.
- **R-2** (W-2): Placeholder InputSchema on proxy tools — hydrate from graphify `tools/list` for better LLM ergonomics.
- **R-3** (W-1): Global proxy mutex serializes first-call spawns across providers — revisit with per-provider locks if/when multi-provider with slow startups ships.

## Recommendation

**Proceed to `sdd-archive`.** All 5 capabilities pass, all HARD invariants hold, all acceptance criteria met (CI-green for lint/test, not re-run per handoff). The 2 WARNINGs and R-1 are non-blocking backlog items; R-1 (inert Env) is the one worth tracking because it is a silent no-op of a documented config field.
