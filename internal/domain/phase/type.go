// Package phase contains the Phase aggregate, PhaseType enum, PhaseStatus
// enum, and state machine. PhaseType is a closed enum of the 9 canonical
// SDD phases. NextValid encodes the canonical lifecycle. ConfidenceThreshold
// returns the gating threshold per phase per spec § 1.4.
package phase

// PhaseType is the closed set of SDD phase types. See spec § 1.1.
type PhaseType string

// The 9 canonical SDD phases.
const (
	PhaseInit     PhaseType = "init"
	PhaseExplore  PhaseType = "explore"
	PhaseProposal PhaseType = "proposal"
	PhaseSpec     PhaseType = "spec"
	PhaseDesign   PhaseType = "design"
	PhaseTasks    PhaseType = "tasks"
	PhaseApply    PhaseType = "apply"
	PhaseVerify   PhaseType = "verify"
	PhaseArchive  PhaseType = "archive"
)

// AllPhaseTypes returns every valid PhaseType in canonical order.
func AllPhaseTypes() []PhaseType {
	return []PhaseType{
		PhaseInit, PhaseExplore, PhaseProposal, PhaseSpec,
		PhaseDesign, PhaseTasks, PhaseApply, PhaseVerify, PhaseArchive,
	}
}

// IsValid reports whether p is a known PhaseType.
func (p PhaseType) IsValid() bool {
	for _, v := range AllPhaseTypes() {
		if v == p {
			return true
		}
	}
	return false
}

// NextValid returns the set of phase types that may follow p in the canonical
// SDD lifecycle. For terminal phases (archive) it returns nil. Note that spec
// and design are concurrent: from proposal both are valid next phases.
func (p PhaseType) NextValid() []PhaseType {
	switch p {
	case PhaseInit:
		return []PhaseType{PhaseExplore}
	case PhaseExplore:
		return []PhaseType{PhaseProposal}
	case PhaseProposal:
		return []PhaseType{PhaseSpec, PhaseDesign}
	case PhaseSpec, PhaseDesign:
		return []PhaseType{PhaseTasks}
	case PhaseTasks:
		return []PhaseType{PhaseApply}
	case PhaseApply:
		return []PhaseType{PhaseVerify}
	case PhaseVerify:
		return []PhaseType{PhaseArchive}
	case PhaseArchive:
		return nil
	default:
		return nil
	}
}

// ConfidenceThreshold returns the minimum confidence value for which a phase
// may transition to DONE. Below threshold the orchestrator forces status to
// DONE_WITH_CONCERNS or BLOCKED. See spec § 1.4.
func (p PhaseType) ConfidenceThreshold() float64 {
	switch p {
	case PhaseExplore:
		return 0.5
	case PhaseProposal, PhaseDesign, PhaseApply:
		return 0.7
	case PhaseSpec, PhaseTasks:
		return 0.8
	case PhaseVerify, PhaseArchive:
		return 0.9
	default:
		// init carries no agent envelope; threshold 0 means transition is unconditional.
		return 0.0
	}
}
