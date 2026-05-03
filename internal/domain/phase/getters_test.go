package phase_test

import (
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/stretchr/testify/require"
)

func TestPhaseGetters_AllExposed(t *testing.T) {
	pid := mkPhaseID(t)
	cid := mkChangeID(t)
	p, err := phase.New(pid, cid, phase.PhaseSpec, 3)
	require.NoError(t, err)

	require.Equal(t, pid, p.ID())
	require.Equal(t, cid, p.ChangeID())
	require.Equal(t, phase.PhaseSpec, p.Type())
	require.Nil(t, p.Envelope())
	require.InDelta(t, 0.0, p.Confidence(), 0.0001)
	require.Equal(t, 3, p.RetryBudget())
	require.Equal(t, 0, p.Attempts())
	require.Nil(t, p.StartedAt())
	require.Nil(t, p.CompletedAt())
}

func TestPhase_HydrateRoundtrip(t *testing.T) {
	pid := mkPhaseID(t)
	cid := mkChangeID(t)
	now := ts()
	end := now.Add(2 * time.Minute)
	env := &envelope.Envelope{
		SchemaVersion: envelope.SchemaVersionV1,
		Phase:         "spec",
		ChangeName:    "x",
		Project:       "y",
		Status:        envelope.StatusDone,
		Confidence:    0.85,
	}
	p := phase.Hydrate(pid, cid, phase.PhaseSpec, phase.PhaseStatusDone, env, 0.85, 3, 1, &now, &end)
	require.Equal(t, pid, p.ID())
	require.Equal(t, cid, p.ChangeID())
	require.Equal(t, phase.PhaseSpec, p.Type())
	require.Equal(t, phase.PhaseStatusDone, p.Status())
	require.Equal(t, env, p.Envelope())
	require.InDelta(t, 0.85, p.Confidence(), 0.0001)
	require.Equal(t, 3, p.RetryBudget())
	require.Equal(t, 1, p.Attempts())
	require.Equal(t, &now, p.StartedAt())
	require.Equal(t, &end, p.CompletedAt())
}

func TestPhase_GettersAfterCompletion(t *testing.T) {
	p, _ := phase.New(mkPhaseID(t), mkChangeID(t), phase.PhaseSpec, 3)
	require.NoError(t, p.Start(ts()))
	end := ts().Add(time.Minute)
	require.NoError(t, p.Complete(mkEnv(envelope.StatusDone, 0.85, phase.PhaseSpec), end))
	require.NotNil(t, p.Envelope())
	require.NotNil(t, p.CompletedAt())
	require.Equal(t, end, *p.CompletedAt())
}

func TestPhaseType_NextValid_AllPhases(t *testing.T) {
	cases := map[phase.PhaseType][]phase.PhaseType{
		phase.PhaseInit:     {phase.PhaseExplore},
		phase.PhaseExplore:  {phase.PhaseProposal},
		phase.PhaseProposal: {phase.PhaseSpec, phase.PhaseDesign},
		phase.PhaseSpec:     {phase.PhaseTasks},
		phase.PhaseDesign:   {phase.PhaseTasks},
		phase.PhaseTasks:    {phase.PhaseApply},
		phase.PhaseApply:    {phase.PhaseVerify},
		phase.PhaseVerify:   {phase.PhaseArchive},
		phase.PhaseArchive:  nil,
	}
	for pt, want := range cases {
		got := pt.NextValid()
		require.ElementsMatch(t, want, got, "phase=%s", pt)
	}
}

func TestPhaseType_NextValid_UnknownReturnsNil(t *testing.T) {
	require.Nil(t, phase.PhaseType("nope").NextValid())
}

// Use ids.ParseChangeID indirectly to ensure the test build references it.
var _ = ids.ParseChangeID
