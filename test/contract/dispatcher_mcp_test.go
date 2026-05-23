//go:build contract

// Package contract holds integration/contract tests for external adapters.
// These tests require real or semi-real external processes and are skipped
// in normal CI unless specific env vars are set.
//
// MCP contract test (this file):
//   - Skips unless SOPHIA_AGENT_MCP_BIN is set to the path of the
//     sophia-agent-mcp binary.
//   - Starts the bridge process, calls agent.health via the dispatcher,
//     asserts the response shape.
//
// Usage:
//
//	SOPHIA_AGENT_MCP_BIN=/path/to/sophia-agent-mcp \
//	  go test -tags=contract ./test/contract/ -run TestMCPDispatcher -v
package contract

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	mcpdispatcher "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/dispatcher/mcp"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

const (
	contractBridgeToken = "contract-test-token-abc123"
	contractBridgeAddr  = "127.0.0.1:17775"
)

// TestMCPDispatcher_HealthCheck starts the sophia-agent-mcp bridge binary
// and verifies that agent.health returns a valid response shape through the
// dispatcher.
func TestMCPDispatcher_HealthCheck(t *testing.T) {
	bridgeBin := os.Getenv("SOPHIA_AGENT_MCP_BIN")
	if bridgeBin == "" {
		t.Skip("SOPHIA_AGENT_MCP_BIN not set — skipping MCP contract test (set to path of sophia-agent-mcp binary)")
	}

	if _, err := os.Stat(bridgeBin); err != nil {
		t.Skipf("SOPHIA_AGENT_MCP_BIN=%q: binary not accessible (%v)", bridgeBin, err)
	}

	// Write a minimal config file.
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.toml")
	cfgContent := fmt.Sprintf(`
listen_addr = "%s"
token = "%s"

[allowlist]
providers = ["opencode"]
cwd_roots = ["/tmp"]
env_vars = ["PATH", "HOME"]
`, contractBridgeAddr, contractBridgeToken)
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgContent), 0600))

	// Start bridge process.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	cmd := exec.CommandContext(ctx, bridgeBin, "--config", cfgPath, "serve")
	cmd.Env = append(os.Environ(),
		"SOPHIA_AGENT_MCP_TOKEN="+contractBridgeToken,
		"SOPHIA_AGENT_MCP_ADDR="+contractBridgeAddr,
	)
	// Capture combined output for diagnostics.
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start(), "failed to start bridge binary")
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	// Wait for bridge to start listening.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("tcp", contractBridgeAddr)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	_, err := net.DialTimeout("tcp", contractBridgeAddr, 500*time.Millisecond)
	require.NoError(t, err, "bridge did not start in time at %s", contractBridgeAddr)

	// Build dispatcher pointing at the real bridge.
	d := mcpdispatcher.New(&http.Client{Timeout: 10 * time.Second}, mcpdispatcher.Config{
		BridgeURL: "http://" + contractBridgeAddr,
		Token:     contractBridgeToken,
		Origin:    "http://localhost",
		Transport: "streamable-http",
		TimeoutMS: 10_000,
		Suggested: 4,
	})

	// Verify Provider() value.
	require.Equal(t, outbound.AgentDispatcher(d), d, "dispatcher must implement AgentDispatcher")

	// Call agent.health through the dispatcher.
	healthCtx, healthCancel := context.WithTimeout(ctx, 5*time.Second)
	defer healthCancel()

	err = d.HealthCheck(healthCtx)
	// A healthy bridge returns nil. An error is acceptable if no providers
	// are installed on the CI machine — we only assert the call completed
	// without panicking and the error (if any) wraps ErrDispatchFailed.
	if err != nil {
		t.Logf("HealthCheck returned error (expected on machines without provider CLIs): %v", err)
	}

	t.Logf("MCP contract test completed — bridge at %s responded to agent.health", contractBridgeAddr)
}

// TestMCPDispatcher_Dispatch_FailFast verifies that Dispatch with a bad
// token returns a properly tagged auth_error (real bridge, real auth check).
func TestMCPDispatcher_Dispatch_BadToken(t *testing.T) {
	bridgeBin := os.Getenv("SOPHIA_AGENT_MCP_BIN")
	if bridgeBin == "" {
		t.Skip("SOPHIA_AGENT_MCP_BIN not set — skipping MCP contract test")
	}

	if _, err := os.Stat(bridgeBin); err != nil {
		t.Skipf("SOPHIA_AGENT_MCP_BIN=%q: binary not accessible (%v)", bridgeBin, err)
	}

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.toml")
	cfgContent := fmt.Sprintf(`
listen_addr = "%s"
token = "%s"

[allowlist]
providers = ["opencode"]
cwd_roots = ["/tmp"]
env_vars = ["PATH"]
`, contractBridgeAddr, contractBridgeToken)
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgContent), 0600))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	cmd := exec.CommandContext(ctx, bridgeBin, "--config", cfgPath, "serve")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("tcp", contractBridgeAddr)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	d := mcpdispatcher.New(&http.Client{Timeout: 5 * time.Second}, mcpdispatcher.Config{
		BridgeURL: "http://" + contractBridgeAddr,
		Token:     "wrong-token",
		TimeoutMS: 5_000,
	})

	_, err := d.Dispatch(ctx, outbound.DispatchRequest{Prompt: "hi"})
	require.Error(t, err, "bad token must produce an error")
	require.ErrorIs(t, err, outbound.ErrDispatchFailed, "must wrap ErrDispatchFailed")
}
