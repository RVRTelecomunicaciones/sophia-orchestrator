package apply

import (
	"encoding/json"
	"strings"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
)

// extractBlockingReasons surfaces the LLM-emitted "why am I blocked"
// signals from an envelope.Data payload so SSE consumers (and audit
// logs) can see the real reason without needing DB access to
// agent_sessions.envelope.
//
// Spec #51 — different SDD prompts and different models use different
// keys to communicate blockers. We probe them in priority order and
// return the first non-empty array found. Empty strings inside the
// array are dropped so consumers see a clean list. Returns nil when:
//   - env is nil
//   - env.Data is empty or not a JSON object
//   - none of the candidate keys hold a non-empty []string
//
// Failure semantics are silent on purpose: this helper feeds an
// SSE-payload enrichment field, not a control-flow decision. If we
// can't parse a list we just emit the rest of the payload without it
// rather than fail the escalation event.
func extractBlockingReasons(env *envelope.Envelope) []string {
	if env == nil || len(env.Data) == 0 {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(env.Data, &raw); err != nil {
		return nil
	}
	// Probe in priority order. First non-empty match wins.
	for _, key := range []string{
		"blocking_reasons",      // gpt-5.4 verbatim in smoke v3
		"blocking_requirements", // earlier smoke runs
		"blockers",              // compact form used by some prompts
	} {
		raw, ok := raw[key]
		if !ok {
			continue
		}
		var list []string
		if err := json.Unmarshal(raw, &list); err != nil {
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
