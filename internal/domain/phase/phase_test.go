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

// --- Restart (BUG-28) ---

// TestRestart_FromBlocked transitions a blocked phase back to running and
// bumps attempts. Used by the apply-phase retry path so operators can
// replay a partially-failed apply without losing the successful groups'
// worktrees (the application layer reuses the existing board for the
// same phase_id).
func TestRestart_FromBlocked(t *testing.T) {
	p, _ := phase.New(mkPhaseID(t), mkChangeID(t), phase.PhaseApply, 3)
	require.NoError(t, p.Start(ts()))
	require.NoError(t, p.Complete(mkEnv(envelope.StatusBlocked, 0.0, phase.PhaseApply), ts()))
	require.Equal(t, phase.PhaseStatusBlocked, p.Status())
	require.Equal(t, 1, p.Attempts())

	require.NoError(t, p.Restart(ts()))
	require.Equal(t, phase.PhaseStatusRunning, p.Status())
	require.Equal(t, 2, p.Attempts(), "Restart consumes the retry budget like Start does")
	require.Nil(t, p.CompletedAt(), "Restart MUST clear completedAt so the next Complete writes a fresh terminal time")
	require.Nil(t, p.Envelope(), "Restart MUST clear the prior BLOCKED envelope so the new Execute writes its own outcome cleanly")
}

func TestRestart_RejectsDone(t *testing.T) {
	p, _ := phase.New(mkPhaseID(t), mkChangeID(t), phase.PhaseApply, 3)
	require.NoError(t, p.Start(ts()))
	require.NoError(t, p.Complete(mkEnv(envelope.StatusDone, 0.85, phase.PhaseApply), ts()))
	require.ErrorIs(t, p.Restart(ts()), phase.ErrTerminal,
		"done phases are NOT retryable — Restart is BUG-28-blocked-specific")
}

func TestRestart_RejectsNonTerminal(t *testing.T) {
	p, _ := phase.New(mkPhaseID(t), mkChangeID(t), phase.PhaseApply, 3)
	require.NoError(t, p.Start(ts()))
	require.ErrorIs(t, p.Restart(ts()), phase.ErrNotBlocked,
		"running phases must go through Resume, not Restart")
}

func TestRestart_BudgetExhausted(t *testing.T) {
	p, _ := phase.New(mkPhaseID(t), mkChangeID(t), phase.PhaseApply, 1)
	require.NoError(t, p.Start(ts()))
	require.NoError(t, p.Complete(mkEnv(envelope.StatusBlocked, 0.0, phase.PhaseApply), ts()))
	require.ErrorIs(t, p.Restart(ts()), phase.ErrBudgetExhausted)
}
