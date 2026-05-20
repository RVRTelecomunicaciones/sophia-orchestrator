# ADR-0008 — MCP Dispatcher Provider (V2.1)

- **Status**: accepted
- **Date**: 2026-05-20
- **Deciders**: Russell Vergara

## Context

ADR-0002 defined `outbound.AgentDispatcher` as a provider-agnostic port and
explicitly reserved MCP as a V2 option. ADR-0007 introduced the dispatcher
factory pattern so new providers can be wired without touching service code.

V2.1 activates the MCP path. The concrete motivation is that `sophia-orchestator`
runs inside Docker and cannot reach the user's macOS Keychain, so provider CLIs
(`opencode`, `claude`, `codex`, `gemini`) that rely on Keychain-backed OAuth
tokens cannot be invoked from inside the container. The solution is a host-side
bridge process (`sophia-agent-mcp`) that runs as the user, holds all credentials,
and exposes a minimal MCP surface to the orchestrator.

The trust boundary is intentional and non-negotiable per the architectural charter
(Engram `sdd/mcp-host-bridge/charter`): the bridge is a HOST-TRUST binary. It
controls allowlists, token storage, subprocess lifecycle, and log redaction. The
orchestrator remains a pure consumer of the `AgentDispatcher` interface —
it never inspects provider CLIs, never manages tokens, and never reads Keychain.

This ADR documents:
- why MCP was chosen as the transport between orchestrator and bridge
- the security baseline required at V1 (not deferred)
- the error taxonomy that freezes the `dispatcher/mcp` contract surface
- why `sophia-runtime-adapters` is intentionally untouched

Relevant spec scenarios: E3, A9, A10.

## Decision

1. **New adapter**: `internal/adapters/outbound/dispatcher/mcp/dispatcher.go`
   implements `outbound.AgentDispatcher` using Streamable HTTP (`POST /mcp`)
   against the bridge URL. The library is the official
   `github.com/modelcontextprotocol/go-sdk v1.6.x` (see `sophia-agent-mcp`
   ADR-0001 for evidence). The orchestrator side uses the SDK's client
   transport only — it does NOT run an MCP server.

2. **Opt-in only**: `SOPHIA_DISPATCHER_PROVIDER=mcp` must be set explicitly.
   The default remains `opencode`. Existing env vars and behavior are
   byte-identical when the MCP provider is not selected (spec scenario A9).

3. **Fail-fast validation**: when `provider=mcp`, bootstrap validates at
   startup that `SOPHIA_MCP_BRIDGE_URL` and `SOPHIA_MCP_BRIDGE_TOKEN` are
   non-empty and that `SOPHIA_MCP_TRANSPORT` equals `streamable-http`. Missing
   any required value aborts with a clear error before the serving loop starts
   (spec scenario A10).

4. **Error taxonomy**: the dispatcher maps all bridge failure classes to wrapped
   `outbound.ErrDispatchFailed` with a structured tag:

   | Bridge failure | Tag |
   |---|---|
   | Network / non-2xx / framing | `transport_error` |
   | HTTP 401/403 | `auth_error` |
   | Provider rejected / binary missing / timeout | `provider_error` |
   | Missing or invalid `envelope_raw` | `envelope_error` |
   | Unknown provider (pre-call) | `ErrUnknownDispatcherProvider` |

   The taxonomy is frozen at V1. The verify phase asserts all four tags via
   contract tests in `test/contract/dispatcher_mcp_test.go`.

5. **`sophia-runtime-adapters` stays untouched**: the MCP transport does not
   use `shell.exec` or any runtime-adapters capability. The bridge handles
   subprocess execution on the host side. This satisfies the inversion-of-control
   principle: the orchestrator controls WHAT runs; the bridge controls HOW the
   host CLI is invoked.

6. **`SuggestedMaxConcurrent` = 1 (V1)**: the bridge is single-host and
   serial-friendly. The factory's Spawn Governor still applies; this value
   prevents thundering-herd against a local bridge.

7. **`HealthCheck`**: calls `agent.health` tool on the bridge and returns nil
   on `status="ok"`, non-nil on any error or timeout. Wired via bootstrap to
   the operator health endpoint.

## Consequences

### Positive

- The Docker→host credential boundary is solved without exposing the Keychain
  to the container or relaxing container isolation.
- Adding the MCP provider is additive: one new adapter package, one config
  struct, one bootstrap wiring, zero changes to `service.go` or domain code.
- The factory pattern from ADR-0007 absorbs the new provider with no call-site
  changes.
- Rollback is a single env-var change: `SOPHIA_DISPATCHER_PROVIDER=opencode`
  (or unset it). No migration required.

### Negative

- V1 is limited to Streamable HTTP. If an operator runs the bridge on a
  separate machine, TLS must be configured out-of-band (the bridge listens
  on loopback only by default, so cross-machine use is explicitly unsupported
  in V1 and requires operator attestation).
- `SuggestedMaxConcurrent = 1` is conservative. Parallelism can be raised via
  config once bridge throughput is empirically verified.
- macOS Docker→host networking relies on `host.docker.internal` (Docker
  Desktop magic DNS). Linux deployments are documentation-only in V1 (no
  automatic `extra_hosts` injection in CI).

### Neutral

