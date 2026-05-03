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
