package phase

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
)

// buildApplyFailedPayload constructs the enriched PhaseFailedPayload
// emitted when the apply phase terminates in envelope.StatusBlocked.
//
// Spec #51 — pre-fix runApplyPhase emitted PhaseCompletedFromApplyPayload
// (slim: just envelope_status + envelope_confidence) on every terminal
// status including BLOCKED. Consumers watching SSE for phase.failed
// got no information about WHY apply blocked; the reason lived in
// agent_sessions.envelope and required DB access. This helper produces
// the same shape failPhaseWithReason emits in the single-agent path so
// the wire stays uniform across apply and non-apply phases.
//
// FailureDetail concatenates the envelope's executive_summary with any
// blocking_reasons declared in envelope.Data, so an operator sees the
// human-readable summary AND the structured blockers in a single field.
func buildApplyFailedPayload(pid ids.PhaseID, pt phase.PhaseType, endedAt time.Time, env *envelope.Envelope) inbound.PhaseFailedPayload {
	if env == nil {
		return inbound.PhaseFailedPayload{
			PhaseID:       pid.String(),
			PhaseType:     string(pt),
			EndedAt:       endedAt,
			Error:         "apply executor returned nil envelope",
			FailureReason: "unknown",
		}
	}
	summary := env.ExecutiveSummary
	detail := summary
	if blockers := extractApplyEnvelopeBlockers(env); len(blockers) > 0 {
		var sb strings.Builder
		if summary != "" {
			sb.WriteString(summary)
			sb.WriteString("\n\nBlockers:\n")
		} else {
			sb.WriteString("Blockers:\n")
		}
		for _, b := range blockers {
			sb.WriteString("- ")
			sb.WriteString(b)
			sb.WriteString("\n")
		}
		detail = strings.TrimRight(sb.String(), "\n")
	}
	return inbound.PhaseFailedPayload{
		PhaseID:       pid.String(),
		PhaseType:     string(pt),
		EndedAt:       endedAt,
		Error:         summary,
		FailureReason: "apply.blocked",
		FailureDetail: detail,
	}
}

// extractApplyEnvelopeBlockers mirrors apply.extractBlockingReasons but
// duplicated here to avoid an inter-package dependency cycle (phase
// imports apply would invert the existing direction). Keep both
// implementations behaviorally identical; the test suite covers the
// canonical version in package apply.
func extractApplyEnvelopeBlockers(env *envelope.Envelope) []string {
	if env == nil || len(env.Data) == 0 {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(env.Data, &raw); err != nil {
		return nil
	}
	for _, key := range []string{
		"blocking_reasons",
		"blocking_requirements",
		"blockers",
	} {
		val, ok := raw[key]
		if !ok {
			continue
		}
		var list []string
		if err := json.Unmarshal(val, &list); err != nil {
			continue
		}
		out := make([]string, 0, len(list))
		for _, s := range list {
			if t := strings.TrimSpace(s); t != "" {
				out = append(out, t)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}
