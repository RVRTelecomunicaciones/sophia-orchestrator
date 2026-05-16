package http

import (
	"crypto/rand"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel/trace"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/inbound/http/handlers"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/inbound/http/middleware"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/obs"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// Deps bundles the dependencies of NewRouter.
type Deps struct {
	Changes   inbound.ChangeService
	Phases    inbound.PhaseService
	Apply     inbound.ApplyService
	Events     inbound.EventStream
	EventStore outbound.EventStore // durable history for Last-Event-ID resume (audit rojo #3)
	// PhaseRepo is OPTIONAL — when supplied, the changes handler reads
	// it directly to populate `current_phase_id` on the change response
	// (a best-effort field consumed by sophia-cli Attach + Status).
	// Domain/application code keeps using the PhaseService; this is the
	// one read-only shortcut where pulling the running phase ID at the
	// edge is materially cheaper than threading a new inbound method.
	PhaseRepo  outbound.PhaseRepository
	Auth       middleware.Authenticator
	Logger    *slog.Logger
	StartedAt time.Time
	Ready     func() error
	// IDGen mints ULIDs for SSE event IDs (sophia-wire-v1 §5.1).
	// MUST be non-nil in production; tests may pass FixedIDGenerator.
	IDGen shared.IDGenerator

	// AllowAnonLocalhost: when true, the auth middleware permits requests
	// without X-Sophia-API-Key. Bootstrap MUST only set this true when
	// the listener is bound exclusively to a loopback address per
	// sophia-wire-v1 §3.2 / D-M10-02; the router itself does NOT verify
	// the listener.
	AllowAnonLocalhost bool

	// Observability (optional). When nil, the corresponding middleware is a no-op.
	Metrics *obs.Metrics
	Tracer  trace.Tracer
}

// NewRouter assembles the chi router with all middleware + handlers per
// sophia-wire-v1 §4. Returns a chi.Router ready to be served by net/http.
func NewRouter(d Deps) chi.Router {
	r := chi.NewRouter()
	// TraceW3C MUST be first: it injects the W3C Trace into context so all
	// subsequent middleware (Logging, Recover, Auth) and handlers can read it
	// via trace.FromContext(ctx). See ADR-0005 P2.2a.
	r.Use(middleware.TraceW3C(rand.Reader, d.Logger))
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

	// Health + ready + metrics (un-auth) per sophia-wire-v1 §4.1 + §4.5.
	hh := handlers.NewHealthHandler(d.StartedAt, d.Ready, writeJSON)
	r.Get("/api/v1/health", hh.Check)
	r.Get("/api/v1/ready", hh.Ready)
	if d.Metrics != nil {
		r.Method(http.MethodGet, "/metrics", d.Metrics.Handler())
	}

	// Authenticated routes per sophia-wire-v1 §4 (D-M10-13 Form A: phase
	// routes are top-level / phase-scoped; only the phase-creation route
	// stays change-scoped because the phase doesn't yet exist when /run
	// is invoked).
	r.Group(func(r chi.Router) {
		if d.Auth != nil {
			r.Use(middleware.APIKeyWithAnonOption(d.Auth, d.AllowAnonLocalhost))
		}

		// Resource-scoped error writers so 404s pick the correct
		// contract code (sophia-wire-v1 §9.2: change_not_found vs
		// phase_not_found). Other errors fall through to the generic
		// mapping.
		writeChangeErr := func(w http.ResponseWriter, err error) { writeErrorResource(w, err, "change") }
		writePhaseErr := func(w http.ResponseWriter, err error) { writeErrorResource(w, err, "phase") }

		ch := handlers.NewChangesHandler(d.Changes, d.PhaseRepo, writeChangeErr, writeJSON)
		ph := handlers.NewPhasesHandler(d.Phases, writePhaseErr, writeJSON)
		ap := handlers.NewApplyHandler(d.Apply, writePhaseErr, writeJSON)
		sh := handlers.NewSSEHandler(d.Events, d.EventStore, d.Phases, 5*time.Second, writePhaseErr, writeJSON, d.IDGen)

		// Change-scoped: create, list, get, abort, plus phase creation
		// (phase doesn't yet exist when /run is invoked, so the parent
		// change-id IS the URL identifier).
		r.Route("/api/v1/changes", func(r chi.Router) {
			r.Post("/", ch.Create)
			r.Get("/", ch.List)
			r.Route("/{change_id}", func(r chi.Router) {
				r.Get("/", ch.Get)
				r.Post("/abort", ch.Abort)
				r.Post("/phases/{phase_type}/run", ph.Run)
			})
		})

		// Phase-scoped (sophia-wire-v1 §4.3, D-M10-13 Form A): get,
		// resume, approve, reject, board, events all use the
		// globally-unique phase_id directly.
		r.Route("/api/v1/phases/{phase_id}", func(r chi.Router) {
			r.Get("/", ph.Get)
			r.Post("/resume", ph.Resume)
			r.Post("/approve", ph.Approve)
			r.Post("/reject", ph.Reject)
			r.Get("/board", ap.GetBoard)
			r.Get("/events", sh.Stream)
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
