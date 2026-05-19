package phase_test

import (
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/stretchr/testify/require"
)

func TestPhaseStatus_AllValid(t *testing.T) {
	statuses := []phase.PhaseStatus{
		phase.PhaseStatusPending,
		phase.PhaseStatusRunning,
		phase.PhaseStatusDone,
		phase.PhaseStatusDoneWithConcerns,
		phase.PhaseStatusBlocked,
		phase.PhaseStatusNeedsContext,
		phase.PhaseStatusInterrupted,
	}
	for _, s := range statuses {
		require.True(t, s.IsValid(), "status %q should be valid", s)
	}
}

func TestPhaseStatus_RejectsUnknown(t *testing.T) {
	require.False(t, phase.PhaseStatus("nonsense").IsValid())
	require.False(t, phase.PhaseStatus("").IsValid())
}

func TestPhaseStatus_IsTerminal(t *testing.T) {
	terminal := []phase.PhaseStatus{
		phase.PhaseStatusDone,
		phase.PhaseStatusDoneWithConcerns,
		phase.PhaseStatusBlocked,
	}
	for _, s := range terminal {
		require.True(t, s.IsTerminal(), "status %q must be terminal", s)
	}

	notTerminal := []phase.PhaseStatus{
		phase.PhaseStatusPending,
		phase.PhaseStatusRunning,
		phase.PhaseStatusNeedsContext,
		phase.PhaseStatusInterrupted,
	}
	for _, s := range notTerminal {
		require.False(t, s.IsTerminal(), "status %q must NOT be terminal", s)
	}
}

// TestPhaseStatus_AdvanceAllowed locks in the 2026-05-19 design
// decision: both DONE and DONE_WITH_CONCERNS advance the change to
// the next phase. Pre-fix only DONE advanced, dead-ending any cycle
// whose phase produced even a minor concern. BLOCKED never advances
// (genuine failure). Non-terminal statuses cannot advance because the
// phase isn't actually finished yet.
func TestPhaseStatus_AdvanceAllowed(t *testing.T) {
	allowed := []phase.PhaseStatus{
		phase.PhaseStatusDone,
		phase.PhaseStatusDoneWithConcerns,
	}
	for _, s := range allowed {
		require.True(t, s.AdvanceAllowed(), "status %q must allow advance", s)
	}

	notAllowed := []phase.PhaseStatus{
		phase.PhaseStatusBlocked,        // genuine failure or guardrail
		phase.PhaseStatusNeedsContext,   // not terminal, can't advance yet
		phase.PhaseStatusPending,        // not run yet
		phase.PhaseStatusRunning,        // still running
		phase.PhaseStatusInterrupted,    // awaiting resume
	}
	for _, s := range notAllowed {
		require.False(t, s.AdvanceAllowed(), "status %q must NOT allow advance", s)
	}
}
