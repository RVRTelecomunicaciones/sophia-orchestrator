package skill

import (
	"context"
	"errors"
	"fmt"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	domainskill "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// ErrNoLegalRevertPath is returned for a skill whose recorded transition cannot
// be reversed because no legal transition chain reaches the prior status (e.g.
// the skill is now archived, a terminal state). Such skills are skipped and
// reported so an operator can intervene manually; the guard is never bypassed.
var ErrNoLegalRevertPath = errors.New("skill: no legal revert path")

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
// existing status-transition validation.
//
// Reversibility (D1, loop-hardening follow-up): when an apply mutates statuses
// it records an immutable prior-state snapshot (a reeval-run audit record) via
// the audit repository. Revert reads that snapshot and replays the INVERSE
// transitions through the SAME PatchStatus guard, walking the legal transition
// chain when a direct single-step inverse is forbidden. The revert is itself
// recorded as a new audit run. No raw status write ever bypasses the guard.
//
// The audit repository, clock, and ID generator are optional: NewReevaluator
// leaves them nil for the dry-run-only path (and the existing call sites), while
// NewReevaluatorWithAudit wires them for the audited apply/revert path.
type Reevaluator struct {
	evidence EvidenceProvider
	patcher  StatusPatcher
	audit    outbound.ReevalAuditRepository
	clock    shared.Clock
	idgen    shared.IDGenerator
}

// NewReevaluator constructs a Reevaluator without audit persistence. Apply still
// applies transitions, but it does not record a revertible snapshot. Used by the
// dry-run path and unit tests that do not exercise revert.
func NewReevaluator(evidence EvidenceProvider, patcher StatusPatcher) *Reevaluator {
	return &Reevaluator{evidence: evidence, patcher: patcher}
}

// NewReevaluatorWithAudit constructs a Reevaluator with audit persistence so
// Apply records a revertible snapshot and Revert/RevertLast become available.
func NewReevaluatorWithAudit(
	evidence EvidenceProvider,
	patcher StatusPatcher,
	audit outbound.ReevalAuditRepository,
	clock shared.Clock,
	idgen shared.IDGenerator,
) *Reevaluator {
	return &Reevaluator{
		evidence: evidence,
		patcher:  patcher,
		audit:    audit,
		clock:    clock,
		idgen:    idgen,
	}
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

	// Mint the run ID before any item ID so the parent run id is allocated
	// first (stable, deterministic ordering under FixedIDGenerator).
	var runID string
	if r.audit != nil {
		runID = r.idgen.NewID()
	}

	items := make([]outbound.ReevalRunItem, 0)
	for i := range report {
		row := &report[i]
		if !row.WouldChange {
			continue
		}
		err := r.patcher.PatchStatus(ctx, row.SkillID, row.ProposedStatus.String(), "retroactive reevaluation")
		switch {
		case err == nil:
			row.Applied = true
			if r.audit != nil {
				items = append(items, outbound.ReevalRunItem{
					ID:          r.idgen.NewID(),
					SkillID:     row.SkillID,
					PriorStatus: row.CurrentStatus.String(),
					NewStatus:   row.ProposedStatus.String(),
				})
			}
		case errors.Is(err, ErrForbiddenStatusTransition):
			row.Skipped = true
			row.ApplyErr = err
		default:
			return report, fmt.Errorf("skill.Reevaluator.Apply: %s: %w", row.SkillID, err)
		}
	}

	// Persist the immutable prior-state snapshot only when at least one
	// transition was actually applied and audit is wired (D1).
	if r.audit != nil && len(items) > 0 {
		run := outbound.ReevalRun{
			ID:        runID,
			Mode:      "apply",
			CreatedAt: r.clock.Now(),
			Items:     items,
		}
		if err := r.audit.Save(ctx, run); err != nil {
			return report, fmt.Errorf("skill.Reevaluator.Apply: audit save: %w", err)
		}
	}
	return report, nil
}

// RevertRow reports the outcome of attempting to reverse one recorded transition.
type RevertRow struct {
	SkillID    string
	FromStatus domainskill.Status // the status the skill is reverted FROM (the recorded new_status)
	ToStatus   domainskill.Status // the prior status we attempt to restore
	Path       []domainskill.Status
	Reverted   bool
	Skipped    bool
	RevertErr  error
}

// RevertLast reverses the most recent recorded reeval run.
func (r *Reevaluator) RevertLast(ctx context.Context) ([]RevertRow, error) {
	run, err := r.audit.FindLatest(ctx)
	if err != nil {
		return nil, fmt.Errorf("skill.Reevaluator.RevertLast: %w", err)
	}
	return r.revertRun(ctx, run)
}

// Revert reverses the transitions recorded in the given run id by replaying the
// inverse transitions (new_status → prior_status) through PatchStatus. Where the
// direct inverse is forbidden by the guard, it walks the shortest legal chain;
// where no legal path exists, the skill is skipped and reported. The revert is
// recorded as a new audit run with mode "revert".
func (r *Reevaluator) Revert(ctx context.Context, runID string) ([]RevertRow, error) {
	run, err := r.audit.FindByID(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("skill.Reevaluator.Revert: %w", err)
	}
	return r.revertRun(ctx, run)
}

// revertRun executes the inverse of every item in run and records the revert.
func (r *Reevaluator) revertRun(ctx context.Context, run outbound.ReevalRun) ([]RevertRow, error) {
	result := make([]RevertRow, 0, len(run.Items))
	revertItems := make([]outbound.ReevalRunItem, 0, len(run.Items))

	// Mint the revert run ID before any item ID (parent-first allocation).
	revRunID := r.idgen.NewID()

	for _, item := range run.Items {
		from := domainskill.Status(item.NewStatus) // where the skill currently sits
		to := domainskill.Status(item.PriorStatus) // where we want it back
		path, ok := transitionPath(from, to)
		row := RevertRow{SkillID: item.SkillID, FromStatus: from, ToStatus: to, Path: path}

		if !ok {
			row.Skipped = true
			row.RevertErr = fmt.Errorf(
				"%w: no legal transition path %s→%s; reverse manually",
				ErrNoLegalRevertPath, from, to)
			result = append(result, row)
			continue
		}

		if err := r.walk(ctx, item.SkillID, path); err != nil {
			row.Skipped = true
			row.RevertErr = err
			result = append(result, row)
			continue
		}

		row.Reverted = true
		result = append(result, row)
		revertItems = append(revertItems, outbound.ReevalRunItem{
			ID:          r.idgen.NewID(),
			SkillID:     item.SkillID,
			PriorStatus: item.NewStatus, // inverse: prior of the revert IS the recorded new_status
			NewStatus:   item.PriorStatus,
		})
	}

	// Record the revert as its own immutable audit run.
	revRun := outbound.ReevalRun{
		ID:           revRunID,
		Mode:         "revert",
		RevertsRunID: run.ID,
		CreatedAt:    r.clock.Now(),
		Items:        revertItems,
	}
	if err := r.audit.Save(ctx, revRun); err != nil {
		return result, fmt.Errorf("skill.Reevaluator.revertRun: audit save: %w", err)
	}
	return result, nil
}

// walk applies each step of a legal transition chain through PatchStatus, so the
// 6-enum guard validates every hop and no raw status write ever bypasses it.
func (r *Reevaluator) walk(ctx context.Context, skillID string, path []domainskill.Status) error {
	for _, step := range path {
		if err := r.patcher.PatchStatus(ctx, skillID, step.String(), "reeval revert"); err != nil {
			return fmt.Errorf("skill.Reevaluator.walk: %s→%s: %w", skillID, step, err)
		}
	}
	return nil
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
