package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel/trace"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/inbound/http/handlers"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/inbound/http/middleware"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/obs"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
)

// Deps bundles the dependencies of NewRouter.
type Deps struct {
	Changes   inbound.ChangeService
	Phases    inbound.PhaseService
	Apply     inbound.ApplyService
	Events    inbound.EventStream
	Auth      middleware.Authenticator
	Logger    *slog.Logger
	StartedAt time.Time
	Ready     func() error

	// Observability (optional). When nil, the corresponding middleware is a no-op.
	Metrics *obs.Metrics
	Tracer  trace.Tracer
}

// NewRouter assembles the chi router with all middleware + handlers per spec
// § 7. Returns a chi.Router ready to be served by net/http.
func NewRouter(d Deps) chi.Router {
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(middleware.Recover(d.Logger))
	if d.Tracer != nil {
		r.Use(middleware.Tracing(d.Tracer))
	}
	if d.Metrics != nil {
		r.Use(middleware.MetricsRecorder(d.Metrics.HTTPRequestDurationMS))
	}
	r.Use(middleware.Logging(d.Logger))

	// Health + ready + metrics (un-auth).
	hh := handlers.NewHealthHandler(d.StartedAt, d.Ready, writeJSON)
	r.Get("/api/v1/health", hh.Check)
	r.Get("/api/v1/ready", hh.Ready)
	if d.Metrics != nil {
		r.Method(http.MethodGet, "/metrics", d.Metrics.Handler())
	}

	// Authenticated routes.
	r.Group(func(r chi.Router) {
		if d.Auth != nil {
			r.Use(middleware.APIKey(d.Auth))
		}

		ch := handlers.NewChangesHandler(d.Changes, writeError, writeJSON)
		ph := handlers.NewPhasesHandler(d.Phases, writeError, writeJSON)
		ap := handlers.NewApplyHandler(d.Apply, writeError, writeJSON)
		sh := handlers.NewSSEHandler(d.Events, 5*time.Second, writeError)

		r.Route("/api/v1/changes", func(r chi.Router) {
			r.Post("/", ch.Create)
			r.Get("/", ch.List)
			r.Route("/{change_id}", func(r chi.Router) {
				r.Get("/", ch.Get)
				r.Post("/abort", ch.Abort)
				r.Route("/phases", func(r chi.Router) {
					r.Post("/{phase_type}/run", ph.Run)
					r.Route("/{phase_id}", func(r chi.Router) {
						r.Get("/", ph.Get)
						r.Post("/resume", ph.Resume)
						r.Post("/approve", ph.Approve)
						r.Post("/reject", ph.Reject)
						r.Get("/board", ap.GetBoard)
						r.Get("/events", sh.Stream)
					})
				})
			})
		})
	})

	// 404 fallback as JSON (chi defaults to plaintext).
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "not found", "code": "not_found",
		})
	})
	return r
}
