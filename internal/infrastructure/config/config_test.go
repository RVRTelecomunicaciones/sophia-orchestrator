package config_test

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/config"
)

// setEnv sets env vars and registers cleanup to restore them.
func setEnv(t *testing.T, pairs ...string) {
	t.Helper()
	if len(pairs)%2 != 0 {
		t.Fatal("setEnv: pairs must be even (key, value, key, value, ...)")
	}
	for i := 0; i < len(pairs); i += 2 {
		key, val := pairs[i], pairs[i+1]
		old, hadOld := os.LookupEnv(key)
		t.Cleanup(func() {
			if hadOld {
				_ = os.Setenv(key, old)
			} else {
				_ = os.Unsetenv(key)
			}
		})
		require.NoError(t, os.Setenv(key, val))
	}
}

// minimalEnv sets the minimum required env vars for Load() to succeed.
func minimalEnv(t *testing.T, extra ...string) {
	t.Helper()
	base := []string{
		"SOPHIA_DB_URL", "postgres://test/test",
		"SOPHIA_GOVERNANCE_URL", "http://gov:9001",
		"SOPHIA_MEMORY_URL", "http://mem:9002",
		"SOPHIA_RUNTIME_URL", "http://rt:9003",
	}
	setEnv(t, append(base, extra...)...)
}

// --- D1: All SOPHIA_MCP_* env vars load into DispatcherConfig.MCP ---

func TestLoad_MCPConfig_AllEnvVars(t *testing.T) {
	minimalEnv(t,
		"SOPHIA_MCP_BRIDGE_URL", "http://127.0.0.1:7775",
		"SOPHIA_MCP_BRIDGE_TOKEN", "super-secret-token",
		"SOPHIA_MCP_TRANSPORT", "streamable-http",
		"SOPHIA_MCP_TIMEOUT_MS", "60000",
		"SOPHIA_MCP_ORIGIN", "http://127.0.0.1",
		"SOPHIA_MCP_MODEL", "anthropic/claude-opus-4-7",
		"SOPHIA_MCP_PROVIDER_ALLOWLIST", "opencode,claude",
	)

	cfg, err := config.Load()
	require.NoError(t, err)

	mcp := cfg.Dispatcher.MCP
	assert.Equal(t, "http://127.0.0.1:7775", mcp.BridgeURL, "BridgeURL must load from SOPHIA_MCP_BRIDGE_URL")
	assert.Equal(t, "super-secret-token", mcp.Token, "Token must load from SOPHIA_MCP_BRIDGE_TOKEN")
	assert.Equal(t, "streamable-http", mcp.Transport, "Transport must load from SOPHIA_MCP_TRANSPORT")
	assert.Equal(t, 60000, mcp.TimeoutMS, "TimeoutMS must load from SOPHIA_MCP_TIMEOUT_MS")
	assert.Equal(t, "http://127.0.0.1", mcp.Origin, "Origin must load from SOPHIA_MCP_ORIGIN")
	assert.Equal(t, "anthropic/claude-opus-4-7", mcp.DefaultModel, "DefaultModel must load from SOPHIA_MCP_MODEL")
	assert.Equal(t, []string{"opencode", "claude"}, mcp.ProviderAllowlist, "ProviderAllowlist must parse comma-separated SOPHIA_MCP_PROVIDER_ALLOWLIST")
}

// D1: no field is silently dropped.
func TestLoad_MCPConfig_NoFieldSilentlyDropped(t *testing.T) {
	minimalEnv(t,
		"SOPHIA_MCP_BRIDGE_URL", "http://127.0.0.1:7775",
		"SOPHIA_MCP_BRIDGE_TOKEN", "tok",
	)
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.NotEmpty(t, cfg.Dispatcher.MCP.BridgeURL)
	assert.NotEmpty(t, cfg.Dispatcher.MCP.Token)
}

