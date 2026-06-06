package inbound

// SSE event-name constants emitted by the orchestrator.
//
// Background: every event the orch publishes to the SSE stream travels as
// a free-form string (Event.Type). Without a single source of truth the
// emitter and consumer could drift on a typo with zero compiler help —
// e.g. orch emits "apply.task.escalat" while sophia-cli matches on
// "apply.task.escalated", and the drift surfaces only as a missing
// dashboard panel or a confused operator. This file centralises every
// name so a typo at the emit site is a compile error.
//
// Cross-repo mirror: sophia-cli/pkg/contract/events.go MUST carry the
// same set so the CLI's IsKnownEvent recognises all names. Adding a new
// event here without mirroring there is an audit issue (tracked under
// the wire-alignment matrix, ADR-0006 row "cli ↔ orch").
const (
	// --- phase pipeline (emitted by application/phase/service.go) ---

	// EventPhaseStarted is published once when a phase moves to running.
	EventPhaseStarted = "phase.started"
	// EventPhaseCompleted is published when the phase reaches DONE.
	EventPhaseCompleted = "phase.completed"
	// EventPhaseCompletedWithConcerns is published when the phase reaches
	// DONE but the envelope carries non-blocking risks.
	EventPhaseCompletedWithConcerns = "phase.completed_with_concerns"
	// EventPhaseFailed is published on any terminal failure (BLOCKED,
	// envelope schema invalid, Iron Law violation, etc.).
	EventPhaseFailed = "phase.failed"
	// EventPhaseNeedsContext is published when the phase status is
	// NEEDS_CONTEXT — caller must supply more input via /resume.
	EventPhaseNeedsContext = "phase.needs_context"

	// EventApprovalRequired is published when a sensitive phase pauses
	// pending human approval.
	EventApprovalRequired = "approval.required"
	// EventApprovalResolved is published when an /approve or /reject lands.
	EventApprovalResolved = "approval.resolved"

	// EventGovernanceDecision is published with the governance verdict
	// (allow / deny / require-approval) for a phase about to run.
	EventGovernanceDecision = "governance.decision"

	// --- agent lifecycle (emitted by application/phase/service.go) ---

	// EventAgentDispatched is published when the dispatcher is invoked.
	EventAgentDispatched = "agent.dispatched"
	// EventAgentEnvelopeReceived is published when the agent's envelope
	// has been parsed (regardless of status).
	EventAgentEnvelopeReceived = "agent.envelope.received"

	// --- apply phase (emitted by application/apply/) ---

	// EventApplyBoardCreated is published once the apply board persists.
	EventApplyBoardCreated = "apply.board.created"
	// EventApplyBoardSaveFailed is published when the post-completion
	// SaveBoard call errors.
	EventApplyBoardSaveFailed = "apply.board.save_failed"
	// EventApplyWorktreeError is published when createWorktrees fails for
	// any group.
	EventApplyWorktreeError = "apply.worktree.error"

	// EventApplyGroupCompleted is published when a group finishes all its
	// tasks successfully.
	EventApplyGroupCompleted = "apply.group.completed"
	// EventApplyGroupFailed is published when a group's wait/Acquire/team-
	// lead returns an error, OR when at least one task BLOCKED.
	EventApplyGroupFailed = "apply.group.failed"
	// EventApplyGroupDegraded (BUG-30) is published when a group's
	// upstream dependency failed but the group continues to execute
	// anyway. The group's outcome is independent of the failed
	// dependency — this event surfaces the degraded condition without
	// cascading the failure.
	EventApplyGroupDegraded = "apply.group.degraded"

	// EventApplyTeamLeadSpawned is published once per group when the
	// team-lead session is created.
	EventApplyTeamLeadSpawned = "apply.team_lead.spawned"
	// EventApplyImplementSpawnFailed is published when the implement
	// session itself could not be constructed (rare; ID gen or repo err).
	EventApplyImplementSpawnFailed = "apply.implement.spawn_failed"
	// EventApplyImplementSpawnGovernorError is published when the
	// SpawnGovernor refuses the implement attempt (cap reached, ctx done).
	EventApplyImplementSpawnGovernorError = "apply.implement.spawn_governor_error"

	// EventApplyTaskClaimed is published after the atomic ClaimTask
	// succeeds for an implement attempt.
	EventApplyTaskClaimed = "apply.task.claimed"
	// EventApplyTaskClaimSkipped is published when another team-lead
	// already owns the task (defensive — should not happen with one
	// team-lead per group).
	EventApplyTaskClaimSkipped = "apply.task.claim_skipped"
	// EventApplyTaskEscalated is published on the 3rd consecutive
	// implement failure (Iron Law #5 enforcement).
	EventApplyTaskEscalated = "apply.task.escalated"
	// EventApplyTaskRetry is published between non-final implement
	// attempts.
	EventApplyTaskRetry = "apply.task.retry"

	// EventApplyProviderQuotaExceeded is published when the agent dispatcher
	// returns ErrProviderQuotaExceeded (HTTP 429 with quota signals).
	// The task is short-circuited out of the MaxAttempts loop immediately —
	// a quota exhaustion MUST NOT burn the remaining Iron-Law-5 attempts.
	// The task is released to a resume-safe failed state so a later resume
	// can retry it against a non-exhausted provider. See ADR-0010, Slice 2.
	EventApplyProviderQuotaExceeded = "apply.provider.quota_exceeded"

	// EventApplyDispatchError is published when the agent dispatcher
	// returns a transport-level error (HTTP, ctx cancellation) — distinct
	// from EventRuntimeDispatchFailed which signals the agent CLI
	// itself did not run.
	EventApplyDispatchError = "apply.dispatch.error"
	// EventApplyEnvelopeValidationFailed is published when the agent
	// produced output but the envelope is missing or fails the schema.
	EventApplyEnvelopeValidationFailed = "apply.envelope.validation_failed"

	// EventRuntimeDispatchFailed is published when receipt.Status != "success"
	// — the agent CLI was not actually executed (binary missing, shell
	// timeout, etc.). See dispatcher M-E0 #3 hardening.
	EventRuntimeDispatchFailed = "runtime.dispatch_failed"

	// EventApplyBuildStarted is published immediately before the build
	// command is executed for a group. Carries the resolved manifest,
	// command, args, and attempt number so the operator can correlate
	// with the subsequent pass/fail event.
	EventApplyBuildStarted = "apply.build.started"
	// EventApplyBuildPassed is published when the build exits with code 0.
	EventApplyBuildPassed = "apply.build.passed"
	// EventApplyBuildFailed is published when the build exits with a
	// non-zero exit code. The payload carries the (truncated) stderr so
	// the operator can inspect compiler errors without DB access.
	EventApplyBuildFailed = "apply.build.failed"

	// EventApplyMaterializeStarted (BUG-29) is published when the apply
	// phase begins copying successful group worktrees into the
	// operator-facing TargetPath.
	EventApplyMaterializeStarted = "apply.materialize.started"
	// EventApplyMaterializeCompleted is published once the materialize
	// pass finishes. GroupsMaterialized reports how many successful
	// groups were copied.
	EventApplyMaterializeCompleted = "apply.materialize.completed"
	// EventApplyMaterializeError is published per-group when the
	// materialize copy fails (mkdir or cp). Does NOT abort the
	// remaining groups — operator inspects the worktree under
	// WorktreeRoot for any group that did not land in TargetPath.
	EventApplyMaterializeError = "apply.materialize.error"

	// EventMemoryArtifactPersistFailed is published when the orch tried to
	// persist an envelope.ArtifactsSaved entry to memory-engine and the
	// Ingest call failed. The phase itself is NOT failed — the envelope is
	// already saved on the orch side (Iron Law #1) and downstream phases
	// can still recover via prior-phase reads. This event is the operator-
	// facing signal that memory-engine ingestion degraded for an artifact
	// the LLM declared.
	EventMemoryArtifactPersistFailed = "memory.artifact_persist_failed"
)

