package inbound_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
)

// TestIsKnownEventType_DocumentedConstants verifies every Event* constant
// declared in event_types.go is recognised by IsKnownEventType. Catches
// drift where a new constant is added but the map isn't updated.
func TestIsKnownEventType_DocumentedConstants(t *testing.T) {
	cases := []string{
		inbound.EventPhaseStarted,
		inbound.EventPhaseCompleted,
		inbound.EventPhaseCompletedWithConcerns,
		inbound.EventPhaseFailed,
		inbound.EventPhaseNeedsContext,
		inbound.EventApprovalRequired,
		inbound.EventApprovalResolved,
		inbound.EventGovernanceDecision,
		inbound.EventAgentDispatched,
		inbound.EventAgentEnvelopeReceived,
		inbound.EventApplyBoardCreated,
		inbound.EventApplyBoardSaveFailed,
		inbound.EventApplyWorktreeError,
		inbound.EventApplyGroupCompleted,
		inbound.EventApplyGroupFailed,
		inbound.EventApplyTeamLeadSpawned,
		inbound.EventApplyImplementSpawnFailed,
		inbound.EventApplyImplementSpawnGovernorError,
		inbound.EventApplyTaskClaimed,
		inbound.EventApplyTaskClaimSkipped,
		inbound.EventApplyTaskEscalated,
		inbound.EventApplyTaskRetry,
		inbound.EventApplyDispatchError,
		inbound.EventApplyEnvelopeValidationFailed,
		inbound.EventRuntimeDispatchFailed,
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			require.True(t, inbound.IsKnownEventType(name),
				"constant %q is declared but not recognised by IsKnownEventType — knownEventTypes map is missing the entry",
				name)
		})
	}
}

// TestIsKnownEventType_RejectsUnknown verifies plausible-looking
// typos are rejected so typo-driven drift surfaces in unit tests
// rather than silently in production.
func TestIsKnownEventType_RejectsUnknown(t *testing.T) {
	for _, typo := range []string{
		"",
		"apply.task.escalat",        // truncated
		"apply.task.escalateddd",    // suffix garbage
		"apply.tasks.escalated",     // wrong segment plural
		"governance.decisions",      // wrong segment plural
		"agent.envelope.recieved",   // common misspelling
		"phase.startd",              // missing 'e'
		"unknown.event",             // wholly invented
	} {
		t.Run(typo, func(t *testing.T) {
			require.False(t, inbound.IsKnownEventType(typo),
				"plausible-looking typo %q must be rejected by IsKnownEventType", typo)
		})
	}
}

// TestEventConstantUniqueness guards against accidental collisions —
// two constants resolving to the same string would mask a typo at the
// declaration site.
func TestEventConstantUniqueness(t *testing.T) {
	all := []string{
		inbound.EventPhaseStarted, inbound.EventPhaseCompleted,
		inbound.EventPhaseCompletedWithConcerns, inbound.EventPhaseFailed,
		inbound.EventPhaseNeedsContext, inbound.EventApprovalRequired,
		inbound.EventApprovalResolved, inbound.EventGovernanceDecision,
		inbound.EventAgentDispatched, inbound.EventAgentEnvelopeReceived,
		inbound.EventApplyBoardCreated, inbound.EventApplyBoardSaveFailed,
		inbound.EventApplyWorktreeError, inbound.EventApplyGroupCompleted,
		inbound.EventApplyGroupFailed, inbound.EventApplyTeamLeadSpawned,
		inbound.EventApplyImplementSpawnFailed,
		inbound.EventApplyImplementSpawnGovernorError,
		inbound.EventApplyTaskClaimed, inbound.EventApplyTaskClaimSkipped,
		inbound.EventApplyTaskEscalated, inbound.EventApplyTaskRetry,
		inbound.EventApplyDispatchError, inbound.EventApplyEnvelopeValidationFailed,
		inbound.EventRuntimeDispatchFailed,
	}
	seen := map[string]bool{}
	for _, name := range all {
		require.False(t, seen[name],
			"duplicate event constant value %q — two constants resolve to the same string", name)
		seen[name] = true
	}
}
