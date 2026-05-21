package phase

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/stretchr/testify/require"
)

// Spec #51 — when apply finishes BLOCKED the SSE payload must carry
// the same enriched fields the single-agent failure path carries
// (failure_reason + failure_detail extracted from the envelope), not
// the slim PhaseCompletedFromApplyPayload that only has
// envelope_status + envelope_confidence.

func TestBuildApplyFailedPayload_PopulatesFromEnvelope(t *testing.T) {
	pid, _ := ids.ParsePhaseID("01KS3DA2FX2W2EYW8T3VAPD8KB")
	ended := time.Date(2026, 5, 20, 14, 15, 16, 0, time.UTC)
	env := &envelope.Envelope{
		Status:           envelope.StatusBlocked,
		Confidence:       0,
		ExecutiveSummary: "Apply did not proceed because tasks board has 0 groups.",
	}

	got := buildApplyFailedPayload(pid, phase.PhaseApply, ended, env)

	require.Equal(t, pid.String(), got.PhaseID)
	require.Equal(t, string(phase.PhaseApply), got.PhaseType)
	require.Equal(t, ended, got.EndedAt)
	require.Equal(t, env.ExecutiveSummary, got.Error,
		"Error must mirror executive_summary so SSE clients without a parser still see the reason")
	require.Equal(t, "apply.blocked", got.FailureReason,
		"FailureReason should classify the blocker source so operators can filter")
	require.Equal(t, env.ExecutiveSummary, got.FailureDetail)
}

func TestBuildApplyFailedPayload_BlockingReasonsAppendedToDetail(t *testing.T) {
	pid, _ := ids.ParsePhaseID("01KS3DA2FX2W2EYW8T3VAPD8KB")
	ended := time.Date(2026, 5, 20, 14, 15, 16, 0, time.UTC)
	env := &envelope.Envelope{
		Status:           envelope.StatusBlocked,
		ExecutiveSummary: "Apply blocked.",
		Data: json.RawMessage(`{"blocking_reasons":["No local proposal-phase DONE evidence found.","Provided spec context is BLOCKED."]}`),
	}

	got := buildApplyFailedPayload(pid, phase.PhaseApply, ended, env)

	require.Contains(t, got.FailureDetail, "Apply blocked.",
		"summary must lead the detail")
	require.Contains(t, got.FailureDetail, "No local proposal-phase DONE evidence found.",
		"blocking_reasons must be appended so operators see WHY without DB access")
	require.Contains(t, got.FailureDetail, "Provided spec context is BLOCKED.")
}

func TestBuildApplyFailedPayload_NilEnvelope_EmitsUnknownReason(t *testing.T) {
	pid, _ := ids.ParsePhaseID("01KS3DA2FX2W2EYW8T3VAPD8KB")
	ended := time.Date(2026, 5, 20, 14, 15, 16, 0, time.UTC)

	got := buildApplyFailedPayload(pid, phase.PhaseApply, ended, nil)

	require.Equal(t, "unknown", got.FailureReason)
	require.NotEmpty(t, got.Error)
}

