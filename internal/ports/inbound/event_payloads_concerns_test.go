package inbound_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
)

// TestPhaseCompletedPayload_NoConcerns_ByteIdentical verifies that a
// PhaseCompletedPayload with no concerns marshals to EXACTLY the 6 legacy
// keys — the omitempty tag keeps plain phase.completed wire-identical to
// today (design D-GA-6).
func TestPhaseCompletedPayload_NoConcerns_ByteIdentical(t *testing.T) {
	p := inbound.PhaseCompletedPayload{
		PhaseID:            "p1",
		PhaseType:          "spec",
		EndedAt:            time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC),
		Confidence:         0.85,
		EnvelopeStatus:     "DONE",
		EnvelopeConfidence: 0.85,
	}
	raw, err := json.Marshal(p)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	require.Len(t, m, 6, "plain phase.completed must carry exactly 6 keys, got %v", m)
	_, hasConcerns := m["concerns"]
	require.False(t, hasConcerns, "concerns must be omitted when empty (omitempty)")
}

// TestPhaseCompletedPayload_WithConcerns verifies the concerns array appears
// only when populated, carrying the ConcernPayload wire shape.
func TestPhaseCompletedPayload_WithConcerns(t *testing.T) {
	p := inbound.PhaseCompletedPayload{
		PhaseID:            "p1",
		PhaseType:          "spec",
		EndedAt:            time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC),
		Confidence:         0.85,
		EnvelopeStatus:     "DONE_WITH_CONCERNS",
		EnvelopeConfidence: 0.85,
		Concerns: []inbound.ConcernPayload{
			{Severity: "medium", Category: "confidence", Message: "low", Evidence: "confidence=0.40<0.50"},
		},
	}
	raw, err := json.Marshal(p)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	require.Len(t, m, 7, "completed_with_concerns must carry 7 keys (6 + concerns)")

	concerns, ok := m["concerns"].([]any)
	require.True(t, ok, "concerns must be a JSON array")
	require.Len(t, concerns, 1)
	c0, ok := concerns[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "medium", c0["severity"])
	require.Equal(t, "confidence", c0["category"])
	require.Equal(t, "low", c0["message"])
	require.Equal(t, "confidence=0.40<0.50", c0["evidence"])
}

// TestConcernPayload_WireKeys pins the ConcernPayload json keys.
func TestConcernPayload_WireKeys(t *testing.T) {
	raw, err := json.Marshal(inbound.ConcernPayload{
		Severity: "high", Category: "risk", Message: "danger", Evidence: "risks[0].level=high",
	})
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	require.ElementsMatch(t, []string{"severity", "category", "message", "evidence"}, keysOf(m))
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
