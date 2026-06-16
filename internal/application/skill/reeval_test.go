package skill_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	skillapp "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/skill"
	domainskill "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
)

// recordingPatcher records PatchStatus calls and can fail specific skills.
type recordingPatcher struct {
	calls    []patchCall
	failWith map[string]error
}

type patchCall struct {
	skillID string
	status  string
}

func newRecordingPatcher() *recordingPatcher {
	return &recordingPatcher{failWith: map[string]error{}}
}

func (p *recordingPatcher) PatchStatus(_ context.Context, skillID, status, _ string) error {
	if err := p.failWith[skillID]; err != nil {
		return err
	}
	p.calls = append(p.calls, patchCall{skillID: skillID, status: status})
	return nil
}

// staticEvidence returns a fixed evidence slice.
type staticEvidence struct {
	rows []skillapp.Evidence
}

func (s staticEvidence) Rows(_ context.Context) ([]skillapp.Evidence, error) {
	return s.rows, nil
}

func ev(skillID string, status domainskill.Status, current float64, attempts int) skillapp.Evidence {
	return skillapp.Evidence{
		SkillID:       skillID,
		CurrentStatus: status,
		CurrentMetric: current,
		ApplyAttempts: attempts,
	}
}

// H.1 RED — DryRun recompute + verdict.

// TestDryRun_RecomputesMetricAndVerdict verifies the metric recompute and gate
// verdicts from real apply_attempts (spec skill-retroactive-reevaluation
// "metric recomputed", "dry-run reports deltas").
func TestDryRun_RecomputesMetricAndVerdict(t *testing.T) {
	tests := []struct {
		name           string
		evidence       skillapp.Evidence
		wantNewMetric  float64
		wantVerdict    skillapp.GateVerdict
		wantProposed   domainskill.Status
		wantTransition bool
	}{
		{
			// attempts=3 → (1.5-3)/1.5 = -1.0 < 0.05 → demote active→deprecated.
			name:           "active demoted on high attempts",
			evidence:       ev(testSkillID1, domainskill.StatusActive, 0.333, 3),
			wantNewMetric:  -1.0,
			wantVerdict:    skillapp.VerdictDemote,
			wantProposed:   domainskill.StatusDeprecated,
			wantTransition: true,
		},
		{
			// attempts=0 → (1.5-0)/1.5 = 1.0 >= 0.20 → promote validated→active.
			name:           "validated promoted on zero attempts",
			evidence:       ev(testSkillID2, domainskill.StatusValidated, 0.333, 0),
			wantNewMetric:  1.0,
			wantVerdict:    skillapp.VerdictPromote,
			wantProposed:   domainskill.StatusActive,
			wantTransition: true,
		},
		{
			// attempts=1 → (1.5-1)/1.5 = 0.333 — neither gate; no transition.
			name:           "mid-band no transition",
			evidence:       ev(testSkillID1, domainskill.StatusActive, 0.333, 1),
			wantNewMetric:  0.3333333333333333,
			wantVerdict:    skillapp.VerdictNone,
			wantProposed:   domainskill.StatusActive,
			wantTransition: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := skillapp.NewReevaluator(
				staticEvidence{rows: []skillapp.Evidence{tt.evidence}},
				newRecordingPatcher(),
			)
			report, err := r.DryRun(context.Background())
			require.NoError(t, err)
			require.Len(t, report, 1)

			row := report[0]
			assert.InDelta(t, tt.wantNewMetric, row.NewMetric, 1e-9)
			assert.Equal(t, tt.evidence.CurrentMetric, row.OldMetric)
			assert.Equal(t, tt.evidence.ApplyAttempts, row.ApplyAttempts)
			assert.Equal(t, tt.wantVerdict, row.Verdict)
			assert.Equal(t, tt.evidence.CurrentStatus, row.CurrentStatus)
			assert.Equal(t, tt.wantProposed, row.ProposedStatus)
			assert.Equal(t, tt.wantTransition, row.WouldChange)
		})
	}
}

