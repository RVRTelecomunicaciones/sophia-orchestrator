//go:build e2e_mcp

// Package e2e — SDK initialize sequence test (PR-3, T-10).
//
// This file proves that the MCP dispatcher performs the full
// initialize → notifications/initialized → tools/call sequence before any
// tool invocation. It does so without docker or an external binary: the SDK's
// own NewStreamableHTTPHandler + httptest.NewServer provides a minimal in-process
// MCP server that registers agent.health and agent.run tools.
//
// What this test proves (BUG-7 regression guard):
//
//   - The dispatcher issues an initialize request BEFORE tools/call.
//   - The stub server responds correctly to initialize (SDK handles this).
//   - tools/call succeeds — the response is a provider_error (no provider
//     binary is actually installed) but the MCP exchange completed.
//
// A raw HTTP/JSON-RPC client (the broken pre-rewrite approach) would never
// send initialize and would receive "method invalid during initialization"
// from the stub, failing at the transport layer instead of the provider layer.
//
// Run with:
//
//	go test -tags=e2e_mcp ./test/e2e/ -run TestMCPInitializeSequence -v
package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	mcpdispatcher "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/dispatcher/mcp"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// agentHealthArgs is the input schema for the stub agent.health tool.
// The SDK requires an object schema; an empty struct satisfies that.
type agentHealthArgs struct{}

// agentRunArgs is the input schema for the stub agent.run tool.
type agentRunArgs struct{}

// TestMCPInitializeSequence proves that the SDK-based dispatcher performs
// the MCP initialize handshake before any tools/call. Uses an in-process
// stub MCP server — no docker, no external binary required.
//
// Scenarios verified:
//   - N3: HealthCheck opens a fresh session, calls agent.health, closes it.
//   - R1: dispatcher does NOT trigger "method invalid during initialization"
//     (BUG-7 regression guard).
func TestMCPInitializeSequence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	// ---------------------------------------------------------------------------
	// Build the stub MCP server with agent.health and agent.run tools.
	// ---------------------------------------------------------------------------
	server := sdkmcp.NewServer(
		&sdkmcp.Implementation{Name: "sophia-stub-bridge", Version: "v0.0.1"},
		nil,
	)

	// agent.health stub — always returns status="ok".
	healthJSON := `{"status":"ok","providers":[{"name":"stub","installed":true}]}`
	sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "agent.health", Description: "health probe"},
		func(_ context.Context, _ *sdkmcp.CallToolRequest, _ agentHealthArgs) (*sdkmcp.CallToolResult, agentHealthArgs, error) {
			return &sdkmcp.CallToolResult{
				Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: healthJSON}},
			}, agentHealthArgs{}, nil
		})

	// agent.run stub — returns a minimal bridge provider_error envelope so the
	// dispatcher's envelope parsing path is exercised (status=error, class=provider_error).
	// This simulates "provider not installed on this machine" — a real bridge error,
	// but NOT a transport error. Crucially, the stub never returns "method invalid
	// during initialization" because the SDK always receives initialize first.
	runErrJSON, _ := json.Marshal(map[string]any{
		"status": "error",
		"class":  "provider_error",
		"error":  "stub: no real provider binary installed",
	})
	sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "agent.run", Description: "run agent"},
		func(_ context.Context, _ *sdkmcp.CallToolRequest, _ agentRunArgs) (*sdkmcp.CallToolResult, agentRunArgs, error) {
			return &sdkmcp.CallToolResult{
				Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: string(runErrJSON)}},
			}, agentRunArgs{}, nil
		})

	// ---------------------------------------------------------------------------
	// Serve the stub over Streamable HTTP via httptest.
	// ---------------------------------------------------------------------------
	handler := sdkmcp.NewStreamableHTTPHandler(
		func(_ *http.Request) *sdkmcp.Server { return server },
		&sdkmcp.StreamableHTTPOptions{
			// JSONResponse simplifies the response for test inspection.
			JSONResponse: true,
		},
	)
	stubServer := httptest.NewServer(handler)
	t.Cleanup(stubServer.Close)

	// ---------------------------------------------------------------------------
	// Build the real dispatcher pointing at the stub.
	// No token enforcement on the stub, so any token value works.
	// ---------------------------------------------------------------------------
	d := mcpdispatcher.New(&http.Client{Timeout: 10 * time.Second}, mcpdispatcher.Config{
		BridgeURL: stubServer.URL,
		Token:     "e2e-stub-token",
		Origin:    "http://localhost",
		Transport: "streamable-http",
		TimeoutMS: 10_000,
		Provider:  "stub",
		Suggested: 4,
	})

	// ---------------------------------------------------------------------------
	// N3: HealthCheck — open fresh session, call agent.health, close.
	// ---------------------------------------------------------------------------
	t.Run("N3_HealthCheck_SessionLifecycle", func(t *testing.T) {
		hCtx, hCancel := context.WithTimeout(ctx, 5*time.Second)
		defer hCancel()

		err := d.HealthCheck(hCtx)
		require.NoError(t, err,
			"HealthCheck must succeed against stub bridge that returns status=ok")
	})

	// N3: call HealthCheck a second time — proves no shared session state is
	// leaked between calls (each call uses a fresh Connect → CallTool → Close).
	t.Run("N3_HealthCheck_NoSharedState_SecondCall", func(t *testing.T) {
		hCtx, hCancel := context.WithTimeout(ctx, 5*time.Second)
		defer hCancel()

		err := d.HealthCheck(hCtx)
		require.NoError(t, err,
			"Second HealthCheck must succeed — no session leak from first call")
	})

	// ---------------------------------------------------------------------------
	// R1: Dispatch — proves initialize handshake happened before tools/call.
	// The stub returns provider_error (not transport_error), which means the
	// MCP initialize + tools/call exchange succeeded. BUG-7 would surface as
	// transport_error containing "method invalid during initialization".
	// ---------------------------------------------------------------------------
	t.Run("R1_Dispatch_InitializeHandshake_NoBUG7", func(t *testing.T) {
		dCtx, dCancel := context.WithTimeout(ctx, 10*time.Second)
		defer dCancel()

		_, err := d.Dispatch(dCtx, outbound.DispatchRequest{
			Prompt:       "echo hello from e2e stub",
			WorktreePath: "/tmp",
			TimeoutMS:    8_000,
		})

		// Expect provider_error (stub returns status=error, class=provider_error).
		require.Error(t, err, "Dispatch against stub must return an error (provider_error expected)")
		require.True(t, errors.Is(err, outbound.ErrDispatchFailed),
			"error must wrap ErrDispatchFailed, got: %v", err)

		// BUG-7 regression: must NOT be "method invalid during initialization".
		assert.NotContains(t, err.Error(), "method invalid during initialization",
			"BUG-7 regression detected: the initialize handshake was skipped")

		// Must be provider_error, not transport_error (transport_error would mean
		// the MCP exchange itself failed — initialize or framing issue).
		assert.Contains(t, err.Error(), "provider_error",
			"expected provider_error from stub, got: %v", err)
		assert.NotContains(t, err.Error(), "transport_error",
			"transport_error would indicate an initialize/framing failure (BUG-7 variant)")
	})
}
