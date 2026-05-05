// Package obs centralizes the orchestrator's observability primitives:
// Prometheus metrics registry, OpenTelemetry tracer setup, and the chi-
// compatible middlewares that record per-request traces + metrics.
//
// Metrics are exported on /metrics by the inbound HTTP router. Cardinality
// guards (spec § 9.2): label whitelist {phase_type, status, role, provider,
// op, capability, event_type, law_id, reason, project}. High-cardinality
// IDs (change_id, phase_id, session_id, trace_id) go to logs / OTEL span
// attributes, never to Prometheus labels.
package obs

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics bundles every Prometheus instrument the orchestrator exports.
// Registered against a private registry so we can serve a clean /metrics
// without leaking Go runtime metrics from the default registry (those are
// added explicitly via promCollectors below if desired).
type Metrics struct {
	reg *prometheus.Registry

	// Counters.
	ChangesTotal              *prometheus.CounterVec
	PhasesTotal               *prometheus.CounterVec
	AgentSessionsTotal        *prometheus.CounterVec
	EnvelopeValidationFailures *prometheus.CounterVec
	IronLawViolationsTotal    *prometheus.CounterVec
	DispatcherCallsTotal      *prometheus.CounterVec
	GovernanceCallsTotal      *prometheus.CounterVec
	MemoryCallsTotal          *prometheus.CounterVec
	RuntimeCallsTotal         *prometheus.CounterVec
	SpawnGovernorThrottled    *prometheus.CounterVec
	SSEEventsEmitted          *prometheus.CounterVec
	ApplyGroupsTotal          *prometheus.CounterVec
	ApplyTasksTotal           *prometheus.CounterVec

	// Histograms.
	PhaseDurationMS         *prometheus.HistogramVec
	PhaseConfidence         *prometheus.HistogramVec
	AgentSessionDurationMS  *prometheus.HistogramVec
	ApplyTaskAttempts       prometheus.Histogram
	SpawnGovernorWaitMS     prometheus.Histogram
	DispatcherCallDurationMS *prometheus.HistogramVec
	HTTPRequestDurationMS   *prometheus.HistogramVec

	// Gauges.
	SpawnGovernorActive  prometheus.Gauge
	SSEConnectionsActive prometheus.Gauge
	ChangesInFlight      prometheus.Gauge
	PhasesRunning        prometheus.Gauge
}

