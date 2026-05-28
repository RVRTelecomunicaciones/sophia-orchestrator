package inbound

import "time"

// Typed payload structs for every SSE event the orchestrator emits.
//
// Why typed: prior to this file each publishEvent call constructed an
// inline `map[string]any{...}` with raw string keys. A typo at any emit
// site (`task_ID` vs `task_id`, `attempts` vs `attempt`) compiled fine —
// drift surfaced only as a missing field in a dashboard or a confused
// operator. The audit (rojo #2) flagged this as the second source of
// silent typo risk after the event-name literals (rojo #1, closed in
// PR #10).
//
// Wire format: every struct here carries `json:"snake_case"` tags so
// the JSON output is byte-identical to the previous map. Consumers
// (sophia-cli ssestream/client.go) continue to receive the same shape
// and need no change — type safety is producer-side only.
//
// Naming convention: `<EventName>Payload`. The matching Event* constant
// in event_types.go uses the same root (e.g. EventApplyTaskClaimed
// pairs with ApplyTaskClaimedPayload). Adding a new event requires
// adding both.
//
// Cross-repo mirror: sophia-cli/pkg/contract/ may eventually adopt
// matching DTOs for parse-side type safety. Tracked as a follow-up
// after this PR proves the orch-side discipline.

// --- apply.* family --------------------------------------------------

// ApplyBoardCreatedPayload is the payload of apply.board.created.
type ApplyBoardCreatedPayload struct {
	BoardID string `json:"board_id"`
	Groups  int    `json:"groups"`
}

// ApplyBoardSaveFailedPayload is the payload of apply.board.save_failed.
type ApplyBoardSaveFailedPayload struct {
	Err string `json:"err"`
}

// ApplyWorktreeErrorPayload is the payload of apply.worktree.error.
type ApplyWorktreeErrorPayload struct {
	GroupID string `json:"group_id"`
	Err     string `json:"err"`
}

// ApplyGroupCompletedPayload is the payload of apply.group.completed.
type ApplyGroupCompletedPayload struct {
	GroupID   string `json:"group_id"`
	TasksDone int    `json:"tasks_done"`
}

// ApplyGroupFailedPayload is the payload of apply.group.failed.
type ApplyGroupFailedPayload struct {
	GroupID string `json:"group_id"`
	Reason  string `json:"reason"`
}

// ApplyGroupDegradedPayload is the payload of apply.group.degraded — a new
// signal introduced by BUG-30. Emitted when a group's upstream dependency
// failed but the group is being executed ANYWAY (cascade soften) so the
// rest of the apply phase still makes progress. The implement agents in
// this group know their priorContext is incomplete; the operator inspects
// the dependency failure reason via FailedDep alongside the group's own
// outcome to decide next steps (resume, manual fix, accept partial).
type ApplyGroupDegradedPayload struct {
	GroupID       string `json:"group_id"`
	FailedDep     string `json:"failed_dep"`
	FailedDepErr  string `json:"failed_dep_err"`
	ContinuedRun bool   `json:"continued_run"`
}

// ApplyTeamLeadSpawnedPayload is the payload of apply.team_lead.spawned.
type ApplyTeamLeadSpawnedPayload struct {
	SessionID string `json:"session_id"`
	GroupID   string `json:"group_id"`
}

// ApplyImplementSpawnFailedPayload is the payload of apply.implement.spawn_failed.
type ApplyImplementSpawnFailedPayload struct {
	TaskID string `json:"task_id"`
	Err    string `json:"err"`
}

// ApplyImplementSpawnGovernorErrorPayload is the payload of
// apply.implement.spawn_governor_error.
type ApplyImplementSpawnGovernorErrorPayload struct {
	TaskID string `json:"task_id"`
	Err    string `json:"err"`
}

// ApplyTaskClaimedPayload is the payload of apply.task.claimed.
type ApplyTaskClaimedPayload struct {
	TaskID    string `json:"task_id"`
	SessionID string `json:"session_id"`
}

// ApplyTaskClaimSkippedPayload is the payload of apply.task.claim_skipped.
type ApplyTaskClaimSkippedPayload struct {
	TaskID string `json:"task_id"`
	Err    string `json:"err"`
}

// ApplyTaskEscalatedPayload is the payload of apply.task.escalated.
//
// Spec #51: pre-fix the payload only carried task_id/attempts/reason, so
// the operator watching SSE never saw WHY the implement-agent kept
// failing — the actual reason (e.g. "Provided spec context is BLOCKED")
// lived in agent_sessions.envelope and required SQL to retrieve. The
// new FinalEnvelopeSummary + BlockingRequirements fields surface that
// signal directly on the wire so an operator can react without DB
// access. Both are empty/nil when the escalation has no associated
// envelope (e.g. all 3 attempts were dispatch errors before envelope
// validation).
type ApplyTaskEscalatedPayload struct {
	TaskID               string   `json:"task_id"`
	Attempts             int      `json:"attempts"`
	Reason               string   `json:"reason"`
	FinalEnvelopeSummary string   `json:"final_envelope_summary,omitempty"`
	BlockingRequirements []string `json:"blocking_requirements,omitempty"`
}

// ApplyTaskRetryPayload is the payload of apply.task.retry.
type ApplyTaskRetryPayload struct {
	TaskID   string `json:"task_id"`
	Attempts int    `json:"attempts"`
}

