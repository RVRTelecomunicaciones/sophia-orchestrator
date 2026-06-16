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
	HTTP          HTTPConfig
	DB            DBConfig
	Governance    ServiceConfig
	Memory        ServiceConfig
	Runtime       ServiceConfig
	Dispatcher    DispatcherConfig
	Spawn         SpawnConfig
	Apply         ApplyConfig
	Bootstrap     BootstrapConfig
	Obs           ObsConfig
	Environment   string // dev | staging | prod
	LogLevel      string // debug | info | warn | error
	SkillsEnabled bool   // SOPHIA_SKILLS_ENABLED (default true)

	// Critic selects the advisory CriticPort implementation wired into the
	// phase service. The per-change opt-in gate (ContextOverrides["scope"]
	// ["critic_enabled"], DEFAULT OFF) is independent of this selection.
	Critic CriticConfig

	// MemoryWebhook configures the outbound best-effort webhook from orch to
	// memory-engine's /api/v1/worker/phase-archived endpoint (D-M2-1).
	MemoryWebhook MemoryWebhookConfig

	// Outbox configures the transactional outbox relay poller (loop-hardening
	// D-LH-1) that durably delivers phase.archived to memory-engine.
	Outbox OutboxConfig
}

// CriticConfig selects which advisory critic implementation is wired (D3
// follow-up: real LLM critic). Loaded from SOPHIA_CRITIC_MODE.
type CriticConfig struct {
	// Mode is "stub" (default — deterministic StubCritic, byte-identical to
	// the shipped behaviour) or "llm" (LLM-backed critic dispatched through
	// the OpenCode dispatcher). Any unrecognized value falls back to "stub"
	// at wire time. The per-change opt-in gate is independent of this.
	Mode string
}

// OutboxConfig holds the outbox relay poller parameters (loop-hardening D-LH-1).
type OutboxConfig struct {
	// PollInterval is the relay tick cadence. Default 5s. Loaded from
	// SOPHIA_OUTBOX_POLL_INTERVAL (duration string, e.g. "5s").
	PollInterval time.Duration
}

// BootstrapConfig holds parameters for BootstrapTriggerService (DG-C7-5/6).
// All keys are loaded from SOPHIA_BOOTSTRAP_* env vars at boot.
type BootstrapConfig struct {
	// Timeout is the context deadline for the async bootstrap goroutine.
	// Default 60s. Loaded from SOPHIA_BOOTSTRAP_TIMEOUT (duration string, e.g. "60s").
	Timeout time.Duration

	// MaxCallsPerProjectPerDay is the per-project sliding-window rate limit.
	// Default 5. Loaded from SOPHIA_BOOTSTRAP_MAX_CALLS_PER_PROJECT_PER_DAY.
	MaxCallsPerProjectPerDay int

	// MinSnippets is the minimum snippet count for a library entry to be used
	// without falling back to the main entry. Default 50.
	// Loaded from SOPHIA_BOOTSTRAP_MIN_SNIPPETS.
	MinSnippets int

	// BodyBudget is the hard byte cap on the imported skill body (BodyBudget).
	// Default 24576 (24 KiB). Loaded from SOPHIA_BOOTSTRAP_BODY_BUDGET.
	BodyBudget int
}

// MemoryWebhookConfig holds the outbound webhook parameters (M2 D-M2-1).
type MemoryWebhookConfig struct {
	// URL is the full POST endpoint. Empty = disabled (no call made).
	// Loaded from SOPHIA_MEMORY_WEBHOOK_URL.
	URL string
	// APIKey is the X-API-Key header value for memory-engine auth.
	// Loaded from SOPHIA_MEMORY_WEBHOOK_API_KEY.
	APIKey string
	// TimeoutMS is the per-request HTTP client timeout in milliseconds.
	// Default 5000 (5s). Loaded from SOPHIA_MEMORY_WEBHOOK_TIMEOUT_MS.
	TimeoutMS int
}

