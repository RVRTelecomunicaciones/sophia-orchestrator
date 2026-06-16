package phase_test

import (
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/stretchr/testify/require"
)

// TestConcern_ValueShape pins the advisory Concern value type. It is a pure
// value object (no behavior) carrying the four advisory fields the critic
// reports per design D-GA-2.
func TestConcern_ValueShape(t *testing.T) {
	c := phase.Concern{
		Severity: "high",
		Category: "risk",
		Message:  "envelope reports a high-level risk",
		Evidence: "risks[0].level=high",
	}
	require.Equal(t, "high", c.Severity)
	require.Equal(t, "risk", c.Category)
	require.Equal(t, "envelope reports a high-level risk", c.Message)
	require.Equal(t, "risks[0].level=high", c.Evidence)
}

// TestPhase_SetConcerns_RecordsAdvisoryConcerns verifies the setter stores
// the concerns and exposes them via Concerns(). Concerns are advisory only —
// SetConcerns does NOT mutate phase status (status is derived by Complete from
// the envelope, design D-GA-5).
func TestPhase_SetConcerns_RecordsAdvisoryConcerns(t *testing.T) {
	p, err := phase.New(mkPhaseID(t), mkChangeID(t), phase.PhaseSpec, 3)
	require.NoError(t, err)

	concerns := []phase.Concern{
		{Severity: "medium", Category: "confidence", Message: "low confidence", Evidence: "confidence=0.4"},
	}
	p.SetConcerns(concerns)

	require.Equal(t, concerns, p.Concerns())
}

// TestPhase_Concerns_EmptyByDefault verifies a freshly constructed phase has
// no concerns — the opted-out / clean path stays byte-identical to today.
func TestPhase_Concerns_EmptyByDefault(t *testing.T) {
	p, err := phase.New(mkPhaseID(t), mkChangeID(t), phase.PhaseSpec, 3)
	require.NoError(t, err)
	require.Empty(t, p.Concerns())
}

// TestPhase_SetConcerns_DoesNotChangeStatus locks in that SetConcerns is
// orthogonal to lifecycle status. The done_with_concerns status is derived by
// Complete from a DONE_WITH_CONCERNS envelope via the existing switch
// (phase.go:133), NOT by attaching concerns.
func TestPhase_SetConcerns_DoesNotChangeStatus(t *testing.T) {
	p, err := phase.New(mkPhaseID(t), mkChangeID(t), phase.PhaseSpec, 3)
	require.NoError(t, err)
	require.NoError(t, p.Start(ts()))

	env := mkEnv(envelope.StatusDoneWithConcerns, 0.85, phase.PhaseSpec)
	require.NoError(t, p.Complete(env, ts().Add(time.Minute)))
	require.Equal(t, phase.PhaseStatusDoneWithConcerns, p.Status())

	p.SetConcerns([]phase.Concern{{Severity: "low", Category: "style"}})
	// Status unchanged by attaching concerns.
	require.Equal(t, phase.PhaseStatusDoneWithConcerns, p.Status())
	require.Len(t, p.Concerns(), 1)
}
