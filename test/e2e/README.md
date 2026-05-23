# E2E Tests

End-to-end tests that require a live stack. These are **not** run by default CI.

## MCP host-bridge e2e (`e2e_mcp` build tag)

Tests the full path: orchestator → MCP dispatcher → sophia-agent-mcp bridge → provider subprocess.

### Run

```bash
export SOPHIA_MCP_BRIDGE_URL=http://127.0.0.1:7775
export SOPHIA_MCP_BRIDGE_TOKEN=$(cat ~/.config/sophia-agent-mcp/token)
export SOPHIA_MCP_PROVIDER=opencode

go test -tags=e2e_mcp ./test/e2e/...
```

### Prerequisites

1. `sophia-agent-mcp` running on localhost:
   ```bash
   ./bin/sophia-agent-mcp --config ~/.config/sophia-agent-mcp/config.toml serve
   ```

2. All three env vars set (test skips if any is missing).

### What it checks (M4)

- `TestE2E_MCP_PhaseDoesNotBlockOnMissingProvider` — dispatches a minimal
  `agent.run` call and asserts it does NOT fail with `ErrProviderEmpty`.
  Other failures (subprocess error, model API unreachable) are logged but
  do not fail the test — they indicate downstream issues, not the BUG-6b fix.
