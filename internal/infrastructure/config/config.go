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

// DispatcherConfig configures the OpenCode dispatcher.
type DispatcherConfig struct {
	Cmd                 string
	SuggestedConcurrent int
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
