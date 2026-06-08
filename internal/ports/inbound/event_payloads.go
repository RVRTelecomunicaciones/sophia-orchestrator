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
	GroupID      string `json:"group_id"`
	FailedDep    string `json:"failed_dep"`
	FailedDepErr string `json:"failed_dep_err"`
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

// ApplyProviderQuotaExceededPayload is the payload of
// apply.provider.quota_exceeded. Emitted when the dispatcher returns
// ErrProviderQuotaExceeded for an implement attempt.
//
// The task is NOT recorded as a burned Iron-Law-5 attempt — a quota
// exhaustion is a provider-side constraint, not an agent failure. The
// task is released to a resume-safe state so a later resume can retry
// it against a replenished or fallback provider. RetryAfterSeconds is
// zero when the provider did not supply a Retry-After header.
// Evidence is a ≤200-char snippet from the combined stdout+stderr that
// triggered detection (useful for operator dashboards without DB access).
type ApplyProviderQuotaExceededPayload struct {
	TaskID            string `json:"task_id"`
	Provider          string `json:"provider"`
	Model             string `json:"model"`
	RetryAfterSeconds int    `json:"retry_after_seconds"`
	Evidence          string `json:"evidence"`
}

// ApplyProviderFallbackUsedPayload is the payload of
// apply.provider.fallback_used. Emitted when the primary model hit quota
// (ErrProviderQuotaExceeded) and the apply phase successfully completed
// the task by re-dispatching with the configured fallback model.
//
// The fallback dispatch is a SINGLE extra try — it does NOT consume an
// Iron-Law-5 attempt. PrimaryQuotaErr carries the evidence snippet from
// the primary's *ProviderQuotaError so operators can correlate the
// fallback event with the quota signal without querying the DB.
type ApplyProviderFallbackUsedPayload struct {
	TaskID            string `json:"task_id"`
	FallbackModel     string `json:"fallback_model"`
	PrimaryProvider   string `json:"primary_provider"`
	PrimaryModel      string `json:"primary_model"`
	RetryAfterSeconds int    `json:"retry_after_seconds"`
	Evidence          string `json:"evidence"`
}

// ApplyPhaseQuotaAbortedPayload is the payload of apply.phase.quota_aborted.
// Emitted ONCE when the per-Execute quota circuit breaker trips: Streak
// consecutive task outcomes were quota outcomes (primary + fallback both
// exhausted or absent) with no intervening successful task. The phase is
// cancelled and a BLOCKED envelope naming the remedy is returned.
//
// Threshold is the configured N (SOPHIA_APPLY_QUOTA_BREAKER_THRESHOLD).
// Streak is the final consecutive-quota count that crossed the threshold
// (equal to Threshold on a clean trip). LastProvider and LastModel are
// the provider/model strings from the most recent quota outcome, useful
// for correlating with the provider's own quota dashboard. RetryAfter is
// the retry_after_seconds from the last quota outcome (zero when absent).
type ApplyPhaseQuotaAbortedPayload struct {
	Threshold    int    `json:"threshold"`
	Streak       int    `json:"streak"`
	LastProvider string `json:"last_provider"`
	LastModel    string `json:"last_model"`
	RetryAfter   int    `json:"retry_after_seconds"`
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

// ApplyBuildStartedPayload is the payload of apply.build.started. Emitted
// immediately before the build command executes.
type ApplyBuildStartedPayload struct {
	GroupID  string   `json:"group_id"`
	Manifest string   `json:"manifest"`
	Command  string   `json:"command"`
	Args     []string `json:"args"`
	Attempt  int      `json:"attempt"`
}

// ApplyBuildPassedPayload is the payload of apply.build.passed.
type ApplyBuildPassedPayload struct {
	GroupID    string `json:"group_id"`
	Manifest   string `json:"manifest"`
	Command    string `json:"command"`
	Attempt    int    `json:"attempt"`
	DurationMS int    `json:"duration_ms"`
}

// ApplyBuildFailedPayload is the payload of apply.build.failed. Stderr
// is truncated to stderrBudgetBytes (4 KB head+tail); Truncated is true
// when truncation occurred. ExitCode is -1 for runtime/transport errors.
type ApplyBuildFailedPayload struct {
	GroupID   string `json:"group_id"`
	Manifest  string `json:"manifest"`
	Command   string `json:"command"`
	Attempt   int    `json:"attempt"`
	ExitCode  int    `json:"exit_code"`
	Stderr    string `json:"stderr"`
	Truncated bool   `json:"truncated"`
}

// ApplyMaterializeStartedPayload is the payload of apply.materialize.started.
type ApplyMaterializeStartedPayload struct {
	TargetPath string `json:"target_path"`
}

// ApplyMaterializeCompletedPayload is the payload of apply.materialize.completed.
type ApplyMaterializeCompletedPayload struct {
	TargetPath         string `json:"target_path"`
	GroupsMaterialized int    `json:"groups_materialized"`
}

// ApplyMaterializeErrorPayload is the payload of apply.materialize.error.
// Best-effort per-group failure during the BUG-29 materialize pass.
type ApplyMaterializeErrorPayload struct {
	GroupID string `json:"group_id"`
	Err     string `json:"err"`
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

// PhaseArchivedPayload is the payload of phase.archived. Emitted by
// application/phase/service.go inside advanceChange when the just-completed
// phase is PhaseArchive AND the Change has been marked Completed AND
// ChangeRepo.Save has returned without error (Iron Law D1.2 satisfied).
//
// Carries enough identifying data for the memory-engine consolidation worker
// to fetch the full change context without subscribing to upstream
// phase.completed events. ArchivedAt is sourced from the injectable Clock
// (never time.Now() directly — V4.1 D13 / CLAUDE.md Iron Law #5).
type PhaseArchivedPayload struct {
	ChangeID   string    `json:"change_id"`
	ChangeName string    `json:"change_name"`
	PhaseType  string    `json:"phase_type"`  // always "archive"
	ArchivedAt time.Time `json:"archived_at"` // s.d.Clock.Now() at emission
}

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
