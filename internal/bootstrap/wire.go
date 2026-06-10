// Package bootstrap is the composition root. It is the ONLY place in the
// codebase that imports concrete adapter implementations. Domain and
// application packages NEVER reach into adapters; that direction is
// guarded by golangci-lint forbidigo rules.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	httpinbound "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/inbound/http"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/inbound/http/middleware"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/dispatcher/aider"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/dispatcher/factory"
	mcpdispatcher "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/dispatcher/mcp"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/dispatcher/ollama"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/dispatcher/opencode"
	execadapter "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/exec"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/governance"
	graphifyadapter "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/graphify"
	httpbase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/http_base"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/memory"
	webhookadapter "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/webhook"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/pg"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/runtime"
	gitrunneradapter "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/gitrunner"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	skillapp "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/eventstream"
	initphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init"
	initcache "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/cache"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/detector"
	initpersister "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/persister"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/recovery"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/config"
	dbpkg "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/db"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/obs"
	obslog "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/obs/log"
)

// App is the wired application. main.go calls Run; tests can call Close
// directly to exercise teardown.
type App struct {
	cfg    config.Config
	logger *slog.Logger
	pool   *pgxpool.Pool
	server *http.Server
	tracer *obs.Tracer
}

// Wire constructs the App by composing every concrete dependency.
func Wire(ctx context.Context, cfg config.Config) (*App, error) {
	logger := newLogger(cfg)

	// MCP fail-fast guards run BEFORE pool.Open so they can be tested
	// without a real DB. These checks mirror the existing BridgeURL guard.
	{
		mcpSelected := cfg.Dispatcher.Provider == "mcp"
		if !mcpSelected {
			for _, p := range cfg.Dispatcher.ProviderByPhase {
				if p == "mcp" {
					mcpSelected = true
					break
				}
			}
		}
		if mcpSelected && cfg.Dispatcher.MCP.BridgeURL == "" {
			return nil, fmt.Errorf("bootstrap: SOPHIA_DISPATCHER_PROVIDER=mcp requires SOPHIA_MCP_BRIDGE_URL to be set")
		}
		if mcpSelected && cfg.Dispatcher.MCP.Provider == "" {
			return nil, fmt.Errorf("bootstrap: SOPHIA_DISPATCHER_PROVIDER=mcp (or per-phase override) requires SOPHIA_MCP_PROVIDER to be set")
		}
	}

	pool, err := dbpkg.Open(ctx, dbpkg.Config{
		URL:      cfg.DB.URL,
		MaxConns: cfg.DB.MaxConns,
		MinConns: cfg.DB.MinConns,
	})
	if err != nil {
		return nil, fmt.Errorf("bootstrap: db open: %w", err)
	}

	if cfg.DB.RunMigrationsOnBoot {
		if err := dbpkg.MigrateUp(cfg.DB.MigrationsPath, cfg.DB.URL); err != nil {
			pool.Close()
			return nil, fmt.Errorf("bootstrap: migrate: %w", err)
		}
	}

	// Repositories.
	changeRepo := pg.NewChangeRepo(pool)
	phaseRepo := pg.NewPhaseRepo(pool)
	boardRepo := pg.NewBoardRepo(pool)
	sessionRepo := pg.NewSessionRepo(pool)
	auditLog := pg.NewAuditLog(pool)
	spawnRepo := pg.NewSpawnGovernorRepo(pool)
	skillRepo := pg.NewSkillRepo(pool)
	skillUsageRepo := pg.NewSkillUsageRepo(pool)
	_ = boardRepo // used by ApplyService below

	// Outbound HTTP clients.
	govClient, err := governance.New(governance.DefaultConfig(cfg.Governance.BaseURL, cfg.Governance.APIKey))
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("bootstrap: governance: %w", err)
	}
	memCfg := memory.DefaultConfig(cfg.Memory.BaseURL, cfg.Memory.APIKey)
	if cfg.Memory.TimeoutMS > 0 {
		// SOPHIA_MEMORY_TIMEOUT_MS override. Default is 15s (see memory.DefaultConfig).
		// INIT phase p95 budget < 30s; 15s default satisfies that constraint.
		memCfg.HTTPBase.HTTPTimeout = time.Duration(cfg.Memory.TimeoutMS) * time.Millisecond
	}
	memClient, err := memory.New(memCfg)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("bootstrap: memory: %w", err)
	}
	rtClient, err := runtime.New(runtime.DefaultConfig(cfg.Runtime.BaseURL, cfg.Runtime.APIKey))
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("bootstrap: runtime: %w", err)
	}
	opencodeAdapter := opencode.New(rtClient, opencode.Config{
		Cmd:          cfg.Dispatcher.Cmd,
		Suggested:    cfg.Dispatcher.SuggestedConcurrent,
		Model:        cfg.Dispatcher.Model,
		ModelByPhase: cfg.Dispatcher.ModelByPhase,
	})

	// V2.0 multi-LLM factory. Always registers "opencode" as default;
	// "ollama" is opt-in (registered only when SOPHIA_OLLAMA_CMD is
	// set, see config.OllamaConfig). The default provider name comes
	// from cfg.Dispatcher.Provider; empty falls back to "opencode"
	// for V1 backward compat.
	defaultProvider := cfg.Dispatcher.Provider
	if defaultProvider == "" {
		defaultProvider = "opencode"
	}
	dispatcherFactory := factory.New(defaultProvider, opencodeAdapter)
	if cfg.Dispatcher.Ollama.Cmd != "" {
		ollamaAdapter := ollama.New(rtClient, ollama.Config{
			Cmd:          cfg.Dispatcher.Ollama.Cmd,
			Suggested:    cfg.Dispatcher.Ollama.SuggestedConcurrent,
			Model:        cfg.Dispatcher.Ollama.Model,
			ModelByPhase: cfg.Dispatcher.Ollama.ModelByPhase,
		})
		dispatcherFactory.Register("ollama", ollamaAdapter)
	}
	if cfg.Dispatcher.Aider.Cmd != "" {
		aiderAdapter := aider.New(rtClient, aider.Config{
			Cmd:          cfg.Dispatcher.Aider.Cmd,
			Suggested:    cfg.Dispatcher.Aider.SuggestedConcurrent,
			Model:        cfg.Dispatcher.Aider.Model,
			ModelByPhase: cfg.Dispatcher.Aider.ModelByPhase,
		})
		dispatcherFactory.Register("aider", aiderAdapter)
	}
	// MCP host-bridge dispatcher — opt-in: registered ONLY when BridgeURL
	// is configured. Fail-fast guards already ran before pool.Open above.
	if cfg.Dispatcher.MCP.BridgeURL != "" {
		mcpAdapter := mcpdispatcher.New(nil, mcpdispatcher.Config{
			BridgeURL:         cfg.Dispatcher.MCP.BridgeURL,
			Token:             cfg.Dispatcher.MCP.Token,
			Origin:            cfg.Dispatcher.MCP.Origin,
			Transport:         cfg.Dispatcher.MCP.Transport,
			TimeoutMS:         cfg.Dispatcher.MCP.TimeoutMS,
			ProviderAllowlist: cfg.Dispatcher.MCP.ProviderAllowlist,
			DefaultModel:      cfg.Dispatcher.MCP.DefaultModel,
			ModelByPhase:      cfg.Dispatcher.MCP.ModelByPhase,
			Provider:          cfg.Dispatcher.MCP.Provider,
			DefaultCWD:        cfg.Dispatcher.MCP.DefaultCWD,
		})
		dispatcherFactory.Register("mcp", mcpAdapter)
	}
	// Wrap factory in an AgentDispatcher facade so service.go +
	// teamlead.go keep talking to a single dispatcher instance.
	dispatcher := factory.NewWrappingDispatcher(dispatcherFactory, cfg.Dispatcher.ProviderByPhase)

	// Discipline services.
	clock := shared.SystemClock{}
	idGen := shared.NewSystemIDGenerator(clock)
	validator := discipline.NewValidator()
	ironLaw := discipline.NewIronLawChecker()
	prompts := discipline.NewPromptBuilder()
	spawnGov, err := discipline.NewSpawnGovernor(spawnRepo, discipline.SpawnGovernorConfig{
		Max:          cfg.Spawn.Max,
		StaggerMin:   cfg.Spawn.StaggerMin,
		StaggerMax:   cfg.Spawn.StaggerMax,
		WaitInterval: cfg.Spawn.WaitInterval,
		MaxWait:      cfg.Spawn.MaxWait,
	}, clock)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("bootstrap: spawn governor: %w", err)
	}

	// SkillMatcher wiring (M3: PGSkillMatcher handles context-aware filtering:
	// scope, applies_when, structural, risk_level sort).
	// When SOPHIA_SKILLS_ENABLED=false, a nil matcher is passed to all services
	// so prompts remain byte-identical to the pre-change baseline (fail-soft).
	var skillMatcher discipline.SkillMatcher
	var skillSvc *skillapp.Service
	if cfg.SkillsEnabled {
		skillMatcher = pg.NewPGSkillMatcher(pool, skillRepo)
		skillSvc = skillapp.New(skillRepo, skillUsageRepo, clock)
	}

	// Observability: Prometheus metrics + OTEL traces. Constructed BEFORE
	// application services so metrics can be injected into them.
	var metrics *obs.Metrics
	if cfg.Obs.MetricsEnabled {
		metrics = obs.NewMetrics()
	}

	// Application services. EventStore is required for durable SSE
	// (audit rojo #3): every Publish persists before broadcasting, so
	// the CLI can resume via Last-Event-ID after a server restart.
	eventStore := pg.NewEventStore(pool)
	events := eventstream.New(0, eventStore, nil, nil)
	changeSvc := change.New(changeRepo, clock, idGen).WithMetrics(metrics)
	applySvc := apply.New(boardRepo)

	// Apply-phase parallel coordination (spec § 5).
	// Spec #61 (BUG-15): WorktreeRoot is overridable via
	// SOPHIA_APPLY_WORKTREE_ROOT so MCP-bridge deployments can point
	// it at a host-mounted path the bridge can see. Empty preserves
	// the container-local default.
	applyRunCfg := apply.DefaultRunConfig()
	if root := cfg.Apply.WorktreeRoot; root != "" {
		applyRunCfg.WorktreeRoot = root
	}
	// Spec #65 (BUG-19): when configured, pre-populate every worktree
	// with a copy of the source repo so the implement agent has source
	// to read and edit. Empty keeps the legacy empty-mkdir behaviour.
	applyRunCfg.SourceRepoPath = cfg.Apply.SourceRepoPath
	// BUG-27: per-cycle override of the worktree init strategy. Operators
	// switch to "empty" for cross-language new-feature cycles where the
	// orch's Go source tree would confuse the implement LLM.
	applyRunCfg.WorktreeInit = cfg.Apply.WorktreeInit
	// BUG-29: operator-facing target where successful worktrees land at
	// end of apply. Empty preserves the legacy behaviour of leaving
	// worktrees isolated under WorktreeRoot.
	applyRunCfg.TargetPath = cfg.Apply.TargetPath
	// ADR-0010 Slice 3: configurable short dispatch timeout. When
	// SOPHIA_DISPATCH_TIMEOUT_MS is set (> 0), forward it to RunConfig
	// so operators can tune the per-dispatch deadline. Zero keeps the
	// apply.DefaultRunConfig default (180_000 ms = 3min).
	if cfg.Apply.DispatchTimeoutMS > 0 {
		applyRunCfg.DispatchTimeoutMS = cfg.Apply.DispatchTimeoutMS
	}
	// ADR-0010 Slice 4: fallback model for quota exhaustion. When
	// SOPHIA_DISPATCHER_FALLBACK_MODEL is set, the apply phase will
	// re-dispatch a quota-failed task once with ModelOverride = FallbackModel
	// before triggering the Slice-2 fail-fast. Empty = no fallback.
	applyRunCfg.FallbackModel = cfg.Apply.FallbackModel
	// ADR-0010 Slice 5: phase quota circuit breaker threshold. When
	// SOPHIA_APPLY_QUOTA_BREAKER_THRESHOLD is set (> 0), override the
	// package default (3). Zero or unset keeps the default.
	if cfg.Apply.QuotaBreakerThreshold > 0 {
		applyRunCfg.QuotaBreakerThreshold = cfg.Apply.QuotaBreakerThreshold
	}
	applyExecutor := apply.NewRun(apply.RunDeps{
		BoardRepo:      boardRepo,
		SessionRepo:    sessionRepo,
		Runtime:        rtClient,
		Dispatcher:     dispatcher,
		SpawnGov:       spawnGov,
		Validator:      validator,
		Prompts:        prompts,
		Audit:          auditLog,
		Events:         events,
		Memory:         memClient,
		Clock:          clock,
		IDGen:          idGen,
		Config:         applyRunCfg,
		Metrics:        metrics,
		Skills:         skillMatcher,     // nil when SOPHIA_SKILLS_ENABLED=false (M3: SkillMatcher)
		SkillUsageRepo: skillUsageRepo,   // M2: track skill injection events
	})

	// INIT phase wiring (design D-INIT-4, D-INIT-7 through D-INIT-11).
	// All subprocess and HTTP calls are behind interfaces; real adapters wired
	// here; tests inject fakes directly into initphase.Deps.
	initDetector := initphase.NewDetectorAdapter(detector.New())
	initExecRunner := execadapter.NewRealRunner()
	initSpawner := graphifyadapter.NewSpawner(initExecRunner, logger, 0) // 0 = use SOPHIA_GRAPHIFY_TIMEOUT_MS or 30s default
	initGitRunner := gitrunneradapter.NewExecRunner()
	initFileReader := initphase.NewOSFileReader()
	initKeyBuilder := initphase.NewKeyBuilder(initGitRunner, initFileReader)
	initFileCacheDir := ".sophia/cache/structural" // relative to cwd (repo root at boot)
	initFileCache := initcache.NewFileCache(initFileCacheDir, clock, 24*time.Hour)
	initDualPersister := initpersister.New(memClient, initFileCache, logger, cfg.Memory.TenantID, cfg.Environment)
	initSvc := initphase.NewService(initphase.Deps{
		Detector:  initDetector,
		Spawner:   initSpawner,
		Persister: initDualPersister,
		Cache:     initFileCache,
		CacheKey:  initKeyBuilder,
		Clock:     clock,
		IDGen:     idGen,
		Logger:    logger,
		CacheTTL:  24 * time.Hour,
	})

	// Memory-engine webhook bridge (M2 D-M2-1): fire-and-forget POST after phase.archived.
	// nil when SOPHIA_MEMORY_WEBHOOK_URL is empty (adapter disabled).
	var webhookNotifier phase.WebhookNotifier
	if cfg.MemoryWebhook.URL != "" {
		wh := webhookadapter.New(webhookadapter.Config{
			URL:     cfg.MemoryWebhook.URL,
			APIKey:  cfg.MemoryWebhook.APIKey,
			Timeout: time.Duration(cfg.MemoryWebhook.TimeoutMS) * time.Millisecond,
		})
		webhookNotifier = webhookadapter.NewPhaseBridge(wh)
	}

	phaseSvc := phase.New(phase.Deps{
		ChangeRepo:      changeRepo,
		PhaseRepo:       phaseRepo,
		SessionRepo:     sessionRepo,
		Governance:      govClient,
		Memory:          memClient,
		Dispatcher:      dispatcher,
		SpawnGov:        spawnGov,
		Validator:       validator,
		IronLaw:         ironLaw,
		Prompts:         prompts,
		Audit:           auditLog,
		Events:          events,
		Clock:           clock,
		IDGen:           idGen,
		Scheduler:       phase.AsyncScheduler,
		Config: func() phase.ServiceConfig {
			c := phase.DefaultServiceConfig()
			// Tenant binding for memory-engine ingest. Empty in
			// single-tenant deployments; required to match the API
			// key's bound tenant in multi-tenant ones (otherwise
			// memory returns 403 on persistArtifactsToMemory).
			c.MemoryTenantID = cfg.Memory.TenantID
			return c
		}(),
		ApplyExecutor:   applyExecutor,
		Metrics:         metrics,
		Skills:          skillMatcher,     // nil when SOPHIA_SKILLS_ENABLED=false (M3: SkillMatcher)
		SkillUsageRepo:  skillUsageRepo,   // M2: track skill injection events
		WebhookNotifier: webhookNotifier,  // M2: fire-and-forget phase.archived (nil = disabled)
		Init:            initSvc,          // INIT phase structural detection (D-INIT-3)
	})

	tracer, err := obs.NewTracer(ctx, obs.TraceConfig{
		Enabled:     cfg.Obs.TracesEnabled,
		Endpoint:    cfg.Obs.OTLPEndpoint,
		Insecure:    cfg.Obs.OTLPInsecure,
		ServiceName: "sophia-orchestator",
		Version:     cfg.Obs.Version,
		Environment: cfg.Environment,
		SampleRatio: cfg.Obs.TraceSampleRate,
	})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("bootstrap: tracer: %w", err)
	}

	// Inbound HTTP.
	auth := newStaticAuthn(cfg.HTTP.APIKey, cfg.HTTP.APIKeyProject)

	// AllowAnonLocalhost is the EFFECTIVE composition of (config flag
	// AND listener-bound-to-loopback) per sophia-wire-v1 §3.2 / D-M10-02.
	// If the operator sets the flag but binds to a non-loopback
	// interface, the flag is silently downgraded to false; this avoids
	// accidentally exposing an unauthenticated endpoint on a routable
	// IP. A warning is logged when downgrading happens.
	effectiveAllowAnon := cfg.HTTP.AllowAnonLocalhost && middleware.IsLoopbackAddr(cfg.HTTP.Addr)
	if cfg.HTTP.AllowAnonLocalhost && !effectiveAllowAnon {
		logger.Warn("AllowAnonLocalhost requested but listener is not loopback-bound; auth required for all requests",
			slog.String("addr", cfg.HTTP.Addr))
	}

	routerDeps := httpinbound.Deps{
		Changes:            changeSvc,
		Phases:             phaseSvc,
		Apply:              applySvc,
		Events:             events,
		EventStore:         eventStore,
		PhaseRepo:          phaseRepo,
		Auth:               auth,
		Logger:             logger,
		StartedAt:          time.Now(),
		Ready:              readinessFor(pool),
		Metrics:            metrics,
		AllowAnonLocalhost: effectiveAllowAnon,
		IDGen:              idGen,
		Skills:             skillSvc, // M2: skills write API (nil when SOPHIA_SKILLS_ENABLED=false)
	}
	if tracer.Enabled() {
		routerDeps.Tracer = tracer.Tracer("sophia-orchestator/http")
	}
	router := httpinbound.NewRouter(routerDeps)

	srv := &http.Server{
		Addr:        cfg.HTTP.Addr,
		Handler:     router,
		ReadTimeout: cfg.HTTP.ReadTimeout,
		// WriteTimeout intentionally 0 — SSE long-poll incompatible with it.
	}

	// Skill seeder — upserts the 9 canonical phase skills on every boot.
	// Runs after migrations, before HTTP serve, so the first prompt
	// hydration always finds seeded rows. Upsert keeps lifecycle fields
	// up-to-date while preserving operator-edited content via (name, version).
	if cfg.SkillsEnabled {
		if err := SeedSkills(ctx, skillRepo, clock, logger); err != nil {
			pool.Close()
			return nil, fmt.Errorf("bootstrap: skill seeder: %w", err)
		}
	}

	// Spec #68 (BUG-23): boot-time recovery scan. Mark every phase
	// left at PhaseStatusRunning by a previous crashed process as
	// PhaseStatusInterrupted so the operator's status polls show a
	// clear "needs Resume" signal instead of a phantom "running"
	// row no goroutine owns. Runs BEFORE HTTP listen so the first
	// status query returns the post-recovery state.
	recoverySvc := recovery.NewService(phaseRepo, logger)
	recoveryCtx, recoveryCancel := context.WithTimeout(ctx, 30*time.Second)
	defer recoveryCancel()
	if marked, err := recoverySvc.MarkStuckInterrupted(recoveryCtx); err != nil {
		// Fail-soft: log but keep booting. Even partial recovery is
		// better than refusing to start.
		logger.Error("bootstrap: recovery scan returned error", slog.String("err", err.Error()), slog.Int("marked", marked))
	}

	return &App{cfg: cfg, logger: logger, pool: pool, server: srv, tracer: tracer}, nil
}

