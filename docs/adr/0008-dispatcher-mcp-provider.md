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

## References

- ADR-0002: Pluggable dispatcher abstraction
- ADR-0007: Multi-LLM dispatcher factory (V2.0)
- `sophia-agent-mcp` ADR-0001: MCP library and spec revision selection
- Engram charter: `sdd/mcp-host-bridge/charter` (observation #517)
- Spec scenarios: A1–A10, C1–C6, E1–E4
- Design: `openspec/design.md` §1, §6, §7, §11