// D2: per-phase provider override accepts "mcp"
func TestLoad_PerPhaseProviderMCP(t *testing.T) {
	minimalEnv(t,
		"SOPHIA_DISPATCHER_PROVIDER_APPLY", "mcp",
		"SOPHIA_DISPATCHER_PROVIDER_SPEC", "opencode",
	)
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "mcp", cfg.Dispatcher.ProviderByPhase["apply"],
		"apply phase must resolve to mcp provider")
	assert.Equal(t, "opencode", cfg.Dispatcher.ProviderByPhase["spec"],
		"spec phase must resolve to opencode provider")
}

// D2: phases without override keep global default.
func TestLoad_PerPhaseProviderMCP_OtherPhasesUnaffected(t *testing.T) {
	minimalEnv(t,
		"SOPHIA_DISPATCHER_PROVIDER_APPLY", "mcp",
	)
	cfg, err := config.Load()
	require.NoError(t, err)
	_, hasExplore := cfg.Dispatcher.ProviderByPhase["explore"]
	assert.False(t, hasExplore, "explore must not have a per-phase override when env is unset")
}

// D3: sensible defaults when no SOPHIA_MCP_* are set
func TestLoad_MCPDefaults(t *testing.T) {
	minimalEnv(t) // no MCP vars
	cfg, err := config.Load()
	require.NoError(t, err)

	mcp := cfg.Dispatcher.MCP
	assert.Equal(t, "streamable-http", mcp.Transport, "Transport must default to streamable-http")
	assert.Equal(t, 300_000, mcp.TimeoutMS, "TimeoutMS must default to 300000 (5 minutes)")
	assert.Equal(t, "http://localhost", mcp.Origin, "Origin must default to http://localhost")
	assert.Empty(t, mcp.BridgeURL, "BridgeURL must be empty when not set")
}

// D3: documented default TimeoutMS is 300000
func TestLoad_MCPDefaults_TimeoutMS(t *testing.T) {
	minimalEnv(t)
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, 300_000, cfg.Dispatcher.MCP.TimeoutMS)
}

// A9: default provider unchanged when env unset or = "opencode"
func TestLoad_DefaultProviderUnchanged(t *testing.T) {
	minimalEnv(t) // SOPHIA_DISPATCHER_PROVIDER not set
	cfg, err := config.Load()
	require.NoError(t, err)
	// Provider defaults to "" which wire.go maps to "opencode".
	assert.Equal(t, "", cfg.Dispatcher.Provider, "Provider must be empty (defaults to opencode in bootstrap)")
}

func TestLoad_ExplicitOpenCodeProvider(t *testing.T) {
	minimalEnv(t, "SOPHIA_DISPATCHER_PROVIDER", "opencode")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "opencode", cfg.Dispatcher.Provider)
}

// D4: SOPHIA_MCP_PROVIDER loads into MCPConfig.Provider (BUG-6b)
func TestLoad_MCPProvider_LoadsFromEnv(t *testing.T) {
	minimalEnv(t, "SOPHIA_MCP_PROVIDER", "opencode")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "opencode", cfg.Dispatcher.MCP.Provider,
		"Provider must load from SOPHIA_MCP_PROVIDER")
}

// D4: SOPHIA_MCP_PROVIDER defaults to empty string when not set.
func TestLoad_MCPProvider_DefaultsEmpty(t *testing.T) {
	minimalEnv(t) // no SOPHIA_MCP_PROVIDER
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "", cfg.Dispatcher.MCP.Provider,
		"Provider must default to empty string when SOPHIA_MCP_PROVIDER is unset")
}

// MCP per-phase model overrides load correctly (SOPHIA_MCP_MODEL_<PHASE>)
func TestLoad_MCPModelByPhase(t *testing.T) {
	minimalEnv(t,
		"SOPHIA_MCP_MODEL_APPLY", "apply-model",
		"SOPHIA_MCP_MODEL_SPEC", "spec-model",
	)
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "apply-model", cfg.Dispatcher.MCP.ModelByPhase["apply"])
	assert.Equal(t, "spec-model", cfg.Dispatcher.MCP.ModelByPhase["spec"])
}

