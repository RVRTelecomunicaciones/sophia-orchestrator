// Package config loads orchestrator configuration from environment
// variables (12-factor) with sensible defaults. Validation runs at boot.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config aggregates all runtime configuration.
type Config struct {
	HTTP        HTTPConfig
	DB          DBConfig
	Governance  ServiceConfig
	Memory      ServiceConfig
	Runtime     ServiceConfig
	Dispatcher  DispatcherConfig
	Spawn       SpawnConfig
	Obs         ObsConfig
	Environment string // dev | staging | prod
	LogLevel    string // debug | info | warn | error
}

// ObsConfig tunes observability (Prometheus metrics + OTEL traces).
type ObsConfig struct {
	MetricsEnabled  bool
	TracesEnabled   bool
	OTLPEndpoint    string  // e.g. "otel-collector:4318"; empty + traces enabled ⇒ stdout exporter
	OTLPInsecure    bool    // skip TLS for OTLP (dev only)
	TraceSampleRate float64 // [0.0, 1.0]
	Version         string  // baked into resource attributes
}

// HTTPConfig configures the inbound HTTP server.
type HTTPConfig struct {
	Addr            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration
	APIKey          string // bootstrap API key (single-tenant V1); empty disables auth
	APIKeyProject   string // the project the bootstrap key authorizes
	// AllowAnonLocalhost enables anonymous (no X-Sophia-API-Key) requests
	// when the listener is bound exclusively to a loopback address. Per
	// sophia-wire-v1 §3.2 / D-M10-02: the EFFECTIVE allow-anon mode is
	// (AllowAnonLocalhost && listener-is-loopback). If the listener binds
	// any non-loopback interface (including 0.0.0.0), the header is
	// required regardless. Default false (safe default).
	AllowAnonLocalhost bool
}

// DBConfig configures Postgres.
type DBConfig struct {
	URL              string
	MaxConns         int32
	MinConns         int32
	MigrationsPath   string
	RunMigrationsOnBoot bool
}

// ServiceConfig configures one outbound HTTP target.
type ServiceConfig struct {
	BaseURL string
	APIKey  string
}

// DispatcherConfig configures the OpenCode dispatcher AND the multi-LLM
// factory (V2.0). Provider/ProviderByPhase select WHICH adapter runs
// (factory.Get); Model/ModelByPhase select WHICH MODEL the chosen adapter
// invokes (passed via the adapter's CLI flags). Both axes are independent
// so an operator can route e.g. apply→aider while spec→opencode + opus.
type DispatcherConfig struct {
	Cmd                 string
	SuggestedConcurrent int
	// Model is the global default opencode `-m <provider/model>` flag
	// value used when no per-phase override is set. Empty = let opencode
	// pick its default. Examples: "anthropic/claude-opus-4-7",
	// "google/gemini-2.5-flash", "github-copilot/claude-sonnet-4.6".
	Model string
	// ModelByPhase maps a phase type (lowercase: "explore", "proposal",
	// "spec", "design", "tasks", "apply", "verify", "archive") to a
	// dispatcher model that overrides Model for THAT phase only. Loaded
	// from env vars `SOPHIA_DISPATCHER_MODEL_<PHASE>` (uppercase) so an
	// operator can wire e.g. Codex for apply + Claude Opus for spec
	// without rebuilding. A missing entry falls back to Model.
	ModelByPhase map[string]string
	// Provider is the V2.0 multi-LLM factory selector — names a
	// registered AgentDispatcher adapter ("opencode" is the V2.0 default
	// and only built-in registration; future versions may register
	// "aider", "ollama", etc.). Empty defaults to "opencode" for
	// backward compatibility with V1 deployments. Loaded from
	// SOPHIA_DISPATCHER_PROVIDER.
	Provider string
	// ProviderByPhase maps a phase type to a registered provider name
	// that overrides Provider for THAT phase only. Loaded from env vars
	// `SOPHIA_DISPATCHER_PROVIDER_<PHASE>`. Combined with ModelByPhase,
	// an operator can wire heterogeneous routing — e.g. apply runs on
	// "aider" + "claude-opus-4-7" while verify runs on "opencode" +
	// "google/gemini-2.5-flash" — without recompiling. A missing entry
	// falls back to Provider.
	ProviderByPhase map[string]string
}