- `session.Provider` gains a new `ProviderMCP` value. Existing session records
  are unaffected; the field was already a string enum.
- No DB migration, no envelope schema change, no SSE event type change.

## Alternatives considered

- **Inject host credentials into the container via Docker volume / env var**:
  rejected — key material would be accessible from inside the container; breaks
  the Keychain trust model on macOS.
- **Run the orchestrator on the host directly (no Docker)**:
  rejected — operators rely on the containerised deployment for isolation,
  reproducibility, and multi-repo worktree management.
- **gRPC instead of MCP over HTTP**:
  rejected — MCP is the established protocol for agent tool invocation; using
  it here reuses the official SDK without introducing a second RPC stack.
- **`mcp-go` (community `v0`) instead of the official SDK**:
  rejected — the official SDK is GA, documents exact spec-version compatibility,
  and wins on governance. `mcp-go` remains the named fallback if the official SDK
  stalls (see `sophia-agent-mcp` ADR-0001).

## Smoke test procedure

Run these commands on macOS to verify the integration end-to-end. Each
step has an expected output; if any step deviates, stop and diagnose
before continuing.

**Step 1 — Build and start the bridge**

```bash
# Build
cd ~/Documents/2026/sophia-agent-mcp
make build

# Rotate token and save to a temp file
./bin/sophia-agent-mcp token rotate > /tmp/sophia-mcp-token
# Expected output (to file): 64 hex characters, e.g.
#   a3f2...8b1c

export SOPHIA_MCP_BRIDGE_TOKEN=$(cat /tmp/sophia-mcp-token)

# Start the bridge (background)
./bin/sophia-agent-mcp serve --config configs/example.toml &
# Expected log line:
#   {"level":"INFO","msg":"MCP server listening","addr":"127.0.0.1:7775"}
```

**Step 2 — Probe: unauthenticated request → 401**

```bash
curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:7775/mcp
# Expected: 401
```

**Step 3 — Probe: tools/list returns agent.run + agent.health only**

```bash
TOKEN=$(cat /tmp/sophia-mcp-token)
curl -s \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}' \
  http://127.0.0.1:7775/mcp
# Expected JSON snapshot (key fields):
# {
#   "jsonrpc": "2.0",
#   "id": 1,
#   "result": {
#     "protocolVersion": "2025-06-18",
#     "serverInfo": {"name": "sophia-agent-mcp", ...}
#   }
# }

curl -s \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
  http://127.0.0.1:7775/mcp
# Expected JSON snapshot:
# {
#   "jsonrpc": "2.0",
#   "id": 2,
#   "result": {
#     "tools": [
#       {"name": "agent.run",    ...},
#       {"name": "agent.health", ...}
#     ]
#   }
# }
# Tools MUST be exactly these two — no generic shell tool present.
```

**Step 4 — Probe: agent.health returns ok**

```bash
curl -s \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"agent.health","arguments":{}}}' \
  http://127.0.0.1:7775/mcp
# Expected JSON snapshot (key fields):
# {
#   "result": {
#     "content": [{"text": "{\"status\":\"ok\", \"providers\":[...]}"}]
#   }
# }
```

**Step 5 — Start stack with MCP overlay**

```bash
# Token must still be exported in this shell
docker compose \
  -f ops/local/compose.full-stack.yaml \
  -f ops/local/compose.mcp.yaml \
  up -d --build
# Expected orchestrator log line:
#   {"msg":"dispatcher registered","provider":"mcp","bridge_url":"http://host.docker.internal:7775"}
```

**Step 6 — Verify dispatcher via orchestrator API**

```bash
curl -s \
  -H "X-API-Key: full-stack-key" \
  http://localhost:8080/api/v1/ready
# Expected: HTTP 200 {"status":"ok"} (confirms pg reachable)

# Send a minimal sophia run request to exercise the MCP path end-to-end.
# Replace <project-id> with a valid project UUID from your local DB.
# The response should complete a phase and return envelope_raw.
```

## Linux notes

On Linux hosts running Docker Engine (without Docker Desktop), the DNS
name `host.docker.internal` is not automatically injected. To enable
container→host connectivity, add the following to the `orchestator` service
block in your local override:

```yaml
services:
  orchestator:
    extra_hosts:
      - "host.docker.internal:host-gateway"
```

This configuration is **documented here for operator reference only**.
It is **NOT** wired into `compose.mcp.yaml` and **NOT** run in CI.
macOS via Docker Desktop is the sole validated target in V1 (operator
decision #4, 2026-05-20). Linux support is best-effort and requires
operator attestation before production use.

The `compose.mcp.yaml` overlay includes a comment block documenting the
`extra_hosts` line for Linux operators who need it.

## References

- ADR-0002: Pluggable dispatcher abstraction
- ADR-0007: Multi-LLM dispatcher factory (V2.0)
- `sophia-agent-mcp` ADR-0001: MCP library and spec revision selection
- Engram charter: `sdd/mcp-host-bridge/charter` (observation #517)
- Spec scenarios: A1–A10, C1–C6, E1–E4
- Design: `openspec/design.md` §1, §6, §7, §11
- E2E overlay: `ops/local/compose.mcp.yaml`
- Human validation checklist: `openspec/m3-smoke-checklist.md`