// Provider allowlist with spaces trimmed
func TestLoad_MCPProviderAllowlist_TrimSpaces(t *testing.T) {
	minimalEnv(t, "SOPHIA_MCP_PROVIDER_ALLOWLIST", " opencode , claude , gemini ")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, []string{"opencode", "claude", "gemini"}, cfg.Dispatcher.MCP.ProviderAllowlist)
}

// Empty provider allowlist → nil
func TestLoad_MCPProviderAllowlist_Empty(t *testing.T) {
	minimalEnv(t) // no SOPHIA_MCP_PROVIDER_ALLOWLIST
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Nil(t, cfg.Dispatcher.MCP.ProviderAllowlist)
}

// SOPHIA_MCP_TRANSPORT override
func TestLoad_MCPTransportOverride(t *testing.T) {
	minimalEnv(t, "SOPHIA_MCP_TRANSPORT", "streamable-http")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "streamable-http", cfg.Dispatcher.MCP.Transport)
}

// Validate: missing required fields return errors.
func TestValidate_MissingDBURL(t *testing.T) {
	cfg := config.Default()
	// DB.URL is required — validate should fail.
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SOPHIA_DB_URL")
}

func TestValidate_MissingGovernanceURL(t *testing.T) {
	cfg := config.Default()
	cfg.DB.URL = "postgres://test/test"
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SOPHIA_GOVERNANCE_URL")
}

func TestValidate_MissingMemoryURL(t *testing.T) {
	cfg := config.Default()
	cfg.DB.URL = "postgres://test/test"
	cfg.Governance.BaseURL = "http://gov:9001"
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SOPHIA_MEMORY_URL")
}

func TestValidate_MissingRuntimeURL(t *testing.T) {
	cfg := config.Default()
	cfg.DB.URL = "postgres://test/test"
	cfg.Governance.BaseURL = "http://gov:9001"
	cfg.Memory.BaseURL = "http://mem:9002"
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SOPHIA_RUNTIME_URL")
}

func TestValidate_InvalidLogLevel(t *testing.T) {
	cfg := config.Default()
	cfg.DB.URL = "postgres://test/test"
	cfg.Governance.BaseURL = "http://gov:9001"
	cfg.Memory.BaseURL = "http://mem:9002"
	cfg.Runtime.BaseURL = "http://rt:9003"
	cfg.Spawn.Max = 4
	cfg.LogLevel = "verbose" // invalid
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SOPHIA_LOG_LEVEL")
}

// Load: missing required URL → error propagated.
func TestLoad_MissingDBURL_ReturnsError(t *testing.T) {
	// Do not set required env vars — only set to avoid inheriting real values.
	setEnv(t,
		"SOPHIA_DB_URL", "",
		"SOPHIA_GOVERNANCE_URL", "",
		"SOPHIA_MEMORY_URL", "",
		"SOPHIA_RUNTIME_URL", "",
	)
	_, err := config.Load()
	require.Error(t, err)
}

// envBool: valid bool values
func TestLoad_EnvBool(t *testing.T) {
	minimalEnv(t, "SOPHIA_METRICS_ENABLED", "false")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.False(t, cfg.Obs.MetricsEnabled)
}

func TestLoad_EnvBool_InvalidFallsBack(t *testing.T) {
	minimalEnv(t, "SOPHIA_METRICS_ENABLED", "notabool")
	cfg, err := config.Load()
	require.NoError(t, err)
	// Falls back to default (true).
	assert.True(t, cfg.Obs.MetricsEnabled)
}

// envDuration: valid duration
func TestLoad_EnvDuration(t *testing.T) {
	minimalEnv(t, "SOPHIA_HTTP_READ_TIMEOUT", "45s")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, 45*time.Second, cfg.HTTP.ReadTimeout)
}

func TestLoad_EnvDuration_InvalidFallsBack(t *testing.T) {
	minimalEnv(t, "SOPHIA_HTTP_READ_TIMEOUT", "not-a-duration")
	cfg, err := config.Load()
	require.NoError(t, err)
	// Falls back to default (30s).
	assert.Equal(t, 30*time.Second, cfg.HTTP.ReadTimeout)
}