// ApplyConfig configures the apply-phase coordinator. Loaded from
// SOPHIA_APPLY_* env vars at boot.
//
// Spec #61 (BUG-15) rationale — the apply runtime creates per-group
// git worktrees under WorktreeRoot. The default `/tmp/sophia/worktrees`
// is container-local: when the MCP dispatcher forwards that path to a
// host-side `sophia-agent-mcp` bridge, the bridge can't see the path
// (different filesystem namespace) and its exact-match cwd_allowlist
// rejects it. Operators using the MCP bridge MUST override this to a
// host-mounted path that is mirrored into the runtime container under
// the SAME absolute name (so the orch and the bridge use a single
// string). See ops/local/compose.mcp.yaml for the canonical wiring.
type ApplyConfig struct {
	// WorktreeRoot is the base directory the apply coordinator uses
	// for per-group git worktrees. Empty falls back to
	// apply.DefaultRunConfig().WorktreeRoot (currently
	// "/tmp/sophia/worktrees"). Loaded from SOPHIA_APPLY_WORKTREE_ROOT.
	WorktreeRoot string

	// SourceRepoPath is the path (from the runtime container's
	// perspective) to the repository the apply phase should copy into
	// each newly created worktree. Empty preserves the legacy V1
	// behaviour where createWorktrees only `mkdir -p`s an empty
	// directory — fine for create-only smoke tasks (greeting.go) but
	// blocks any edit-existing-code task because the LLM lands in an
	// empty cwd and can't find the source.
	//
	// Set this to e.g. "/workspace/sophia-orchestator" (under the
	// existing read-only workspace bind mount) so the orch performs
	// `cp -aR <source>/. <worktree>/` before dispatching the implement
	// agent. The agent then has full source visibility AND can run
	// `git diff` against the copied .git. Loaded from
	// SOPHIA_APPLY_SOURCE_REPO_PATH. Spec #65 (BUG-19).
	SourceRepoPath string
	// WorktreeInit selects how the apply phase populates each newly-
	// created worktree before dispatching the implement agent. Valid
	// values: "source_clone" (default, preserves BUG-19) | "empty".
	// Loaded from SOPHIA_APPLY_WORKTREE_INIT. See
	// internal/application/apply/run.go RunConfig.WorktreeInit for the
	// full rationale and the BUG-27 motivation.
	WorktreeInit string
	// TargetPath, when non-empty, triggers the apply phase to copy
	// every successful group's worktree into <TargetPath>/<group_name>/
	// at the end of Execute (BUG-29). Empty preserves the legacy
	// behaviour of leaving worktrees isolated under WorktreeRoot.
	// Loaded from SOPHIA_APPLY_TARGET_PATH.
	TargetPath string
	// DispatchTimeoutMS is the per-dispatch timeout in milliseconds
	// forwarded to RunConfig.DispatchTimeoutMS. Zero leaves the
	// apply.DefaultRunConfig default in effect (currently 180_000 = 3min).
	// Must be > 0 when set; values ≤ 0 are treated as unset (default
	// applies). Loaded from SOPHIA_DISPATCH_TIMEOUT_MS.
	//
	// ADR-0010 Slice 3: the 3min default is chosen so a doomed dispatch
	// (quota exhaustion or silent hang) fails fast within the E2E runtime
	// shell.exec cap of 600s, leaving margin for retries. Override this
	// only when dispatching unusually long-running agents.
	DispatchTimeoutMS int
	// FallbackModel is the model string used when the primary dispatch
	// returns ErrProviderQuotaExceeded (ADR-0010 Slice 4). When non-empty,
	// the apply phase re-dispatches the same task once with
	// DispatchRequest.ModelOverride = FallbackModel before triggering the
	// Slice-2 fail-fast path. The fallback try does NOT consume an Iron-
	// Law-5 attempt. Empty = no fallback (Slice-3 behavior unchanged).
	// Loaded from SOPHIA_DISPATCHER_FALLBACK_MODEL. Apply-phase override
	// only — the global env var applies to all phases via the dispatcher
	// config, but the apply layer reads its own field so it can be wired
	// independently from the dispatcher's per-phase model routing.
	FallbackModel string
	// QuotaBreakerThreshold is the number of consecutive quota outcomes
	// (primary + fallback both exhausted or absent, with no intervening
	// successful task) within a single Execute call that triggers the
	// phase circuit breaker. When the streak reaches this value the phase
	// is aborted with a BLOCKED envelope naming the remedy and one
	// apply.phase.quota_aborted SSE event is emitted. Zero or negative
	// falls back to the apply-level default (3). Loaded from
	// SOPHIA_APPLY_QUOTA_BREAKER_THRESHOLD. See ADR-0010, Slice 5.
	QuotaBreakerThreshold int
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
	URL                 string
	MaxConns            int32
	MinConns            int32
	MigrationsPath      string
	RunMigrationsOnBoot bool
}

