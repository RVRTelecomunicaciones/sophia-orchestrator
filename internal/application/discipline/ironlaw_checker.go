package discipline

import (
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ironlaw"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
)

// Action enumerates the orchestrator transitions the IronLawChecker may
// evaluate. Iron Law #1 (persisted-before-return) is enforced structurally
// by repository semantics and is NOT checked here.
type Action string

// Actions checked by IronLawChecker.
const (
	ActionStartPhase    Action = "start_phase"
	ActionCompletePhase Action = "complete_phase"
	ActionRunApply      Action = "run_apply"
	ActionRunArchive    Action = "run_archive"
	ActionDispatch      Action = "dispatch"
)

// PhasePredicate is a minimal projection of a Phase used by IronLawChecker:
// status + confidence. The checker doesn't need the full aggregate.
type PhasePredicate struct {
	Status     phase.PhaseStatus
	Confidence float64
}

// Context is the input to IronLawChecker.Check. PriorPhases maps phase types
// (typically spec/design/tasks/verify) to their persisted state. Empty map
// keys mean "the phase has not been run".
type Context struct {
	Action                Action
	DesiredPhase          phase.PhaseType
	PriorPhases           map[phase.PhaseType]PhasePredicate
	HasGovernanceDecision bool
	TaskAttempts          int
}

// Violation is one Iron Law violation reported by Check.
type Violation struct {
	Law         ironlaw.Law
	Description string
}

// IronLawChecker validates Iron Laws #2..#5 against an Action context.
// Iron Law #1 (persisted-before-return) is structurally enforced by the
// repository layer and is not part of this static check.
type IronLawChecker struct{}

// NewIronLawChecker constructs a stateless checker.
func NewIronLawChecker() *IronLawChecker { return &IronLawChecker{} }

// Check runs every applicable Iron Law against the context and returns the
// list of violations (empty if all laws are satisfied).
func (c *IronLawChecker) Check(ctx Context) []Violation {
	var v []Violation

	// IL2: NO APPLY WITHOUT TASKS DONE.
	if ctx.Action == ActionRunApply {
		tp, ok := ctx.PriorPhases[phase.PhaseTasks]
		threshold := phase.PhaseTasks.ConfidenceThreshold()
		if !ok || tp.Status != phase.PhaseStatusDone || tp.Confidence < threshold {
			law, _ := ironlaw.ByID(ironlaw.IronLaw2)
			v = append(v, Violation{
				Law:         law,
				Description: "tasks phase not DONE with confidence ≥ threshold",
			})
		}
	}

	// IL3: NO ARCHIVE WITHOUT VERIFY DONE.
	if ctx.Action == ActionRunArchive {
		vp, ok := ctx.PriorPhases[phase.PhaseVerify]
		threshold := phase.PhaseVerify.ConfidenceThreshold()
		if !ok || vp.Status != phase.PhaseStatusDone || vp.Confidence < threshold {
			law, _ := ironlaw.ByID(ironlaw.IronLaw3)
			v = append(v, Violation{
				Law:         law,
				Description: "verify phase not DONE with confidence ≥ threshold",
			})
		}
	}

	// IL4: NO RUNTIME CALL WITHOUT GOVERNANCE DECISION.
	if ctx.Action == ActionDispatch && !ctx.HasGovernanceDecision {
		law, _ := ironlaw.ByID(ironlaw.IronLaw4)
		v = append(v, Violation{
			Law:         law,
			Description: "no governance decision recorded for dispatch",
		})
	}

	// IL5: NO FIX #4 WITHOUT ARCHITECTURAL ESCALATION.
	// Triggered when caller proposes a 4th attempt on a task that has
	// already failed 3 times.
	if ctx.TaskAttempts >= 3 {
		law, _ := ironlaw.ByID(ironlaw.IronLaw5)
		v = append(v, Violation{
			Law:         law,
			Description: "task attempts already at 3; escalate, do not retry",
		})
	}

	return v
}