// knownEventTypes is the union of every constant declared above. Used by
// IsKnownEventType to validate that a string passed through Event.Type
// matches one of the documented names.
var knownEventTypes = map[string]struct{}{
	EventPhaseStarted:                     {},
	EventPhaseCompleted:                   {},
	EventPhaseCompletedWithConcerns:       {},
	EventPhaseFailed:                      {},
	EventPhaseNeedsContext:                {},
	EventApprovalRequired:                 {},
	EventApprovalResolved:                 {},
	EventGovernanceDecision:               {},
	EventAgentDispatched:                  {},
	EventAgentEnvelopeReceived:            {},
	EventApplyBoardCreated:                {},
	EventApplyBoardSaveFailed:             {},
	EventApplyWorktreeError:               {},
	EventApplyGroupCompleted:              {},
	EventApplyGroupFailed:                 {},
	EventApplyGroupDegraded:               {},
	EventApplyTeamLeadSpawned:             {},
	EventApplyImplementSpawnFailed:        {},
	EventApplyImplementSpawnGovernorError: {},
	EventApplyTaskClaimed:                 {},
	EventApplyTaskClaimSkipped:            {},
	EventApplyTaskEscalated:               {},
	EventApplyTaskRetry:                   {},
	EventApplyProviderQuotaExceeded:       {},
	EventApplyDispatchError:               {},
	EventApplyEnvelopeValidationFailed:    {},
	EventRuntimeDispatchFailed:            {},
	EventMemoryArtifactPersistFailed:      {},
	EventApplyBuildStarted:                {},
	EventApplyBuildPassed:                 {},
	EventApplyBuildFailed:                 {},
	EventApplyMaterializeStarted:          {},
	EventApplyMaterializeCompleted:        {},
	EventApplyMaterializeError:            {},
}

// IsKnownEventType reports whether the given type string is one of the
// documented event-name constants. Useful for tests and for the SSE
// handler when validating outbound payloads.
func IsKnownEventType(eventType string) bool {
	_, ok := knownEventTypes[eventType]
	return ok
}