// ServiceConfig configures one outbound HTTP target.
type ServiceConfig struct {
	BaseURL string
	APIKey  string
	// TenantID is the tenant the service's API key is bound to. Used
	// only by Memory ingest path today (orch stamps it on
	// MemoryScope.TenantID for persistArtifactsToMemory). Single-
	// tenant deployments leave it empty. Multi-tenant deployments MUST
	// set it via SOPHIA_<SERVICE>_TENANT_ID so memory-engine's auth
	// scope check passes.
	TenantID string
	// TimeoutMS is an optional per-request HTTP timeout override in
	// milliseconds. 0 means use the adapter's built-in default.
	// Set via SOPHIA_MEMORY_TIMEOUT_MS for the memory-engine client
	// (INIT phase p95 budget < 30s; default is 15s which fits).
	TimeoutMS int
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
	// Ollama is the optional sub-config for the local-LLM dispatcher
	// adapter. It is independent from the opencode-shaped Cmd/Model
	// above because ollama uses a different binary, a positional model
	// argument, and its own per-phase model map. The adapter is
	// REGISTERED into the V2.0 factory at boot ONLY when Ollama.Cmd is
	// non-empty — keeping ollama as a strict opt-in for deployments
	// that ship the binary on the runtime-adapters image.
	Ollama OllamaConfig
	// Aider is the optional sub-config for the aider coding-agent
	// dispatcher. Same opt-in shape as Ollama — registered only when
	// Aider.Cmd is non-empty. Aider EDITS FILES in-place rather than
	// returning a JSON envelope, so it should only be selected for
	// the APPLY phase via SOPHIA_DISPATCHER_PROVIDER_APPLY=aider.
	Aider AiderConfig
	// MCP is the opt-in sub-config for the sophia-agent-mcp host-bridge
	// dispatcher. Registered ONLY when MCP.BridgeURL is non-empty,
	// keeping it as a strict opt-in. Select with
	// SOPHIA_DISPATCHER_PROVIDER=mcp or a per-phase override.
	MCP MCPConfig
}

// OllamaConfig configures the optional `ollama` provider in the V2.0
// multi-LLM factory. Loaded from SOPHIA_OLLAMA_* env vars at boot.
//
// Selection axis (Provider/ProviderByPhase) and model axis are kept
// independent from opencode's: ollama has a separate Model field
// because the strings are NOT interchangeable (e.g. ollama wants
// "deepseek-r1:7b", opencode wants "anthropic/claude-opus-4-7"), and
// because the same operator may want to route different phases to
// different providers AND different models simultaneously.
type OllamaConfig struct {
	// Cmd is the binary name; empty disables registration entirely.
	Cmd string
	// SuggestedConcurrent is the SuggestedMaxConcurrent hint surfaced
	// to the SpawnGovernor. Defaults to 2 when unset (single-GPU
	// pipelining limit; see ollama.SuggestedMaxConcurrentDefault).
	SuggestedConcurrent int
	// Model is the global default ollama model. Empty REQUIRES at
	// least one ModelByPhase entry, otherwise the dispatcher fails
	// fast at first call (ollama has no implicit default).
	Model string
	// ModelByPhase maps a phase type (lowercase) to the ollama model
	// name to use for THAT phase only. Loaded from
	// `SOPHIA_OLLAMA_MODEL_<PHASE>`.
	ModelByPhase map[string]string
}

// AiderConfig configures the optional `aider` provider in the V2.0
// multi-LLM factory. Loaded from SOPHIA_AIDER_* env vars at boot.
//
// Aider differs from opencode/ollama in two operationally-important
// ways: (a) it APPLIES EDITS in the worktree directly (no envelope),
// so it should only be routed to the apply phase; (b) credentials
// come from provider env vars (ANTHROPIC_API_KEY, etc.) baked into
// the runtime-adapters image, not from a config field here.
type AiderConfig struct {
	// Cmd is the binary name; empty disables registration entirely.
	Cmd string
	// SuggestedConcurrent is the SuggestedMaxConcurrent hint surfaced
	// to the SpawnGovernor. Defaults to 1 when unset (concurrent
	// in-place edits race; see aider.SuggestedMaxConcurrentDefault).
	SuggestedConcurrent int
	// Model is the global default aider model passed via `--model`.
	// Empty omits the flag, letting aider pick its own default from
	// `~/.aider.conf.yml` or provider env vars.
	Model string
	// ModelByPhase maps a phase type (lowercase) to the aider model
	// name to use for THAT phase only. Loaded from
	// `SOPHIA_AIDER_MODEL_<PHASE>`.
	ModelByPhase map[string]string
}

