//go:build e2e_mcp

// Package e2e contains end-to-end tests for the MCP host-bridge integration.
// Run with: go test -tags=e2e_mcp ./test/e2e/...
//
// Required env vars (test is SKIPPED when any is absent):
//   - SOPHIA_MCP_BRIDGE_URL   — e.g. http://127.0.0.1:7775
//   - SOPHIA_MCP_BRIDGE_TOKEN — bearer token printed by `sophia-agent-mcp token rotate`
//   - SOPHIA_MCP_PROVIDER     — e.g. opencode
package e2e

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpdispatcher "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/dispatcher/mcp"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// TestE2E_MCP_PhaseDoesNotBlockOnMissingProvider (M4)
// Asserts that a real dispatch through the live bridge completes without a
// provider_error caused by a missing "provider" argument. The phase is
// allowed to fail for other reasons (e.g. model unreachable, subprocess
// error) but it must NOT block with ErrProviderEmpty.
func TestE2E_MCP_PhaseDoesNotBlockOnMissingProvider(t *testing.T) {
	bridgeURL := os.Getenv("SOPHIA_MCP_BRIDGE_URL")
	bridgeToken := os.Getenv("SOPHIA_MCP_BRIDGE_TOKEN")
	provider := os.Getenv("SOPHIA_MCP_PROVIDER")

	if bridgeURL == "" || bridgeToken == "" || provider == "" {
		t.Skip("skipping e2e_mcp test: SOPHIA_MCP_BRIDGE_URL, SOPHIA_MCP_BRIDGE_TOKEN, and SOPHIA_MCP_PROVIDER must all be set")
	}

	d := mcpdispatcher.New(&http.Client{}, mcpdispatcher.Config{
		BridgeURL: bridgeURL,
		Token:     bridgeToken,
		Origin:    "http://localhost",
		TimeoutMS: 30_000,
		Provider:  provider,
	})

	result, err := d.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt:       "e2e validate: respond OK",
		WorktreePath: t.TempDir(),
		TimeoutMS:    30_000,
		PhaseType:    "explore",
	})

	// The phase must NOT fail with ErrProviderEmpty. Any other failure
	// (network, subprocess, model API) is acceptable for this check.
	if err != nil {
		isProviderEmpty := errors.Is(err, outbound.ErrDispatchFailed) &&
			strings.Contains(err.Error(), "ErrProviderEmpty")
		assert.False(t, isProviderEmpty,
			"dispatch must NOT fail with ErrProviderEmpty — BUG-6b fix ensures provider is injected: %v", err)
		t.Logf("dispatch failed for non-provider reason (acceptable): %v", err)
		return
	}

	// If we got a result, verify it has the expected shape.
	require.NotNil(t, result, "DispatchResult must not be nil on success")
	assert.Equal(t, "mcp", result.AdapterID)
	t.Logf("e2e dispatch succeeded: adapterID=%s durationMS=%d", result.AdapterID, result.DurationMS)
}
