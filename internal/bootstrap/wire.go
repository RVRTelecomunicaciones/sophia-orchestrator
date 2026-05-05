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
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/dispatcher/opencode"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/governance"
	httpbase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/http_base"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/memory"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/pg"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/runtime"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/eventstream"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/config"
	dbpkg "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/db"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/obs"
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
	_ = boardRepo // used by ApplyService below

	// Outbound HTTP clients.
	govClient, err := governance.New(governance.DefaultConfig(cfg.Governance.BaseURL, cfg.Governance.APIKey))
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("bootstrap: governance: %w", err)
	}
	memClient, err := memory.New(memory.DefaultConfig(cfg.Memory.BaseURL, cfg.Memory.APIKey))
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("bootstrap: memory: %w", err)
	}
	rtClient, err := runtime.New(runtime.DefaultConfig(cfg.Runtime.BaseURL, cfg.Runtime.APIKey))
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("bootstrap: runtime: %w", err)
	}
	dispatcher := opencode.New(rtClient, opencode.Config{
		Cmd:       cfg.Dispatcher.Cmd,
		Suggested: cfg.Dispatcher.SuggestedConcurrent,
	})

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

	// Application services.
	events := eventstream.New(0, nil)
	changeSvc := change.New(changeRepo, clock, idGen)
	applySvc := apply.New(boardRepo)

	// Apply-phase parallel coordination (spec § 5).
	applyExecutor := apply.NewRun(apply.RunDeps{
		BoardRepo:   boardRepo,
		SessionRepo: sessionRepo,
		Runtime:     rtClient,
		Dispatcher:  dispatcher,
		SpawnGov:    spawnGov,
		Validator:   validator,
		Prompts:     prompts,
		Audit:       auditLog,
		Events:      events,
		Memory:      memClient,
		Clock:       clock,
		IDGen:       idGen,
		Config:      apply.DefaultRunConfig(),
	})

	phaseSvc := phase.New(phase.Deps{
		ChangeRepo:  changeRepo,
		PhaseRepo:   phaseRepo,
		SessionRepo: sessionRepo,
		Governance:  govClient,
		Memory:      memClient,
		Dispatcher:  dispatcher,
		SpawnGov:    spawnGov,
		Validator:   validator,
		IronLaw:     ironLaw,
		Prompts:     prompts,
		Audit:       auditLog,
		Events:      events,
		Clock:       clock,
		IDGen:       idGen,
		Scheduler:     phase.AsyncScheduler,
		Config:        phase.DefaultServiceConfig(),
		ApplyExecutor: applyExecutor,
	})

	// Observability: Prometheus metrics + OTEL traces.
	var metrics *obs.Metrics
	if cfg.Obs.MetricsEnabled {
		metrics = obs.NewMetrics()
	}
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
	routerDeps := httpinbound.Deps{
		Changes:   changeSvc,
		Phases:    phaseSvc,
		Apply:     applySvc,
		Events:    events,
		Auth:      auth,
		Logger:    logger,
		StartedAt: time.Now(),
		Ready:     readinessFor(pool),
		Metrics:   metrics,
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
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
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