// TestDryRun_NoOpEmptyState reports zero projected changes when no skill crosses a
// gate or there are no skills (spec "Dry-run on no-op / empty state reports zero changes").
func TestDryRun_NoOpEmptyState(t *testing.T) {
	// No skills at all.
	r := skillapp.NewReevaluator(staticEvidence{rows: nil}, newRecordingPatcher())
	report, err := r.DryRun(context.Background())
	require.NoError(t, err)
	assert.Empty(t, report)
	assert.Equal(t, 0, skillapp.CountChanges(report))

	// A skill that does not cross any gate.
	r2 := skillapp.NewReevaluator(
		staticEvidence{rows: []skillapp.Evidence{ev(testSkillID1, domainskill.StatusActive, 0.333, 1)}},
		newRecordingPatcher(),
	)
	report2, err := r2.DryRun(context.Background())
	require.NoError(t, err)
	require.Len(t, report2, 1)
	assert.Equal(t, 0, skillapp.CountChanges(report2), "no gate crossed → zero projected changes")
}

// I.1 RED — Apply confirm-gated + transition-validated.

// TestApply_MutatesOnlyGatedSkillsOnConfirm verifies confirm-gated apply mutates
// exactly the gated skills (spec "Apply mutates only gated skills after confirmation").
func TestApply_MutatesOnlyGatedSkillsOnConfirm(t *testing.T) {
	patcher := newRecordingPatcher()
	r := skillapp.NewReevaluator(
		staticEvidence{rows: []skillapp.Evidence{
			ev(testSkillID1, domainskill.StatusActive, 0.333, 3),    // demote → deprecated
			ev(testSkillID2, domainskill.StatusActive, 0.333, 1),    // no change
		}},
		patcher,
	)

	report, err := r.Apply(context.Background(), true)
	require.NoError(t, err)
	require.Len(t, report, 2)

	require.Len(t, patcher.calls, 1, "only the gated skill is mutated")
	assert.Equal(t, testSkillID1, patcher.calls[0].skillID)
	assert.Equal(t, string(domainskill.StatusDeprecated), patcher.calls[0].status)
}

// TestApply_DefaultNeverMutates verifies confirm=false performs no mutation
// (spec "Default invocation never mutates").
func TestApply_DefaultNeverMutates(t *testing.T) {
	patcher := newRecordingPatcher()
	r := skillapp.NewReevaluator(
		staticEvidence{rows: []skillapp.Evidence{ev(testSkillID1, domainskill.StatusActive, 0.333, 3)}},
		patcher,
	)

	report, err := r.Apply(context.Background(), false)
	require.NoError(t, err)
	require.Len(t, report, 1)
	assert.Empty(t, patcher.calls, "confirm=false must mutate nothing (dry-run semantics)")
}

// TestApply_ForbiddenTransitionReportedSkipped verifies a forbidden transition is
// reported skipped, never forced, and the reversal path is PatchStatus (no rollback
// surface). Spec "apply mutates only on confirmation" + design D-LH-3.
func TestApply_ForbiddenTransitionReportedSkipped(t *testing.T) {
	patcher := newRecordingPatcher()
	patcher.failWith[testSkillID1] = skillapp.ErrForbiddenStatusTransition

	r := skillapp.NewReevaluator(
		staticEvidence{rows: []skillapp.Evidence{
			ev(testSkillID1, domainskill.StatusActive, 0.333, 3), // demote attempt, but patcher forbids
			ev(testSkillID2, domainskill.StatusActive, 0.333, 0), // 1.0 >= 0.20 but active has no promote target → no change
		}},
		patcher,
	)

	report, err := r.Apply(context.Background(), true)
	require.NoError(t, err, "a forbidden transition must not fail the whole run")

	var skipped *skillapp.ReevalRow
	for i := range report {
		if report[i].SkillID == testSkillID1 {
			skipped = &report[i]
		}
	}
	require.NotNil(t, skipped)
	assert.True(t, skipped.Skipped, "forbidden transition must be reported skipped")
	require.Error(t, skipped.ApplyErr)
	assert.True(t, errors.Is(skipped.ApplyErr, skillapp.ErrForbiddenStatusTransition))
	assert.Empty(t, patcher.calls, "forbidden transition is never forced")
}