// SpawnConfig tunes the SpawnGovernor.
type SpawnConfig struct {
	Max          int
	StaggerMin   time.Duration
	StaggerMax   time.Duration
	WaitInterval time.Duration
	MaxWait      time.Duration
}

// Default returns a Config populated with V1 production defaults.
func Default() Config {
	return Config{
		HTTP: HTTPConfig{
			Addr:            ":8080",
			ReadTimeout:     30 * time.Second,
			WriteTimeout:    0, // disabled for SSE long-poll
			ShutdownTimeout: 30 * time.Second,
		},
		DB: DBConfig{
			MaxConns:            16,
			MinConns:            2,
			MigrationsPath:      "migrations/postgres",
			RunMigrationsOnBoot: false,
		},
		Dispatcher: DispatcherConfig{
			Cmd:                 "opencode",
			SuggestedConcurrent: 4,
		},
		Obs: ObsConfig{
			MetricsEnabled:  true,
			TracesEnabled:   false,
			TraceSampleRate: 1.0,
			Version:         "v0.1.0",
		},
		Spawn: SpawnConfig{
			Max:          4,
			StaggerMin:   200 * time.Millisecond,
			StaggerMax:   500 * time.Millisecond,
			WaitInterval: 250 * time.Millisecond,
			MaxWait:      30 * time.Second,
		},
		Environment: "dev",
		LogLevel:    "info",
	}
}