// ApplyDispatchErrorPayload is the payload of apply.dispatch.error.
// Distinct from RuntimeDispatchFailedPayload: this signals the
// dispatcher returned a transport-level error (HTTP/ctx), NOT that
// the agent CLI itself failed to start.
type ApplyDispatchErrorPayload struct {
	TaskID string `json:"task_id"`
	Err    string `json:"err"`
}

// ApplyEnvelopeValidationFailedPayload is the payload of
// apply.envelope.validation_failed.
type ApplyEnvelopeValidationFailedPayload struct {
	TaskID string `json:"task_id"`
	Err    string `json:"err"`
}

// RuntimeDispatchFailedPayload is the payload of runtime.dispatch_failed
// (receipt.Status != "success" — the agent CLI never ran).
type RuntimeDispatchFailedPayload struct {
	TaskID string `json:"task_id"`
	Err    string `json:"err"`
}

// MemoryArtifactPersistFailedPayload is the payload of
// memory.artifact_persist_failed. PhaseID identifies the phase whose
// envelope declared the artifact; TopicKey is the failing
// envelope.ArtifactsSaved entry; Type is the artifact type the LLM
// declared (e.g. "explore", "spec"); Err is the underlying memory-
// engine error (HTTP code + body or transport failure).
type MemoryArtifactPersistFailedPayload struct {
	PhaseID  string `json:"phase_id"`
	TopicKey string `json:"topic_key"`
	Type     string `json:"type"`
	Err      string `json:"err"`
}

// --- phase pipeline + governance + agent lifecycle -------------------

// PhaseStartedPayload is the payload of phase.started.
type PhaseStartedPayload struct {
	PhaseID   string    `json:"phase_id"`
	PhaseType string    `json:"phase_type"`
	ChangeID  string    `json:"change_id"`
	StartedAt time.Time `json:"started_at"`
}

// GovernanceDecisionPayload is the payload of governance.decision.
type GovernanceDecisionPayload struct {
	Decision  string `json:"decision"`
	Reason    string `json:"reason"`
	AgentRole string `json:"agent_role"`
}

// ApprovalRequiredPayload is the payload of approval.required.
type ApprovalRequiredPayload struct {
	PhaseID string `json:"phase_id"`
	GateURL string `json:"gate_url"`
	Reason  string `json:"reason"`
}

// ApprovalResolvedPayload is the payload of approval.resolved (both
// approve and reject — Decision discriminates).
type ApprovalResolvedPayload struct {
	PhaseID   string    `json:"phase_id"`
	Decision  string    `json:"decision"`
	Approver  string    `json:"approver"`
	Reason    string    `json:"reason"`
	DecidedAt time.Time `json:"decided_at"`
}

// AgentDispatchedPayload is the payload of agent.dispatched.
type AgentDispatchedPayload struct {
	PhaseID   string `json:"phase_id"`
	SessionID string `json:"session_id"`
	Role      string `json:"role"`
	Provider  string `json:"provider"`
}

// AgentEnvelopeReceivedPayload is the payload of agent.envelope.received.
type AgentEnvelopeReceivedPayload struct {
	Status     string  `json:"status"`
	Confidence float64 `json:"confidence"`
}

// PhaseCompletedPayload is the payload of phase.completed,
// phase.completed_with_concerns, and phase.needs_context emitted from
// the synchronous phase-completion path. All 6 fields are required.
//
// The apply-phase finalize path emits a SLIMMER subset via
// PhaseCompletedFromApplyPayload below — two distinct types because
// `time.Time` zero value does NOT honor `omitempty` (a Go quirk:
// time.Time is a struct, not a primitive, and its zero is
// "0001-01-01T00:00:00Z" rather than empty). Keeping two types
// preserves the prior map[string]any wire shape on both sites.
// Unifying the emit paths is tracked as a separate cleanup.
type PhaseCompletedPayload struct {
	PhaseID            string    `json:"phase_id"`
	PhaseType          string    `json:"phase_type"`
	EndedAt            time.Time `json:"ended_at"`
	Confidence         float64   `json:"confidence"`
	EnvelopeStatus     string    `json:"envelope_status"`
	EnvelopeConfidence float64   `json:"envelope_confidence"`
}

// PhaseCompletedFromApplyPayload is the slimmer payload emitted from
// the apply-phase finalize path (phase/service.go runApplyPhase after
// the apply pipeline finishes). Only the envelope-derived fields are
// known at that emit site — the phase_id, phase_type, and ended_at
// would be tautological echoes that the consumer already has from
// the apply.* family of events emitted during the run.
type PhaseCompletedFromApplyPayload struct {
	EnvelopeStatus     string  `json:"envelope_status"`
	EnvelopeConfidence float64 `json:"envelope_confidence"`
}

// PhaseFailedPayload is the payload of phase.failed.
//
// Spec #48: FailureReason carries the agent-reported rule id (e.g.
// "schema_mismatch") or "unknown" when the envelope contains no rule id.
// FailureDetail carries the agent-reported message or the envelope
// executive_summary as a fallback. Existing consumers that only read
// phase_id / phase_type / ended_at / error are unaffected — the two new
// fields are additive.
type PhaseFailedPayload struct {
	PhaseID       string    `json:"phase_id"`
	PhaseType     string    `json:"phase_type"`
	EndedAt       time.Time `json:"ended_at"`
	Error         string    `json:"error"`
	FailureReason string    `json:"failure_reason,omitempty"`
	FailureDetail string    `json:"failure_detail,omitempty"`
}