// MCPConfig configures the sophia-agent-mcp host-bridge dispatcher.
// Loaded from SOPHIA_MCP_* env vars at boot.
//
// The adapter is REGISTERED into the V2.1 factory ONLY when BridgeURL is
// non-empty — keeping the MCP provider as a strict opt-in. Fail-fast
// validation fires at boot when Provider=="mcp" but BridgeURL is empty.
type MCPConfig struct {
	// BridgeURL is the HTTP base URL of the sophia-agent-mcp server,
	// e.g. "http://127.0.0.1:7775". Required when provider=mcp.
	// Loaded from SOPHIA_MCP_BRIDGE_URL.
	BridgeURL string
	// Token is the bearer token sent to the bridge. Required when
	// provider=mcp. Loaded from SOPHIA_MCP_BRIDGE_TOKEN.
	Token string
	// Transport selects the MCP transport. "streamable-http" in V1.
	// Default "streamable-http". Loaded from SOPHIA_MCP_TRANSPORT.
	Transport string
	// TimeoutMS is the per-request timeout in milliseconds.
	// Default 300000 (5 minutes). Loaded from SOPHIA_MCP_TIMEOUT_MS.
	TimeoutMS int
	// ProviderAllowlist is a comma-separated list of providers the
	// bridge may delegate to, forwarded as metadata. Empty = all.
	// Loaded from SOPHIA_MCP_PROVIDER_ALLOWLIST.
	ProviderAllowlist []string
	// DefaultModel is the global default model forwarded to agent.run.
	// Loaded from SOPHIA_MCP_MODEL.
	DefaultModel string
	// ModelByPhase maps a phase type (lowercase) to a model override.
	// Loaded from SOPHIA_MCP_MODEL_<PHASE>.
	ModelByPhase map[string]string
	// Origin is the HTTP Origin header value sent to the bridge.
	// Default "http://localhost". Loaded from SOPHIA_MCP_ORIGIN.
	Origin string
	// Provider is the upstream provider name forwarded as the required
	// "provider" argument to agent.run (e.g. "opencode"). Required when
	// provider=mcp. Bootstrap fails fast when MCP is selected and this
	// is empty. Loaded from SOPHIA_MCP_PROVIDER.
	Provider string
	// DefaultCWD is the absolute host directory the dispatcher
	// substitutes for any DispatchRequest whose WorktreePath is empty
	// or ".". Empty preserves legacy behaviour. Loaded from
	// SOPHIA_MCP_DEFAULT_CWD. See Spec #60 (BUG-14) — bridge cwd
	// allowlist is exact-match so without a permitted absolute path
	// here, all pre-apply phases (which run without a worktree) get
	// rejected by the bridge with cwd_not_allowed.
	DefaultCWD string
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
			// 2026-06: raised 4→6 so the global slot ceiling exceeds the
			// apply phase's max concurrent implement demand (was exactly 4
			// = MaxParallelGroups×MaxParallelImplementsPerGroup, leaving
			// zero margin and forcing saturation waits). Overridable via
			// SOPHIA_SPAWN_MAX.
			Max:          6,
			StaggerMin:   200 * time.Millisecond,
			StaggerMax:   500 * time.Millisecond,
			WaitInterval: 250 * time.Millisecond,
			MaxWait:      30 * time.Second,
		},
		Bootstrap: BootstrapConfig{
			Timeout:                  60 * time.Second,
			MaxCallsPerProjectPerDay: 5,
			MinSnippets:              50,
			BodyBudget:               24576,
		},
		Environment:   "dev",
		LogLevel:      "info",
		SkillsEnabled: true, // default ON
		Critic: CriticConfig{
			Mode: "stub", // default ON-stub; LLM critic is opt-in via SOPHIA_CRITIC_MODE=llm
		},
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
	c.Memory.TenantID = envStr("SOPHIA_MEMORY_TENANT_ID", "")
	if v := envInt("SOPHIA_MEMORY_TIMEOUT_MS", 0); v > 0 {
		c.Memory.TimeoutMS = v
	}
	c.Runtime.BaseURL = envStr("SOPHIA_RUNTIME_URL", "")
	c.Runtime.APIKey = envStr("SOPHIA_RUNTIME_API_KEY", "")

	c.MemoryWebhook.URL = envStr("SOPHIA_MEMORY_WEBHOOK_URL", "")
	c.MemoryWebhook.APIKey = envStr("SOPHIA_MEMORY_WEBHOOK_API_KEY", "")
	c.MemoryWebhook.TimeoutMS = envInt("SOPHIA_MEMORY_WEBHOOK_TIMEOUT_MS", 5000)

	c.Outbox.PollInterval = envDuration("SOPHIA_OUTBOX_POLL_INTERVAL", 5*time.Second)

	c.Apply.WorktreeRoot = envStr("SOPHIA_APPLY_WORKTREE_ROOT", c.Apply.WorktreeRoot)
	c.Apply.SourceRepoPath = envStr("SOPHIA_APPLY_SOURCE_REPO_PATH", c.Apply.SourceRepoPath)
	c.Apply.WorktreeInit = envStr("SOPHIA_APPLY_WORKTREE_INIT", c.Apply.WorktreeInit)
	c.Apply.TargetPath = envStr("SOPHIA_APPLY_TARGET_PATH", c.Apply.TargetPath)
	if v := envInt("SOPHIA_DISPATCH_TIMEOUT_MS", 0); v > 0 {
		c.Apply.DispatchTimeoutMS = v
	}
	c.Apply.FallbackModel = envStr("SOPHIA_DISPATCHER_FALLBACK_MODEL", "")
	if v := envInt("SOPHIA_APPLY_QUOTA_BREAKER_THRESHOLD", 0); v > 0 {
		c.Apply.QuotaBreakerThreshold = v
	}

	c.Dispatcher.Cmd = envStr("SOPHIA_DISPATCHER_CMD", c.Dispatcher.Cmd)
	c.Dispatcher.SuggestedConcurrent = envInt("SOPHIA_DISPATCHER_CONCURRENT", c.Dispatcher.SuggestedConcurrent)
	c.Dispatcher.Model = envStr("SOPHIA_DISPATCHER_MODEL", c.Dispatcher.Model)
	c.Dispatcher.ModelByPhase = loadDispatcherModelByPhase()
	c.Dispatcher.Provider = envStr("SOPHIA_DISPATCHER_PROVIDER", c.Dispatcher.Provider)
	c.Dispatcher.ProviderByPhase = loadDispatcherProviderByPhase()

	c.Dispatcher.Ollama.Cmd = envStr("SOPHIA_OLLAMA_CMD", c.Dispatcher.Ollama.Cmd)
	c.Dispatcher.Ollama.SuggestedConcurrent = envInt("SOPHIA_OLLAMA_CONCURRENT", c.Dispatcher.Ollama.SuggestedConcurrent)
	c.Dispatcher.Ollama.Model = envStr("SOPHIA_OLLAMA_MODEL", c.Dispatcher.Ollama.Model)
	c.Dispatcher.Ollama.ModelByPhase = loadOllamaModelByPhase()

	c.Dispatcher.Aider.Cmd = envStr("SOPHIA_AIDER_CMD", c.Dispatcher.Aider.Cmd)
	c.Dispatcher.Aider.SuggestedConcurrent = envInt("SOPHIA_AIDER_CONCURRENT", c.Dispatcher.Aider.SuggestedConcurrent)
	c.Dispatcher.Aider.Model = envStr("SOPHIA_AIDER_MODEL", c.Dispatcher.Aider.Model)
	c.Dispatcher.Aider.ModelByPhase = loadAiderModelByPhase()

	c.Dispatcher.MCP.BridgeURL = envStr("SOPHIA_MCP_BRIDGE_URL", c.Dispatcher.MCP.BridgeURL)
	c.Dispatcher.MCP.Token = envStr("SOPHIA_MCP_BRIDGE_TOKEN", c.Dispatcher.MCP.Token)
	c.Dispatcher.MCP.Transport = envStr("SOPHIA_MCP_TRANSPORT", "streamable-http")
	c.Dispatcher.MCP.TimeoutMS = envInt("SOPHIA_MCP_TIMEOUT_MS", 300_000)
	c.Dispatcher.MCP.Origin = envStr("SOPHIA_MCP_ORIGIN", "http://localhost")
	c.Dispatcher.MCP.Provider = envStr("SOPHIA_MCP_PROVIDER", "")
	c.Dispatcher.MCP.DefaultCWD = envStr("SOPHIA_MCP_DEFAULT_CWD", c.Dispatcher.MCP.DefaultCWD)
	c.Dispatcher.MCP.DefaultModel = envStr("SOPHIA_MCP_MODEL", c.Dispatcher.MCP.DefaultModel)
	c.Dispatcher.MCP.ModelByPhase = loadMCPModelByPhase()
	if allowlist := envStr("SOPHIA_MCP_PROVIDER_ALLOWLIST", ""); allowlist != "" {
		for _, p := range strings.Split(allowlist, ",") {
			if p = strings.TrimSpace(p); p != "" {
				c.Dispatcher.MCP.ProviderAllowlist = append(c.Dispatcher.MCP.ProviderAllowlist, p)
			}
		}
	}

	c.Spawn.Max = envInt("SOPHIA_SPAWN_MAX", c.Spawn.Max)

	c.Obs.MetricsEnabled = envBool("SOPHIA_METRICS_ENABLED", c.Obs.MetricsEnabled)
	c.Obs.TracesEnabled = envBool("SOPHIA_TRACES_ENABLED", c.Obs.TracesEnabled)
	c.Obs.OTLPEndpoint = envStr("SOPHIA_OTLP_ENDPOINT", c.Obs.OTLPEndpoint)
	c.Obs.OTLPInsecure = envBool("SOPHIA_OTLP_INSECURE", c.Obs.OTLPInsecure)
	c.Obs.Version = envStr("SOPHIA_VERSION", c.Obs.Version)

	c.Environment = envStr("SOPHIA_ENV", c.Environment)
	c.LogLevel = strings.ToLower(envStr("SOPHIA_LOG_LEVEL", c.LogLevel))
	c.SkillsEnabled = envBool("SOPHIA_SKILLS_ENABLED", c.SkillsEnabled)
	c.Critic.Mode = strings.ToLower(envStr("SOPHIA_CRITIC_MODE", c.Critic.Mode))

	c.Bootstrap.Timeout = envDuration("SOPHIA_BOOTSTRAP_TIMEOUT", c.Bootstrap.Timeout)
	if v := envInt("SOPHIA_BOOTSTRAP_MAX_CALLS_PER_PROJECT_PER_DAY", 0); v > 0 {
		c.Bootstrap.MaxCallsPerProjectPerDay = v
	}
	if v := envInt("SOPHIA_BOOTSTRAP_MIN_SNIPPETS", 0); v > 0 {
		c.Bootstrap.MinSnippets = v
	}
	if v := envInt("SOPHIA_BOOTSTRAP_BODY_BUDGET", 0); v > 0 {
		c.Bootstrap.BodyBudget = v
	}

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