// Load builds a Config from environment variables, overriding defaults.
// Returns an error if any required value is missing or malformed.
func Load() (Config, error) {
	c := Default()

	c.HTTP.Addr = envStr("SOPHIA_HTTP_ADDR", c.HTTP.Addr)
	c.HTTP.APIKey = envStr("SOPHIA_HTTP_API_KEY", "")
	c.HTTP.APIKeyProject = envStr("SOPHIA_HTTP_API_KEY_PROJECT", "")
	c.HTTP.ReadTimeout = envDuration("SOPHIA_HTTP_READ_TIMEOUT", c.HTTP.ReadTimeout)
	c.HTTP.ShutdownTimeout = envDuration("SOPHIA_HTTP_SHUTDOWN_TIMEOUT", c.HTTP.ShutdownTimeout)
	c.HTTP.AllowAnonLocalhost = envBool("SOPHIA_HTTP_ALLOW_ANON_LOCALHOST", c.HTTP.AllowAnonLocalhost)

	c.DB.URL = envStr("SOPHIA_DB_URL", "")
	c.DB.MaxConns = int32(envInt("SOPHIA_DB_MAX_CONNS", int(c.DB.MaxConns)))
	c.DB.MinConns = int32(envInt("SOPHIA_DB_MIN_CONNS", int(c.DB.MinConns)))
	c.DB.MigrationsPath = envStr("SOPHIA_DB_MIGRATIONS_PATH", c.DB.MigrationsPath)
	c.DB.RunMigrationsOnBoot = envBool("SOPHIA_DB_MIGRATE_ON_BOOT", c.DB.RunMigrationsOnBoot)

	c.Governance.BaseURL = envStr("SOPHIA_GOVERNANCE_URL", "")
	c.Governance.APIKey = envStr("SOPHIA_GOVERNANCE_API_KEY", "")
	c.Memory.BaseURL = envStr("SOPHIA_MEMORY_URL", "")
	c.Memory.APIKey = envStr("SOPHIA_MEMORY_API_KEY", "")
	c.Runtime.BaseURL = envStr("SOPHIA_RUNTIME_URL", "")
	c.Runtime.APIKey = envStr("SOPHIA_RUNTIME_API_KEY", "")

	c.Dispatcher.Cmd = envStr("SOPHIA_DISPATCHER_CMD", c.Dispatcher.Cmd)
	c.Dispatcher.SuggestedConcurrent = envInt("SOPHIA_DISPATCHER_CONCURRENT", c.Dispatcher.SuggestedConcurrent)
	c.Dispatcher.Model = envStr("SOPHIA_DISPATCHER_MODEL", c.Dispatcher.Model)
	c.Dispatcher.ModelByPhase = loadDispatcherModelByPhase()
	c.Dispatcher.Provider = envStr("SOPHIA_DISPATCHER_PROVIDER", c.Dispatcher.Provider)
	c.Dispatcher.ProviderByPhase = loadDispatcherProviderByPhase()

	c.Spawn.Max = envInt("SOPHIA_SPAWN_MAX", c.Spawn.Max)

	c.Obs.MetricsEnabled = envBool("SOPHIA_METRICS_ENABLED", c.Obs.MetricsEnabled)
	c.Obs.TracesEnabled = envBool("SOPHIA_TRACES_ENABLED", c.Obs.TracesEnabled)
	c.Obs.OTLPEndpoint = envStr("SOPHIA_OTLP_ENDPOINT", c.Obs.OTLPEndpoint)
	c.Obs.OTLPInsecure = envBool("SOPHIA_OTLP_INSECURE", c.Obs.OTLPInsecure)
	c.Obs.Version = envStr("SOPHIA_VERSION", c.Obs.Version)

	c.Environment = envStr("SOPHIA_ENV", c.Environment)
	c.LogLevel = strings.ToLower(envStr("SOPHIA_LOG_LEVEL", c.LogLevel))

	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate enforces required fields.
func (c Config) Validate() error {
	if c.DB.URL == "" {
		return errors.New("config: SOPHIA_DB_URL required")
	}
	if c.Governance.BaseURL == "" {
		return errors.New("config: SOPHIA_GOVERNANCE_URL required")
	}
	if c.Memory.BaseURL == "" {
		return errors.New("config: SOPHIA_MEMORY_URL required")
	}
	if c.Runtime.BaseURL == "" {
		return errors.New("config: SOPHIA_RUNTIME_URL required")
	}
	if c.Spawn.Max <= 0 {
		return errors.New("config: SOPHIA_SPAWN_MAX must be > 0")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("config: SOPHIA_LOG_LEVEL=%q (want debug|info|warn|error)", c.LogLevel)
	}
	return nil
}

// envStr returns the env var or default.
func envStr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envDuration(key string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

// dispatcherPhaseEnvNames lists the lowercase phase keys whose per-phase
// dispatcher model overrides are read from `SOPHIA_DISPATCHER_MODEL_<UPPER>`
// env vars. The set mirrors phase.PhaseType (excluding init/aborted, which
// never invoke the dispatcher).
var dispatcherPhaseEnvNames = []string{
	"explore", "proposal", "spec", "design",
	"tasks", "apply", "verify", "archive",
}

// loadDispatcherModelByPhase reads `SOPHIA_DISPATCHER_MODEL_<PHASE>` for
// each known phase and returns the populated overrides. An unset env var
// means "fall back to the global Model"; only set keys appear in the map.
// The returned map is nil when no overrides are configured (caller should
// treat nil and empty equivalently).
func loadDispatcherModelByPhase() map[string]string {
	return loadPhaseEnvMap("SOPHIA_DISPATCHER_MODEL_")
}

// loadDispatcherProviderByPhase mirrors loadDispatcherModelByPhase for the
// V2.0 multi-LLM factory: reads `SOPHIA_DISPATCHER_PROVIDER_<PHASE>` for
// each known phase. Combined with the model overrides, an operator can
// route heterogeneous phases (e.g. apply via "aider"/claude-opus while
// verify uses "opencode"/gemini-flash) without rebuilding.
func loadDispatcherProviderByPhase() map[string]string {
	return loadPhaseEnvMap("SOPHIA_DISPATCHER_PROVIDER_")
}

// loadPhaseEnvMap is the shared helper for the per-phase env-var maps.
// prefix is the literal env-var prefix WITHOUT trailing phase name —
// the function appends `<UPPER_PHASE>` for each entry in
// dispatcherPhaseEnvNames. Returns nil if no overrides are set.
func loadPhaseEnvMap(prefix string) map[string]string {
	var out map[string]string
	for _, p := range dispatcherPhaseEnvNames {
		key := prefix + strings.ToUpper(p)
		if v, ok := os.LookupEnv(key); ok && v != "" {
			if out == nil {
				out = make(map[string]string, len(dispatcherPhaseEnvNames))
			}
			out[p] = v
		}
	}
	return out
}