// NewMetrics constructs the metrics registry with all instruments
// declared in spec § 9.2.
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{reg: reg}

	const ns = "sophia_orchestator"

	m.ChangesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "changes_total", Help: "SDD changes by project + status."},
		[]string{"project", "status"},
	)
	m.PhasesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "phases_total", Help: "Phase executions by type + status."},
		[]string{"phase_type", "status"},
	)
	m.AgentSessionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "agent_sessions_total", Help: "AgentSessions by role + provider + status."},
		[]string{"role", "provider", "status"},
	)
	m.EnvelopeValidationFailures = prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "envelope_validation_failures_total", Help: "Envelope validation failures by phase + reason."},
		[]string{"phase_type", "reason"},
	)
	m.IronLawViolationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "iron_law_violations_total", Help: "Iron Law violations by law id."},
		[]string{"law_id"},
	)
	m.DispatcherCallsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "dispatcher_calls_total", Help: "Dispatcher invocations by provider + status."},
		[]string{"provider", "status"},
	)
	m.GovernanceCallsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "governance_calls_total", Help: "Governance calls by op + status."},
		[]string{"op", "status"},
	)
	m.MemoryCallsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "memory_calls_total", Help: "memory-engine calls by op + status."},
		[]string{"op", "status"},
	)
	m.RuntimeCallsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "runtime_calls_total", Help: "runtime-adapters calls by capability + status."},
		[]string{"capability", "status"},
	)
	m.SpawnGovernorThrottled = prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "spawn_governor_throttled_total", Help: "Spawn-governor throttle events by reason."},
		[]string{"reason"},
	)
	m.SSEEventsEmitted = prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "sse_events_emitted_total", Help: "SSE events emitted by event_type."},
		[]string{"event_type"},
	)
	m.ApplyGroupsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "apply_groups_total", Help: "Apply groups completed by status."},
		[]string{"status"},
	)
	m.ApplyTasksTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "apply_tasks_total", Help: "Apply tasks completed by status."},
		[]string{"status"},
	)

	phaseDurationBuckets := []float64{100, 500, 1000, 5000, 30000, 60000, 300000, 600000, 1800000}
	m.PhaseDurationMS = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Namespace: ns, Name: "phase_duration_ms", Help: "Phase wall-clock duration in milliseconds.", Buckets: phaseDurationBuckets},
		[]string{"phase_type"},
	)
	m.PhaseConfidence = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Namespace: ns, Name: "phase_confidence", Help: "Phase envelope confidence values.", Buckets: []float64{0.1, 0.3, 0.5, 0.7, 0.8, 0.9, 1.0}},
		[]string{"phase_type"},
	)
	m.AgentSessionDurationMS = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Namespace: ns, Name: "agent_session_duration_ms", Help: "AgentSession wall-clock duration.", Buckets: phaseDurationBuckets},
		[]string{"role", "provider"},
	)
	m.ApplyTaskAttempts = prometheus.NewHistogram(
		prometheus.HistogramOpts{Namespace: ns, Name: "apply_task_attempts", Help: "Apply task attempts before terminal state.", Buckets: []float64{1, 2, 3, 4, 5}},
	)
	m.SpawnGovernorWaitMS = prometheus.NewHistogram(
		prometheus.HistogramOpts{Namespace: ns, Name: "spawn_governor_wait_ms", Help: "SpawnGovernor.Acquire wait time in ms.", Buckets: []float64{50, 100, 250, 500, 1000, 5000}},
	)
	m.DispatcherCallDurationMS = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Namespace: ns, Name: "dispatcher_call_duration_ms", Help: "Dispatcher invocation duration.", Buckets: []float64{1000, 5000, 10000, 30000, 60000, 300000}},
		[]string{"provider"},
	)
	m.HTTPRequestDurationMS = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Namespace: ns, Name: "http_request_duration_ms", Help: "Inbound HTTP request duration.", Buckets: []float64{1, 5, 10, 50, 100, 250, 500, 1000, 5000}},
		[]string{"method", "route", "status"},
	)

	m.SpawnGovernorActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: ns, Name: "spawn_governor_active", Help: "Currently-active dispatcher subprocesses.",
	})
	m.SSEConnectionsActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: ns, Name: "sse_connections_active", Help: "Currently-open SSE connections.",
	})
	m.ChangesInFlight = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: ns, Name: "changes_in_flight", Help: "Changes with status=active.",
	})
	m.PhasesRunning = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: ns, Name: "phases_running", Help: "Phases with status=running.",
	})

	reg.MustRegister(
		m.ChangesTotal, m.PhasesTotal, m.AgentSessionsTotal,
		m.EnvelopeValidationFailures, m.IronLawViolationsTotal,
		m.DispatcherCallsTotal, m.GovernanceCallsTotal,
		m.MemoryCallsTotal, m.RuntimeCallsTotal,
		m.SpawnGovernorThrottled, m.SSEEventsEmitted,
		m.ApplyGroupsTotal, m.ApplyTasksTotal,
		m.PhaseDurationMS, m.PhaseConfidence, m.AgentSessionDurationMS,
		m.ApplyTaskAttempts, m.SpawnGovernorWaitMS,
		m.DispatcherCallDurationMS, m.HTTPRequestDurationMS,
		m.SpawnGovernorActive, m.SSEConnectionsActive,
		m.ChangesInFlight, m.PhasesRunning,
	)

	return m
}

// Handler returns the http.Handler that exposes /metrics. Wire it into the
// inbound router (un-authenticated, alongside /api/v1/health).
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
}

// Registry exposes the Prometheus registry for tests.
func (m *Metrics) Registry() *prometheus.Registry { return m.reg }