// loadOllamaModelByPhase reads `SOPHIA_OLLAMA_MODEL_<PHASE>` overrides
// for the ollama adapter. Kept on a SEPARATE axis from the
// opencode-shaped SOPHIA_DISPATCHER_MODEL_<PHASE> because the model
// strings are not portable across providers (e.g. opencode wants
// "anthropic/claude-opus-4-7", ollama wants "deepseek-r1:7b") and
// because an operator may want apply-on-opencode-with-claude
// alongside verify-on-ollama-with-qwen simultaneously.
func loadOllamaModelByPhase() map[string]string {
	return loadPhaseEnvMap("SOPHIA_OLLAMA_MODEL_")
}

// loadAiderModelByPhase reads `SOPHIA_AIDER_MODEL_<PHASE>` overrides
// for the aider adapter. Separate from ollama and opencode for the
// same reason: aider's model names follow its own alias table (e.g.
// "claude-opus-4-7" — no "anthropic/" prefix, no "github-copilot/"
// prefix), and the apply phase may use a different aider model than
// the operator's default declared in `~/.aider.conf.yml`.
func loadAiderModelByPhase() map[string]string {
	return loadPhaseEnvMap("SOPHIA_AIDER_MODEL_")
}

// loadMCPModelByPhase reads `SOPHIA_MCP_MODEL_<PHASE>` overrides for
// the sophia-agent-mcp bridge dispatcher. The per-phase model is
// forwarded to agent.run as metadata so the bridge can select the
// appropriate provider model without re-configuration.
func loadMCPModelByPhase() map[string]string {
	return loadPhaseEnvMap("SOPHIA_MCP_MODEL_")
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
