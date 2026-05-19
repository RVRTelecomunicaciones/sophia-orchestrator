package phase

// PhaseStatus is the lifecycle state of a Phase. The set is closed.
type PhaseStatus string

// Phase lifecycle states.
const (
	PhaseStatusPending          PhaseStatus = "pending"
	PhaseStatusRunning          PhaseStatus = "running"
	PhaseStatusDone             PhaseStatus = "done"
	PhaseStatusDoneWithConcerns PhaseStatus = "done_with_concerns"
	PhaseStatusBlocked          PhaseStatus = "blocked"
	PhaseStatusNeedsContext     PhaseStatus = "needs_context"
	PhaseStatusInterrupted      PhaseStatus = "interrupted"
)

// IsValid reports whether s is a known PhaseStatus.
func (s PhaseStatus) IsValid() bool {
	switch s {
	case PhaseStatusPending, PhaseStatusRunning, PhaseStatusDone,
		PhaseStatusDoneWithConcerns, PhaseStatusBlocked,
		PhaseStatusNeedsContext, PhaseStatusInterrupted:
		return true
	}
	return false
}

// IsTerminal reports whether the status is a terminal state (no further
// transitions allowed). DONE, DONE_WITH_CONCERNS, and BLOCKED are terminal.
// NEEDS_CONTEXT is NOT terminal — the orchestrator may retry within budget.
// INTERRUPTED is NOT terminal — manual resume reactivates it.
func (s PhaseStatus) IsTerminal() bool {
	return s == PhaseStatusDone ||
		s == PhaseStatusDoneWithConcerns ||
		s == PhaseStatusBlocked
}

// AdvanceAllowed reports whether finishing a phase in this status should
// trigger Change.CurrentPhase advancement to the next phase. Both DONE
// and DONE_WITH_CONCERNS qualify — concerns are informational signals the
// LLM raised about its own output, not policy blockers. The operator
// reviews concerns post-hoc by reading the persisted envelope; cycle
// progression continues. BLOCKED never advances (genuine failure or
// guardrail). Pre-2026-05-19 only DONE advanced, which dead-ended any
// cycle whose phase produced even a minor concern.
func (s PhaseStatus) AdvanceAllowed() bool {
	return s == PhaseStatusDone || s == PhaseStatusDoneWithConcerns
}
