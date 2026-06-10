# Delta: mcp-providers-config

## Capability

TOML `mcp_providers[]` array-of-tables schema in `sophia-agent-mcp`. Extends the `Config` struct with `MCPProviders []MCPProviderConfig`, validates required fields at load time, and adds a `tools_allowed` allowlist enforcement middleware in front of MCP tool invocations. No new parser dependency is introduced — the existing BurntSushi/toml decoder handles the new fields automatically.

**Source refs:** proposal §Scope item 4; explore §Item 4 (mcp_providers[] config); explore §Format reality; explore §Approaches considered item 4.

---

## ADDED Requirements

### Requirement: MCPProviderConfig Schema

The system MUST define `MCPProviderConfig` with fields: `id` (string), `package` (string), `command` (string), `transport` (string, enum `{stdio}`), `tools_allowed` (non-empty list of strings), and `lifecycle` (string, enum `{spawned_per_change, spawned_per_session, persistent}`).

#### Scenario: Valid single-provider config loads without error

- GIVEN a TOML config file with one `[[mcp_providers]]` entry containing all required fields and valid enum values
- WHEN the config loader parses the file
- THEN the `Config.MCPProviders` slice contains exactly one `MCPProviderConfig` with all fields populated

#### Scenario: Absent mcp_providers block loads as empty list

- GIVEN a TOML config file with no `[[mcp_providers]]` block
- WHEN the config loader parses the file
- THEN `Config.MCPProviders` is an empty (or nil) slice
- AND no error is returned (backward compatible)

---

### Requirement: Loader Validation at Load Time

The system MUST validate every `MCPProviderConfig` entry at load time and MUST return an error — not panic — if any entry is invalid.

#### Scenario: Empty id rejected

- GIVEN a `[[mcp_providers]]` entry with `id = ""`
- WHEN the config loader validates the entry
- THEN an error is returned identifying the invalid entry
- AND the agent does not start

#### Scenario: Unknown transport rejected

- GIVEN a `[[mcp_providers]]` entry with `transport = "grpc"`
- WHEN the config loader validates the entry
- THEN an error is returned stating the transport is not in the allowed set `{stdio}`

#### Scenario: Unknown lifecycle rejected

- GIVEN a `[[mcp_providers]]` entry with `lifecycle = "on_demand"`
- WHEN the config loader validates the entry
- THEN an error is returned stating the lifecycle is not in the allowed set

#### Scenario: Empty tools_allowed rejected

- GIVEN a `[[mcp_providers]]` entry with `tools_allowed = []`
- WHEN the config loader validates the entry
- THEN an error is returned requiring at least one tool to be listed

---

### Requirement: tools_allowed Allowlist Enforcement

The system MUST enforce `tools_allowed` on every MCP tool invocation. An invocation of a tool not listed in `tools_allowed` for the relevant provider MUST be rejected before the tool executes.

#### Scenario: Tool in allowlist passes

- GIVEN an `MCPProviderConfig` with `tools_allowed = ["query_graph", "get_node"]`
- WHEN an MCP tool invocation for `query_graph` is received
- THEN the invocation is forwarded to the provider

#### Scenario: Tool outside allowlist rejected at invocation

- GIVEN an `MCPProviderConfig` with `tools_allowed = ["query_graph"]`
- WHEN an MCP tool invocation for `delete_graph` is received
- THEN the allowlist middleware returns an error before the tool executes
- AND the provider is not contacted

---

### Requirement: No New Parser Dependency

The system MUST NOT introduce a new parser library. The existing `BurntSushi/toml` decoder MUST handle `mcp_providers[]` field parsing by struct tag alone.

#### Scenario: Struct tag is sufficient for array-of-tables decoding

- GIVEN `MCPProviders []MCPProviderConfig` is added to `Config` with a `toml:"mcp_providers"` tag
- WHEN the TOML decoder parses a file with `[[mcp_providers]]` entries
- THEN each entry is decoded into an `MCPProviderConfig` without any custom decoder code
