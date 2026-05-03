package outbound

import (
	"context"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
)

// GovernanceDecisionType is the closed set of decisions returned by
// agent-governance-core for a phase transition or sensitive runtime call.
type GovernanceDecisionType string

// Decision values per agent-governance-core spec.
const (
	DecisionAllow                GovernanceDecisionType = "allow"
	DecisionAllowWithConstraints GovernanceDecisionType = "allow_with_constraints"
	DecisionRequireApproval      GovernanceDecisionType = "require_approval"
	DecisionDeny                 GovernanceDecisionType = "deny"
)

// GovernanceDecision is the result of governance evaluation for a phase or
// sensitive runtime capability invocation. Iron Law #4 requires the
// orchestrator to obtain one before any state-changing call.
type GovernanceDecision struct {
	Decision    GovernanceDecisionType
	AgentRole   string
	Strategy    string // direct | decompose | collaborate | escalate
	Reason      string
	Approval    *ApprovalGate
	Constraints map[string]any
}

// ApprovalGate carries approval-flow metadata when Decision == require_approval.
type ApprovalGate struct {
	URL string
}

// EvaluatePhaseInput names the inputs governance needs to decide a phase.
type EvaluatePhaseInput struct {
	ChangeID        ids.ChangeID
	PhaseType       phase.PhaseType
	TaskDescription string
	Sensitive       bool
}

// GovernanceClient is the orchestrator's outbound view of agent-governance-core.
type GovernanceClient interface {
	// EvaluatePhase calls governance to classify, route, and evaluate policy
	// for a phase transition. Always called before any phase Start/Run.
	EvaluatePhase(ctx context.Context, in EvaluatePhaseInput) (*GovernanceDecision, error)

	// AwaitApproval polls (or subscribes) until governance reports the
	// approval has been granted or denied for (changeID, phaseID).
	AwaitApproval(ctx context.Context, changeID ids.ChangeID, phaseID ids.PhaseID) error

	// EvaluateSensitiveAction is called before any sensitive runtime call
	// (e.g. push, deploy) to satisfy Iron Law #4.
	EvaluateSensitiveAction(ctx context.Context, changeID ids.ChangeID, capability string, payload []byte) (*GovernanceDecision, error)
}
