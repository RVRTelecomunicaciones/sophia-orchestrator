# Delta: allowlist-dispatch-wiring

## Capability

Every proxied tool call passes through `AllowlistEnforcer.Authorize` before the proxy forwards it to the MCP subprocess. The enforcer is constructed from `cfg.MCPProviders` at boot. A tool name absent from the provider's `tools_allowed` list MUST be rejected before any subprocess interaction occurs. An unknown provider ID MUST be rejected with `ErrUnknownProvider`.

## ADDED Requirements

### Requirement: Pre-Forward Authorization

The proxy MUST call `AllowlistEnforcer.Authorize(providerID, toolName)` on every proxied tool call. Authorization MUST occur before any subprocess is spawned or any `CallTool` is forwarded. A tool call that fails authorization MUST NOT reach the MCP subprocess.

#### Scenario: Allowed tool is forwarded

- GIVEN a provider registered in `cfg.MCPProviders` with `tools_allowed = ["graph_stats", "query_graph"]`
- WHEN a call for `graph_stats` arrives
- THEN `Authorize` returns nil
- AND the proxy spawns (or reuses) the session and dispatches `CallTool`

#### Scenario: Disallowed tool is rejected before subprocess

- GIVEN a provider registered with `tools_allowed = ["graph_stats"]`
- WHEN a call for `list_prs` arrives
- THEN `Authorize` returns `ErrToolNotAllowed`
- AND the proxy returns `ErrToolNotAllowed` to the caller without spawning a subprocess or calling `CallTool`

#### Scenario: Unknown provider is rejected

- GIVEN an enforcer constructed from a config map that does not contain provider ID `"unknown-provider"`
- WHEN a call for any tool on `"unknown-provider"` arrives
- THEN `Authorize` returns `ErrUnknownProvider`
- AND no subprocess is spawned

### Requirement: Enforcer Boot Construction

The `AllowlistEnforcer` MUST be constructed from `cfg.MCPProviders` during application boot (wire phase), before any tool call is processed. The enforcer MUST be injected into the proxy at construction time, not created lazily per call.

#### Scenario: Enforcer built from provider config at boot

- GIVEN a config with one or more `MCPProviderConfig` entries, each carrying a `tools_allowed` list
- WHEN the application boots and `wire.go` constructs the proxy
- THEN the `AllowlistEnforcer` is initialized with the full provider-to-tools mapping
- AND the same enforcer instance is shared across all tool calls for the lifetime of the process

#### Scenario: Provider with empty tools_allowed

- GIVEN a provider config entry with an empty `tools_allowed` list
- WHEN any tool call arrives for that provider
- THEN `Authorize` returns `ErrToolNotAllowed` for every tool name
- AND no subprocess is spawned
