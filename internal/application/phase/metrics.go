package phase

import (
	"strings"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/obs"
)

// recordPhaseTerminal increments the per-phase counters + histograms when
// a Phase reaches a terminal status (done / done_with_concerns / blocked /
// needs_context). Safe to call when Metrics is nil (no-op).
func (s *Service) recordPhaseTerminal(p *phase.Phase, env *envelope.Envelope) {
	m := s.d.Metrics
	if m == nil {
		return
	}
	m.PhasesTotal.WithLabelValues(string(p.Type()), string(p.Status())).Inc()
	if p.StartedAt() != nil && p.CompletedAt() != nil {
		dur := p.CompletedAt().Sub(*p.StartedAt())
		m.PhaseDurationMS.WithLabelValues(string(p.Type())).Observe(float64(dur.Milliseconds()))
	}
	if env != nil {
		m.PhaseConfidence.WithLabelValues(string(p.Type())).Observe(env.Confidence)
		if p.Status() == phase.PhaseStatusBlocked || p.Status() == phase.PhaseStatusNeedsContext {
			reason := classifyEnvelopeReason(env)
			m.EnvelopeValidationFailures.WithLabelValues(string(p.Type()), reason).Inc()
		}
	}
}

// recordPhaseStarted increments running gauge.
func (s *Service) recordPhaseStarted(_ *phase.Phase) {
	m := s.d.Metrics
	if m == nil {
		return
	}
	m.PhasesRunning.Inc()
}

// recordPhaseEnded decrements running gauge.
func (s *Service) recordPhaseEnded(_ *phase.Phase) {
	m := s.d.Metrics
	if m == nil {
		return
	}
	m.PhasesRunning.Dec()
}

// recordSession instruments AgentSession lifecycle counters/histograms.
func (s *Service) recordSession(sess *session.Session) {
	m := s.d.Metrics
	if m == nil || sess == nil {
		return
	}
	m.AgentSessionsTotal.WithLabelValues(
		string(sess.Role()), string(sess.Provider()), string(sess.Status()),
	).Inc()
	if sess.EndedAt() != nil {
		dur := sess.EndedAt().Sub(sess.StartedAt())
		m.AgentSessionDurationMS.WithLabelValues(string(sess.Role()), string(sess.Provider())).Observe(float64(dur.Milliseconds()))
	}
}

// recordIronLawViolation logs Iron Law breaches + increments the counter.
func (s *Service) recordIronLawViolation(lawID string) {
	m := s.d.Metrics
	if m == nil || lawID == "" {
		return
	}
	m.IronLawViolationsTotal.WithLabelValues(lawID).Inc()
}

// recordSSEEvent counts SSE emissions by event_type.
func (s *Service) recordSSEEvent(eventType string) {
	m := s.d.Metrics
	if m == nil {
		return
	}
	m.SSEEventsEmitted.WithLabelValues(eventType).Inc()
}

// recordGovernanceCall increments GovernanceCallsTotal after an EvaluatePhase
// call. op must be a bounded string (e.g. "evaluate_phase").
func (s *Service) recordGovernanceCall(op string, err error) {
	m := s.d.Metrics
	if m == nil {
		return
	}
	status := "ok"
	if err != nil {
		status = "error"
	}
	m.GovernanceCallsTotal.WithLabelValues(op, status).Inc()
}

// recordDispatcherCall increments DispatcherCallsTotal and observes
// DispatcherCallDurationMS. provider should be the string form of the
// dispatcher's Provider() value.
func (s *Service) recordDispatcherCall(provider string, durationMS float64, err error) {
	m := s.d.Metrics
	if m == nil {
		return
	}
	status := "ok"
	if err != nil {
		status = "error"
	}
	m.DispatcherCallsTotal.WithLabelValues(provider, status).Inc()
	m.DispatcherCallDurationMS.WithLabelValues(provider).Observe(durationMS)
}

// recordMemoryCall increments MemoryCallsTotal. op must be a bounded string
// (e.g. "ingest", "get", "get_by_topic_key", "search", "build_context").
func (s *Service) recordMemoryCall(op string, err error) {
	m := s.d.Metrics
	if m == nil {
		return
	}
	status := "ok"
	if err != nil {
		status = "error"
	}
	m.MemoryCallsTotal.WithLabelValues(op, status).Inc()
}

// classifyEnvelopeReason maps an envelope failure to a bounded reason
// label for the EnvelopeValidationFailures counter. Keeps cardinality low.
func classifyEnvelopeReason(env *envelope.Envelope) string {
	if env == nil {
		return "nil_envelope"
	}
	switch env.Status {
	case envelope.StatusBlocked:
		// Use a coarse classification of the executive summary to avoid
		// high-cardinality labels.
		summary := strings.ToLower(env.ExecutiveSummary)
		switch {
		case strings.Contains(summary, "iron law"):
			return "iron_law"
		case strings.Contains(summary, "governance"):
			return "governance"
		case strings.Contains(summary, "dispatch"):
			return "dispatch"
		case strings.Contains(summary, "envelope"):
			return "envelope_validation"
		case strings.Contains(summary, "spawn"):
			return "spawn_governor"
		default:
			return "blocked_other"
		}
	case envelope.StatusNeedsContext:
		return "needs_context"
	}
	return "ok"
}

// Compile-time guard the obs import is used (some build modes strip
// otherwise-unused imports — keep the bind explicit for clarity).
var _ = obs.NewMetrics
