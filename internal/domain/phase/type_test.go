package phase_test

import (
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/stretchr/testify/require"
)

func TestAllPhaseTypes_NineCanonical(t *testing.T) {
	require.Len(t, phase.AllPhaseTypes(), 9)
}

func TestPhaseType_IsValid(t *testing.T) {
	for _, pt := range phase.AllPhaseTypes() {
		require.True(t, pt.IsValid(), "type %q should be valid", pt)
	}
	require.False(t, phase.PhaseType("nonsense").IsValid())
	require.False(t, phase.PhaseType("").IsValid())
}

func TestPhaseType_NextValid_Init(t *testing.T) {
	require.Equal(t, []phase.PhaseType{phase.PhaseExplore}, phase.PhaseInit.NextValid())
}

func TestPhaseType_NextValid_ProposalSplitsToSpecAndDesign(t *testing.T) {
	got := phase.PhaseProposal.NextValid()
	require.ElementsMatch(t, []phase.PhaseType{phase.PhaseSpec, phase.PhaseDesign}, got)
}

func TestPhaseType_NextValid_SpecAndDesignBothLeadToTasks(t *testing.T) {
	require.Equal(t, []phase.PhaseType{phase.PhaseTasks}, phase.PhaseSpec.NextValid())
	require.Equal(t, []phase.PhaseType{phase.PhaseTasks}, phase.PhaseDesign.NextValid())
}

func TestPhaseType_NextValid_ArchiveTerminal(t *testing.T) {
	require.Nil(t, phase.PhaseArchive.NextValid())
}

func TestPhaseType_ConfidenceThreshold(t *testing.T) {
	cases := map[phase.PhaseType]float64{
		phase.PhaseInit:     0.0,
		phase.PhaseExplore:  0.5,
		phase.PhaseProposal: 0.7,
		phase.PhaseSpec:     0.8,
		phase.PhaseDesign:   0.7,
		phase.PhaseTasks:    0.8,
		phase.PhaseApply:    0.7,
		phase.PhaseVerify:   0.9,
		phase.PhaseArchive:  0.9,
	}
	for pt, want := range cases {
		require.InDelta(t, want, pt.ConfidenceThreshold(), 0.0001, "phase=%s", pt)
	}
}