// Run starts the HTTP server and blocks until ctx is cancelled or the server
// returns an unrecoverable error.
func (a *App) Run(ctx context.Context) error {
	a.logger.Info("sophia-orchestator starting", slog.String("addr", a.cfg.HTTP.Addr), slog.String("env", a.cfg.Environment))

	errCh := make(chan error, 1)
	go func() {
		if err := a.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		a.logger.Info("shutdown signal received")
	case err := <-errCh:
		if err != nil {
			return err
		}
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), a.cfg.HTTP.ShutdownTimeout)
	defer cancel()
	if err := a.server.Shutdown(shutCtx); err != nil {
		return fmt.Errorf("bootstrap: shutdown: %w", err)
	}
	return nil
}

// Close releases resources without serving HTTP. Used by tests.
func (a *App) Close() {
	if a.pool != nil {
		a.pool.Close()
	}
	if a.tracer != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.tracer.Shutdown(shutCtx)
	}
}

// staticAuthn is the V1 single-tenant authenticator: a single env-configured
// API key authorizes a single project. V2 swaps for OIDC + per-project
// keys stored in the api_keys table.
type staticAuthn struct {
	keyHash string
	project string
}

func newStaticAuthn(key, project string) middleware.Authenticator {
	if key == "" {
		return nil // disable auth
	}
	return &staticAuthn{
		keyHash: middleware.HashAPIKey(key),
		project: project,
	}
}

func (a *staticAuthn) Validate(_ middleware.ContextProvider, key string) (string, error) {
	if middleware.HashAPIKey(key) != a.keyHash {
		return "", errors.New("invalid api key")
	}
	return a.project, nil
}

func newLogger(cfg config.Config) *slog.Logger {
	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	// Wrap the JSON handler with TraceHandler so every log line emitted within
	// a request context automatically carries trace_id and span_id attributes
	// (ADR-0005 P2.2a).
	base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	return slog.New(obslog.NewTraceHandler(base))
}

func readinessFor(pool *pgxpool.Pool) func() error {
	return func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			return fmt.Errorf("readiness: pg: %w", err)
		}
		return nil
	}
}

// guard against http_base import-cycle warnings since we don't use it directly here.
var _ = httpbase.DefaultConfig
