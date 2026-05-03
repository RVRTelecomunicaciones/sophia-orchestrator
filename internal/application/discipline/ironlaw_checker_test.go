package discipline_test

import (
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ironlaw"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/stretchr/testify/require"
)

func priorAllDone() map[phase.PhaseType]discipline.PhasePredicate {
	return map[phase.PhaseType]discipline.PhasePredicate{
		phase.PhaseTasks:  {Status: phase.PhaseStatusDone, Confidence: 0.85},
		phase.PhaseVerify: {Status: phase.PhaseStatusDone, Confidence: 0.95},
	}
}

func TestIronLawChecker_NoViolations_HappyPath(t *testing.T) {
	c := discipline.NewIronLawChecker()
	v := c.Check(discipline.Context{
		Action:                ActionRunApplyForTest(),
		PriorPhases:           priorAllDone(),
		HasGovernanceDecision: true,
		TaskAttempts:          0,
	})
	require.Empty(t, v)
}

// ActionRunApplyForTest is a thin alias to expose the internal Action constants.
// Defining locally keeps the test stable if Action constants are moved.
func ActionRunApplyForTest() discipline.Action {
	return discipline.ActionRunApply
}

func TestIronLawChecker_IL2_Violated_TasksMissing(t *testing.T) {
	c := discipline.NewIronLawChecker()
	v := c.Check(discipline.Context{
		Action:      discipline.ActionRunApply,
		PriorPhases: map[phase.PhaseType]discipline.PhasePredicate{},
	})
	require.Len(t, v, 1)
	require.Equal(t, ironlaw.IronLaw2, v[0].Law.ID)
}

func TestIronLawChecker_IL2_Violated_TasksLowConfidence(t *testing.T) {
	c := discipline.NewIronLawChecker()
	v := c.Check(discipline.Context{
		Action: discipline.ActionRunApply,
		PriorPhases: map[phase.PhaseType]discipline.PhasePredicate{
			phase.PhaseTasks: {Status: phase.PhaseStatusDone, Confidence: 0.4}, // below 0.8
		},
	})
	require.Len(t, v, 1)
	require.Equal(t, ironlaw.IronLaw2, v[0].Law.ID)
}

func TestIronLawChecker_IL2_Violated_TasksWrongStatus(t *testing.T) {
	c := discipline.NewIronLawChecker()
	v := c.Check(discipline.Context{
		Action: discipline.ActionRunApply,
		PriorPhases: map[phase.PhaseType]discipline.PhasePredicate{
			phase.PhaseTasks: {Status: phase.PhaseStatusDoneWithConcerns, Confidence: 0.85},
		},
	})
	require.Len(t, v, 1)
	require.Equal(t, ironlaw.IronLaw2, v[0].Law.ID)
}

func TestIronLawChecker_IL3_Violated_VerifyMissing(t *testing.T) {
	c := discipline.NewIronLawChecker()
	v := c.Check(discipline.Context{
		Action:      discipline.ActionRunArchive,
		PriorPhases: map[phase.PhaseType]discipline.PhasePredicate{},
	})
	require.Len(t, v, 1)
	require.Equal(t, ironlaw.IronLaw3, v[0].Law.ID)
}

func TestIronLawChecker_IL3_Violated_VerifyLowConfidence(t *testing.T) {
	c := discipline.NewIronLawChecker()
	v := c.Check(discipline.Context{
		Action: discipline.ActionRunArchive,
		PriorPhases: map[phase.PhaseType]discipline.PhasePredicate{
			phase.PhaseVerify: {Status: phase.PhaseStatusDone, Confidence: 0.85}, // below 0.9
		},
	})
	require.Len(t, v, 1)
	require.Equal(t, ironlaw.IronLaw3, v[0].Law.ID)
}

func TestIronLawChecker_IL4_Violated_NoGovernance(t *testing.T) {
	c := discipline.NewIronLawChecker()
	v := c.Check(discipline.Context{
		Action:                discipline.ActionDispatch,
		HasGovernanceDecision: false,
	})
	require.Len(t, v, 1)
	require.Equal(t, ironlaw.IronLaw4, v[0].Law.ID)
}

func TestIronLawChecker_IL5_Violated_TooManyAttempts(t *testing.T) {
	c := discipline.NewIronLawChecker()
	v := c.Check(discipline.Context{
		Action:       discipline.ActionStartPhase,
		TaskAttempts: 3,
	})
	require.Len(t, v, 1)
	require.Equal(t, ironlaw.IronLaw5, v[0].Law.ID)
}

func TestIronLawChecker_IL5_TriggersAtFour(t *testing.T) {
	c := discipline.NewIronLawChecker()
	v := c.Check(discipline.Context{TaskAttempts: 4})
	require.Len(t, v, 1)
	require.Equal(t, ironlaw.IronLaw5, v[0].Law.ID)
}

func TestIronLawChecker_IL5_NotTriggeredAtTwo(t *testing.T) {
	c := discipline.NewIronLawChecker()
	v := c.Check(discipline.Context{TaskAttempts: 2})
	require.Empty(t, v)
}

func TestIronLawChecker_MultipleViolations(t *testing.T) {
	c := discipline.NewIronLawChecker()
	v := c.Check(discipline.Context{
		Action:                discipline.ActionRunArchive, // IL3 (verify missing)
		PriorPhases:           map[phase.PhaseType]discipline.PhasePredicate{},
		HasGovernanceDecision: false, // IL4 not triggered (action != dispatch)
		TaskAttempts:          5,     // IL5
	})
	require.Len(t, v, 2)
	laws := []ironlaw.ID{v[0].Law.ID, v[1].Law.ID}
	require.Contains(t, laws, ironlaw.IronLaw3)
	require.Contains(t, laws, ironlaw.IronLaw5)
}

func TestIronLawChecker_ViolationCarriesDescription(t *testing.T) {
	c := discipline.NewIronLawChecker()
	v := c.Check(discipline.Context{
		Action: discipline.ActionDispatch,
	})
	require.Len(t, v, 1)
	require.NotEmpty(t, v[0].Description)
	require.NotEmpty(t, v[0].Law.Description)
}
