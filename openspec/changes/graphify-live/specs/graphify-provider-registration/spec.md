# Delta: graphify-provider-registration

## Capability

A graphify provider entry in `configs/example.toml` declares the subprocess command, transport type, lifecycle, and the 8 allowed tool names. The same 8 tools are registered on `buildSDKServer` as proxy entries so the dispatched LLM agent can call them. `list_prs` and `triage_prs` MUST NOT appear in either the config or the server registration.

## ADDED Requirements

### Requirement: Graphify Provider Config Entry

`configs/example.toml` MUST contain a `[[mcp_providers]]` block for graphify. The block MUST specify: `package = "graphifyy[mcp]==0.8.35"`, `command = "graphify serve graphify-out/graph.json"`, `transport = "stdio"`, `lifecycle = "spawned_per_change"`, and `tools_allowed` listing exactly the 8 tools: `query_graph`, `get_node`, `get_neighbors`, `get_community`, `god_nodes`, `graph_stats`, `shortest_path`, `get_pr_impact`.

#### Scenario: Config contains the graphify block

- GIVEN `configs/example.toml` is parsed into `[]MCPProviderConfig`
- WHEN the parsed slice is inspected
- THEN exactly one entry has `id = "graphify"` (or equivalent unique identifier)
- AND its `tools_allowed` contains exactly the 8 listed tools
- AND `list_prs` and `triage_prs` are absent from `tools_allowed`

#### Scenario: Config specifies stdio lifecycle

- GIVEN the graphify `MCPProviderConfig` entry
- WHEN the `transport` and `lifecycle` fields are read
- THEN `transport` is `"stdio"` and `lifecycle` is `"spawned_per_change"`

### Requirement: SDK Server Proxy Tool Registration

`buildSDKServer` MUST register each of the 8 graphify tools as a proxy tool entry on the SDK server. Each registration MUST map the tool name to the `ExternalMCPProxy` dispatch path so the LLM agent can invoke them as first-class SDK tools.

#### Scenario: All 8 tools registered on SDK server

- GIVEN `buildSDKServer` is called with the graphify provider config in scope
- WHEN the resulting SDK server's tool list is inspected
- THEN it includes all 8 tools: `query_graph`, `get_node`, `get_neighbors`, `get_community`, `god_nodes`, `graph_stats`, `shortest_path`, `get_pr_impact`
- AND `list_prs` and `triage_prs` are absent

#### Scenario: Proxy tools route through ExternalMCPProxy

- GIVEN the SDK server has been built with the 8 proxy registrations
- WHEN the LLM agent calls any of the 8 tools
- THEN the call is routed to `ExternalMCPProxy` (which applies AllowlistEnforcer then dispatches to the subprocess)
- AND the result is returned to the caller unchanged

#### Scenario: Existing non-proxy tools unaffected

- GIVEN `buildSDKServer` registers `agent.run` and `agent.health` before this change
- WHEN the 8 graphify proxy tools are added
- THEN `agent.run` and `agent.health` remain registered and functional
- AND their behavior is unchanged
