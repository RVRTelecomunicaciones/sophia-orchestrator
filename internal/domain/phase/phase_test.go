package phase_test

import (
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/stretchr/testify/require"
)

func mkPhaseID(t *testing.T) ids.PhaseID {
	t.Helper()
	id, err := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	require.NoError(t, err)
	return id
}

func mkChangeID(t *testing.T) ids.ChangeID {
	t.Helper()
	id, err := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5FAW")
	require.NoError(t, err)
	return id
}

func ts() time.Time {
	return time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
}

func mkEnv(status envelope.Status, conf float64, phaseType phase.PhaseType) *envelope.Envelope {
	return &envelope.Envelope{
		SchemaVersion: envelope.SchemaVersionV1,
		Phase:         string(phaseType),
		ChangeName:    "x",
		Project:       "y",
		Status:        status,
		Confidence:    conf,
	}
}

func TestNew_RejectsInvalidType(t *testing.T) {
	_, err := phase.New(mkPhaseID(t), mkChangeID(t), phase.PhaseType("nope"), 3)
	require.ErrorIs(t, err, phase.ErrInvalidType)
}

func TestNew_DefaultsRetryBudget(t *testing.T) {
	p, err := phase.New(mkPhaseID(t), mkChangeID(t), phase.PhaseSpec, 0)
	require.NoError(t, err)
	require.Equal(t, 1, p.RetryBudget())
	require.Equal(t, phase.PhaseStatusPending, p.Status())
	require.Equal(t, 0, p.Attempts())
}

func TestStart_TransitionsToRunning(t *testing.T) {
	p, _ := phase.New(mkPhaseID(t), mkChangeID(t), phase.PhaseSpec, 3)
	require.NoError(t, p.Start(ts()))
	require.Equal(t, phase.PhaseStatusRunning, p.Status())
	require.Equal(t, 1, p.Attempts())
	require.NotNil(t, p.StartedAt())
}

func TestStart_BudgetExhausted(t *testing.T) {
	p, _ := phase.New(mkPhaseID(t), mkChangeID(t), phase.PhaseSpec, 1)
	require.NoError(t, p.Start(ts()))
	require.ErrorIs(t, p.Start(ts()), phase.ErrBudgetExhausted)
}

func TestStart_RejectsTerminal(t *testing.T) {
	p, _ := phase.New(mkPhaseID(t), mkChangeID(t), phase.PhaseSpec, 3)
	require.NoError(t, p.Start(ts()))
	require.NoError(t, p.Complete(mkEnv(envelope.StatusDone, 0.85, phase.PhaseSpec), ts()))
	require.ErrorIs(t, p.Start(ts()), phase.ErrTerminal)
}

func TestComplete_DoneRequiresThresholdConfidence(t *testing.T) {
	p, _ := phase.New(mkPhaseID(t), mkChangeID(t), phase.PhaseSpec, 3) // threshold 0.8
	require.NoError(t, p.Start(ts()))
	err := p.Complete(mkEnv(envelope.StatusDone, 0.5, phase.PhaseSpec), ts())
	require.ErrorIs(t, err, phase.ErrBelowThreshold)
}

func TestComplete_DoneWithConcernsAcceptsLowConfidence(t *testing.T) {
	p, _ := phase.New(mkPhaseID(t), mkChangeID(t), phase.PhaseSpec, 3)
	require.NoError(t, p.Start(ts()))
	require.NoError(t, p.Complete(mkEnv(envelope.StatusDoneWithConcerns, 0.4, phase.PhaseSpec), ts()))
	require.Equal(t, phase.PhaseStatusDoneWithConcerns, p.Status())
}

func TestComplete_BlockedAcceptsLowConfidence(t *testing.T) {
	p, _ := phase.New(mkPhaseID(t), mkChangeID(t), phase.PhaseSpec, 3)
	require.NoError(t, p.Start(ts()))
	require.NoError(t, p.Complete(mkEnv(envelope.StatusBlocked, 0.0, phase.PhaseSpec), ts()))
	require.Equal(t, phase.PhaseStatusBlocked, p.Status())
}

func TestComplete_NeedsContextAcceptsLowConfidence(t *testing.T) {
	p, _ := phase.New(mkPhaseID(t), mkChangeID(t), phase.PhaseSpec, 3)
	require.NoError(t, p.Start(ts()))
	require.NoError(t, p.Complete(mkEnv(envelope.StatusNeedsContext, 0.3, phase.PhaseSpec), ts()))
	require.Equal(t, phase.PhaseStatusNeedsContext, p.Status())
}

func TestComplete_RejectsPhaseMismatch(t *testing.T) {
	p, _ := phase.New(mkPhaseID(t), mkChangeID(t), phase.PhaseSpec, 3)
	require.NoError(t, p.Start(ts()))
	err := p.Complete(mkEnv(envelope.StatusDone, 0.85, phase.PhaseDesign), ts())
	require.ErrorIs(t, err, phase.ErrEnvelopeMismatch)
}

func TestComplete_RejectsNilEnvelope(t *testing.T) {
	p, _ := phase.New(mkPhaseID(t), mkChangeID(t), phase.PhaseSpec, 3)
	require.NoError(t, p.Start(ts()))
	err := p.Complete(nil, ts())
	require.ErrorIs(t, err, phase.ErrEnvelopeNil)
}

func TestComplete_RejectsNotRunning(t *testing.T) {
	p, _ := phase.New(mkPhaseID(t), mkChangeID(t), phase.PhaseSpec, 3)
	err := p.Complete(mkEnv(envelope.StatusDone, 0.85, phase.PhaseSpec), ts())
	require.ErrorIs(t, err, phase.ErrNotRunning)
}

func TestMarkInterrupted_FromRunning(t *testing.T) {
	p, _ := phase.New(mkPhaseID(t), mkChangeID(t), phase.PhaseSpec, 3)
	require.NoError(t, p.Start(ts()))
	require.NoError(t, p.MarkInterrupted())
	require.Equal(t, phase.PhaseStatusInterrupted, p.Status())
}

func TestMarkInterrupted_RejectsTerminal(t *testing.T) {
	p, _ := phase.New(mkPhaseID(t), mkChangeID(t), phase.PhaseSpec, 3)
	require.NoError(t, p.Start(ts()))
	require.NoError(t, p.Complete(mkEnv(envelope.StatusDone, 0.85, phase.PhaseSpec), ts()))
	require.ErrorIs(t, p.MarkInterrupted(), phase.ErrTerminal)
}
