# Delta: context7-provider-registration

## Capability

Context7 (Upstash MCP) is registered as a second `[[mcp_providers]]` entry in
`sophia-agent-mcp/configs/example.toml`. It reuses the existing `ExternalMCPProxy`
+ `AllowlistEnforcer` + persistent-per-process lifecycle — zero proxy code changes.
The entry pins exactly two tools (`resolve-library-id`, `get-library-docs`) via
`tools_allowed`. The `CONTEXT7_API_KEY` env var is declared in the `[env]` map and
forwarded to the subprocess via the existing env-forwarding path (`config.go:225`,
`wire.go:291-300`, R-1 already merged). If `CONTEXT7_API_KEY` is absent or the
subprocess fails to start, agent-mcp MUST continue serving all other providers
(degraded-first). The two Context7 tools MUST be registered on `buildSDKServer`
as proxy entries so the orchestrator bootstrap service can call them via the MCP
dispatcher.

## ADDED Requirements

### Requirement: Context7 Provider Config Entry

`configs/example.toml` MUST contain a `[[mcp_providers]]` block for Context7 with:
- `id = "context7"` (or the equivalent unique identifier field used by the existing
  graphify entry)
- `command = "npx -y @upstash/context7-mcp@latest"`
- `transport = "stdio"`
- `lifecycle = "spawned_per_change"` (retained as documented intent per the existing
  proxy reinterpretation note — actual lifecycle is persistent-per-process)
- `tools_allowed = ["resolve-library-id", "get-library-docs"]`
- `[env]` block containing `CONTEXT7_API_KEY = "${CONTEXT7_API_KEY}"` (or equivalent
  env-reference syntax supported by the config loader)

MUST NOT add any tools beyond the two listed. MUST NOT register `list_libraries`
or any other Context7 tool. This pin is the structural defense against
ContextCrush-style injection (D-C7-5).

#### Scenario: Config contains the context7 block

- GIVEN `configs/example.toml` is parsed into `[]MCPProviderConfig`
- WHEN the parsed slice is inspected
- THEN exactly one entry has `id = "context7"` (or equivalent)
- AND its `tools_allowed` contains exactly `["resolve-library-id", "get-library-docs"]`
- AND its command is `"npx -y @upstash/context7-mcp@latest"`

#### Scenario: Graphify entry unaffected

- GIVEN `configs/example.toml` is parsed
- WHEN both providers are inspected
- THEN the graphify entry retains its original 8 tools and command unchanged
- AND neither entry interferes with the other's `tools_allowed`

#### Scenario: Config specifies stdio transport

- GIVEN the context7 `MCPProviderConfig` entry
- WHEN the `transport` field is read
- THEN `transport == "stdio"`

### Requirement: SDK Server Proxy Tool Registration

`buildSDKServer` MUST register `context7.resolve-library-id` and
`context7.get-library-docs` as proxy tool entries on the SDK server, routing them
through `ExternalMCPProxy` with `providerID = "context7"`.

The `AllowlistEnforcer` constructed at boot MUST include the context7 provider
with the two-tool allowlist. Any call to a context7 tool not in the allowlist
MUST be rejected by `AllowlistEnforcer.Authorize` before reaching the subprocess.

#### Scenario: Both tools registered on SDK server

- GIVEN `buildSDKServer` is called with the context7 provider config in scope
- WHEN the SDK server's tool list is inspected
- THEN it includes `context7.resolve-library-id` and `context7.get-library-docs`
- AND no other `context7.*` tools are present

#### Scenario: Proxy routes context7 tools through AllowlistEnforcer

- GIVEN a tool call for `context7.resolve-library-id`
- WHEN the call reaches `ExternalMCPProxy`
- THEN `AllowlistEnforcer.Authorize("context7", "resolve-library-id")` returns nil
- AND the call is forwarded to the context7 subprocess

#### Scenario: Unlisted context7 tool rejected

- GIVEN `AllowlistEnforcer` for context7 with `tools_allowed = ["resolve-library-id", "get-library-docs"]`
- WHEN a call for `context7.list_libraries` arrives
- THEN `Authorize` returns `ErrToolNotAllowed`
- AND no subprocess interaction occurs

### Requirement: Degraded-First — Missing API Key

If `CONTEXT7_API_KEY` is absent from the environment when agent-mcp starts,
the context7 subprocess MUST NOT block application startup. The proxy MUST degrade
gracefully: the first tool call to any context7 tool MUST surface a provider-level
error (not a panic) and agent-mcp MUST continue serving all other registered
providers (graphify and any future entries).

MUST NOT fail the agent-mcp health check due to a missing context7 API key.

#### Scenario: Missing API key — agent-mcp still serves graphify

- GIVEN `CONTEXT7_API_KEY` is not set
- WHEN agent-mcp starts and a graphify tool call arrives
- THEN the graphify tool call is served normally
- AND no crash or startup failure occurs

#### Scenario: Missing API key — context7 tool call returns error

- GIVEN `CONTEXT7_API_KEY` is not set
- WHEN a call to `context7.resolve-library-id` is made through the proxy
- THEN the proxy returns an error (not a panic)
- AND the error message references the missing key or subprocess failure

### Requirement: Env Forwarding for API Key

The `CONTEXT7_API_KEY` env var declared in the provider config MUST be forwarded
to the `npx` subprocess via the existing `callerFactory` env-forwarding path
(`wire.go:291-300`, `config.go:225`). No new proxy code changes are required
(env forwarding is already present via R-1).

#### Scenario: API key forwarded to subprocess

- GIVEN `CONTEXT7_API_KEY = "test-key"` in the environment
- AND the context7 provider config declares `CONTEXT7_API_KEY` in its env block
- WHEN the proxy spawns the `npx -y @upstash/context7-mcp@latest` subprocess
- THEN the subprocess receives `CONTEXT7_API_KEY = "test-key"` in its environment
- AND the subprocess initializes successfully
