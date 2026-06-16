package skill

import (
	"context"
	"errors"
	"fmt"

	domainskill "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
)

// Gate thresholds for the avg_retry_reduction proxy (D-LH-3). These mirror the
// ME consumer's promoter/demoter gates that were previously dead because
// apply_attempts was hardcoded to 0 (constant 0.333, never crossing either gate).
const (
	promoteThreshold = 0.20
	demoteThreshold  = 0.05
)

// GateVerdict is the outcome of evaluating the recomputed metric against the gates.
type GateVerdict string

// Gate verdict values.
const (
	VerdictNone    GateVerdict = "none"
	VerdictPromote GateVerdict = "promote"
	VerdictDemote  GateVerdict = "demote"
)

// Evidence is the per-skill input to re-evaluation: the current lifecycle status,
// the currently-stored avg_retry_reduction (for delta reporting), and the real
// per-change apply_attempts basis (SUM(tasks.attempts), D-LH-2).
type Evidence struct {
	SkillID       string
	CurrentStatus domainskill.Status
	CurrentMetric float64
	ApplyAttempts int
}

// EvidenceProvider yields the evidence rows to re-evaluate. The concrete
// implementation reads active skills and their per-change apply_attempts basis.
type EvidenceProvider interface {
	Rows(ctx context.Context) ([]Evidence, error)
}

// StatusPatcher applies a lifecycle status transition. It is satisfied by
// *Service.PatchStatus, so apply reuses the 6-enum allowedTransitions guard and
// ErrForbiddenStatusTransition semantics unchanged (no new mutation surface).
type StatusPatcher interface {
	PatchStatus(ctx context.Context, skillID, status, reason string) error
}

// ReevalRow is one line of the re-evaluation report.
type ReevalRow struct {
	SkillID        string
	CurrentStatus  domainskill.Status
	ProposedStatus domainskill.Status
	OldMetric      float64
	NewMetric      float64
	ApplyAttempts  int
	Verdict        GateVerdict
	WouldChange    bool
	// Applied is true when this row's transition was actually written (apply mode).
	Applied bool
	// Skipped is true when an attempted transition was rejected (e.g. forbidden).
	Skipped bool
	// ApplyErr carries the rejection cause when Skipped is true.
	ApplyErr error
}

// Reevaluator recomputes skill promotion/demotion from real apply_attempts and,
// only on explicit confirmation, applies the resulting transitions through the
// existing status-transition validation. The reversal path for any applied change
// is the existing admin PATCH /status transition — no rollback surface is added.
type Reevaluator struct {
	evidence EvidenceProvider
	patcher  StatusPatcher
}

// NewReevaluator constructs a Reevaluator.
func NewReevaluator(evidence EvidenceProvider, patcher StatusPatcher) *Reevaluator {
	return &Reevaluator{evidence: evidence, patcher: patcher}
}

// recompute returns the avg_retry_reduction proxy for the given apply_attempts,
// using the same formula as the live GetUsage basis so dry-run and live agree.
func recompute(applyAttempts int) float64 {
	return (1.5 - float64(applyAttempts)) / 1.5
}

// evaluate returns the gate verdict and proposed status for a skill given its
// recomputed metric. The verdict is actionable: it is Promote/Demote only when the
// recomputed metric crosses a gate AND the current lifecycle status has the
// matching transition target (promote: validated→active; demote: active→deprecated).
// Any skill with no resulting transition reports VerdictNone and no change, so the
// previously-dead 0.333 case on an already-active skill is correctly a no-op. The
// apply step still defers to PatchStatus for the authoritative transition guard.
func evaluate(current domainskill.Status, newMetric float64) (GateVerdict, domainskill.Status, bool) {
	switch {
	case newMetric >= promoteThreshold && current == domainskill.StatusValidated:
		return VerdictPromote, domainskill.StatusActive, true
	case newMetric < demoteThreshold && current == domainskill.StatusActive:
		return VerdictDemote, domainskill.StatusDeprecated, true
	default:
		return VerdictNone, current, false
	}
}

// plan builds the report rows without any mutation.
func (r *Reevaluator) plan(ctx context.Context) ([]ReevalRow, error) {
	rows, err := r.evidence.Rows(ctx)
	if err != nil {
		return nil, fmt.Errorf("skill.Reevaluator: evidence: %w", err)
	}

	report := make([]ReevalRow, 0, len(rows))
	for _, e := range rows {
		newMetric := recompute(e.ApplyAttempts)
		verdict, proposed, wouldChange := evaluate(e.CurrentStatus, newMetric)
		report = append(report, ReevalRow{
			SkillID:        e.SkillID,
			CurrentStatus:  e.CurrentStatus,
			ProposedStatus: proposed,
			OldMetric:      e.CurrentMetric,
			NewMetric:      newMetric,
			ApplyAttempts:  e.ApplyAttempts,
			Verdict:        verdict,
			WouldChange:    wouldChange,
		})
	}
	return report, nil
}

// DryRun recomputes every skill's metric and gate verdict and returns the report
// without mutating any status.
func (r *Reevaluator) DryRun(ctx context.Context) ([]ReevalRow, error) {
	return r.plan(ctx)
}

// Apply recomputes the report and, when confirm is true, applies each proposed
// transition through PatchStatus. With confirm=false it is identical to DryRun
// (no mutation). Forbidden transitions are reported as skipped, never forced.
func (r *Reevaluator) Apply(ctx context.Context, confirm bool) ([]ReevalRow, error) {
	report, err := r.plan(ctx)
	if err != nil {
		return nil, err
	}
	if !confirm {
		return report, nil
	}

	for i := range report {
		row := &report[i]
		if !row.WouldChange {
			continue
		}
		err := r.patcher.PatchStatus(ctx, row.SkillID, row.ProposedStatus.String(), "retroactive reevaluation")
		switch {
		case err == nil:
			row.Applied = true
		case errors.Is(err, ErrForbiddenStatusTransition):
			row.Skipped = true
			row.ApplyErr = err
		default:
			return report, fmt.Errorf("skill.Reevaluator.Apply: %s: %w", row.SkillID, err)
		}
	}
	return report, nil
}

// CountChanges returns the number of rows whose status would change.
func CountChanges(report []ReevalRow) int {
	n := 0
	for _, row := range report {
		if row.WouldChange {
			n++
		}
	}
	return n
}
