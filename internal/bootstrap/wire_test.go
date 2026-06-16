package bootstrap_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	critic "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/critic"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/bootstrap"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/config"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// minimalWireConfig returns the minimum valid config.Config needed by Wire
// without a real DB or external services. Tests that check pre-DB guards
// (like the MCP provider fail-fast) must use testing.Short() to skip the
// full Wire call when a real DB is required; those guards fire before DB open
// however, so this config is enough for guard-only tests.
func minimalWireConfig() config.Config {
	cfg := config.Default()
	// Required fields that Validate() enforces.
	cfg.DB.URL = "postgres://sophia:sophia@localhost:5432/sophia_orchestator"
	cfg.Governance.BaseURL = "http://gov:9001"
	cfg.Memory.BaseURL = "http://mem:9002"
	cfg.Runtime.BaseURL = "http://rt:9003"
	return cfg
}

// wireFakeDispatcher / wireFakeGovernor are no-op doubles satisfying the
// critic adapter's minimal interfaces, so selectCritic can be exercised without
// a real dispatcher or governor.
type wireFakeDispatcher struct{}

func (wireFakeDispatcher) Dispatch(_ context.Context, _ outbound.DispatchRequest) (*outbound.DispatchResult, error) {
	return &outbound.DispatchResult{}, nil
}

type wireFakeGovernor struct{}

func (wireFakeGovernor) Acquire(_ context.Context) error { return nil }
func (wireFakeGovernor) Release(_ context.Context) error { return nil }

// TestSelectCritic_DefaultsToStub locks that the default config wires the
// deterministic StubCritic — production behaviour stays byte-identical.
func TestSelectCritic_DefaultsToStub(t *testing.T) {
	cfg := config.Default() // Mode == "stub"
	c := bootstrap.ExportedSelectCritic(cfg, wireFakeDispatcher{}, wireFakeGovernor{})
	_, ok := c.(*critic.StubCritic)
	assert.True(t, ok, "default critic mode must wire StubCritic, got %T", c)
}

func TestSelectCritic_UnknownModeFallsBackToStub(t *testing.T) {
	cfg := config.Default()
	cfg.Critic.Mode = "bogus"
	c := bootstrap.ExportedSelectCritic(cfg, wireFakeDispatcher{}, wireFakeGovernor{})
	_, ok := c.(*critic.StubCritic)
	assert.True(t, ok, "unknown critic mode must fall back to StubCritic, got %T", c)
}

// TestSelectCritic_LLMMode locks that SOPHIA_CRITIC_MODE=llm wires the
// LLM-backed critic.
func TestSelectCritic_LLMMode(t *testing.T) {
	cfg := config.Default()
	cfg.Critic.Mode = "llm"
	c := bootstrap.ExportedSelectCritic(cfg, wireFakeDispatcher{}, wireFakeGovernor{})
	_, ok := c.(*critic.LLMCritic)
	assert.True(t, ok, "llm critic mode must wire LLMCritic, got %T", c)
}

// TestWire_MCPProvider_EmptyProvider_FailsFast (M2)
// When SOPHIA_DISPATCHER_PROVIDER=mcp (or any per-phase override) and
// SOPHIA_MCP_PROVIDER is empty, bootstrap.Wire MUST return a non-nil error
// whose message names SOPHIA_MCP_PROVIDER.
//
// The MCP fail-fast guard fires BEFORE pool.Open (first line of Wire is
// db.Open), so this test WILL try to connect to a DB. We skip it in -short
// mode so CI without a Postgres sidecar skips cleanly; the guard is also
// exercised by the unit-level config + dispatcher tests.
func TestWire_MCPProvider_EmptyProvider_FailsFast(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping bootstrap integration test in -short mode (requires DB)")
	}

	cfg := minimalWireConfig()
	cfg.Dispatcher.Provider = "mcp"
	cfg.Dispatcher.MCP.BridgeURL = "http://127.0.0.1:7775" // set so BridgeURL guard passes
	cfg.Dispatcher.MCP.Provider = ""                       // intentionally empty — should fail fast

	_, err := bootstrap.Wire(context.Background(), cfg)
	require.Error(t, err, "Wire must return error when MCP provider is empty")
	assert.Contains(t, err.Error(), "SOPHIA_MCP_PROVIDER",
		"error message must name the missing env var (M2)")
}

// TestWire_MCPProvider_PerPhaseOverride_EmptyProvider_FailsFast (M2 variant)
// Same guard triggers when a per-phase override selects mcp even if global
// Provider is not "mcp".
func TestWire_MCPProvider_PerPhaseOverride_EmptyProvider_FailsFast(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping bootstrap integration test in -short mode (requires DB)")
	}

	cfg := minimalWireConfig()
	cfg.Dispatcher.Provider = "opencode"
	cfg.Dispatcher.ProviderByPhase = map[string]string{"explore": "mcp"}
	cfg.Dispatcher.MCP.BridgeURL = "http://127.0.0.1:7775"
	cfg.Dispatcher.MCP.Provider = "" // empty — must fail fast

	_, err := bootstrap.Wire(context.Background(), cfg)
	require.Error(t, err, "Wire must return error when per-phase mcp override is set but MCP.Provider is empty")
	assert.Contains(t, err.Error(), "SOPHIA_MCP_PROVIDER",
		"error message must name the missing env var (M2 variant)")
}
