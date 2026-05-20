package inbound_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
)

// TestPayload_WireShape_PreservesKeys verifies that each typed payload
// JSON-marshals to the exact wire-key set the consumer expects. Drift
// between Go field name and json tag (or accidental removal of an
// omitempty when one is needed) would surface as a test failure rather
// than as a missing dashboard panel in production.
//
// The expected key sets here MUST match what the consumer
// (sophia-cli ssestream/client.go and the wire-v1 spec §5.3) parses.
func TestPayload_WireShape_PreservesKeys(t *testing.T) {
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name     string
		payload  any
		wantKeys []string
	}{
		// apply.* family
		{"ApplyBoardCreated", inbound.ApplyBoardCreatedPayload{BoardID: "b1", Groups: 2},
			[]string{"board_id", "groups"}},
		{"ApplyBoardSaveFailed", inbound.ApplyBoardSaveFailedPayload{Err: "x"},
			[]string{"err"}},
		{"ApplyWorktreeError", inbound.ApplyWorktreeErrorPayload{GroupID: "g1", Err: "x"},
			[]string{"group_id", "err"}},
		{"ApplyGroupCompleted", inbound.ApplyGroupCompletedPayload{GroupID: "g1", TasksDone: 3},
			[]string{"group_id", "tasks_done"}},
		{"ApplyGroupFailed", inbound.ApplyGroupFailedPayload{GroupID: "g1", Reason: "r"},
			[]string{"group_id", "reason"}},
		{"ApplyTeamLeadSpawned", inbound.ApplyTeamLeadSpawnedPayload{SessionID: "s1", GroupID: "g1"},
			[]string{"session_id", "group_id"}},
		{"ApplyImplementSpawnFailed", inbound.ApplyImplementSpawnFailedPayload{TaskID: "t1", Err: "x"},
			[]string{"task_id", "err"}},
		{"ApplyImplementSpawnGovernorError", inbound.ApplyImplementSpawnGovernorErrorPayload{TaskID: "t1", Err: "x"},
			[]string{"task_id", "err"}},
		{"ApplyTaskClaimed", inbound.ApplyTaskClaimedPayload{TaskID: "t1", SessionID: "s1"},
			[]string{"task_id", "session_id"}},
		{"ApplyTaskClaimSkipped", inbound.ApplyTaskClaimSkippedPayload{TaskID: "t1", Err: "x"},
			[]string{"task_id", "err"}},
		{"ApplyTaskEscalated", inbound.ApplyTaskEscalatedPayload{
			TaskID: "t1", Attempts: 3, Reason: "r",
			FinalEnvelopeSummary: "blocked-because-x",
			BlockingRequirements: []string{"missing-evidence-y"},
		},
			[]string{"task_id", "attempts", "reason", "final_envelope_summary", "blocking_requirements"}},
		{"ApplyTaskRetry", inbound.ApplyTaskRetryPayload{TaskID: "t1", Attempts: 2},
			[]string{"task_id", "attempts"}},
		{"ApplyDispatchError", inbound.ApplyDispatchErrorPayload{TaskID: "t1", Err: "x"},
			[]string{"task_id", "err"}},
		{"ApplyEnvelopeValidationFailed", inbound.ApplyEnvelopeValidationFailedPayload{TaskID: "t1", Err: "x"},
			[]string{"task_id", "err"}},
		{"RuntimeDispatchFailed", inbound.RuntimeDispatchFailedPayload{TaskID: "t1", Err: "x"},
			[]string{"task_id", "err"}},

		// phase pipeline + governance + agent
		{"PhaseStarted", inbound.PhaseStartedPayload{PhaseID: "p1", PhaseType: "spec", ChangeID: "c1", StartedAt: now},
			[]string{"phase_id", "phase_type", "change_id", "started_at"}},
		{"GovernanceDecision", inbound.GovernanceDecisionPayload{Decision: "allow", Reason: "r", AgentRole: "spec"},
			[]string{"decision", "reason", "agent_role"}},
		{"ApprovalRequired", inbound.ApprovalRequiredPayload{PhaseID: "p1", GateURL: "u", Reason: "r"},
			[]string{"phase_id", "gate_url", "reason"}},
		{"ApprovalResolved", inbound.ApprovalResolvedPayload{PhaseID: "p1", Decision: "approved", Approver: "u@x", Reason: "r", DecidedAt: now},
			[]string{"phase_id", "decision", "approver", "reason", "decided_at"}},
		{"AgentDispatched", inbound.AgentDispatchedPayload{PhaseID: "p1", SessionID: "s1", Role: "spec", Provider: "opencode"},
			[]string{"phase_id", "session_id", "role", "provider"}},
		{"AgentEnvelopeReceived", inbound.AgentEnvelopeReceivedPayload{Status: "DONE", Confidence: 0.9},
			[]string{"status", "confidence"}},
		{"PhaseFailed", inbound.PhaseFailedPayload{PhaseID: "p1", PhaseType: "spec", EndedAt: now, Error: "boom"},
			[]string{"phase_id", "phase_type", "ended_at", "error"}},
	}

	// Spec #48: FailureReason and FailureDetail are additive — the base
	// set above verifies backwards compat (omitempty on the new fields
	// means they don't appear when empty). This case verifies they DO
	// appear when populated.
	t.Run("PhaseFailedWithReason", func(t *testing.T) {
		payload := inbound.PhaseFailedPayload{
			PhaseID: "p1", PhaseType: "spec", EndedAt: now, Error: "boom",
			FailureReason: "schema_mismatch",
			FailureDetail: "tasks output must be data.groups[]",
		}
		raw, err := json.Marshal(payload)
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		require.Equal(t, "schema_mismatch", m["failure_reason"], "failure_reason must appear when set")
		require.Equal(t, "tasks output must be data.groups[]", m["failure_detail"], "failure_detail must appear when set")
		// Backwards-compat fields still present.
		require.Equal(t, "boom", m["error"])
		require.Equal(t, "p1", m["phase_id"])
	})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.payload)
			require.NoError(t, err)
			var m map[string]any
			require.NoError(t, json.Unmarshal(raw, &m))
			gotKeys := make(map[string]struct{}, len(m))
			for k := range m {
				gotKeys[k] = struct{}{}
			}
			for _, want := range tc.wantKeys {
				_, present := gotKeys[want]
				require.True(t, present, "%s payload missing wire key %q (json output: %s)",
					tc.name, want, string(raw))
			}
			require.Lenf(t, gotKeys, len(tc.wantKeys),
				"%s payload has unexpected keys (want %v, got %v)", tc.name, tc.wantKeys, m)
		})
	}
}

// TestPhaseCompleted_TwoShapes verifies that the two emit sites for
// phase.completed (sync path + apply-finalize path) use distinct
// payload structs, each with the field set documented in the wire
// catalogue. They are NOT unified into one omitempty-tagged struct
// because time.Time's zero value does not honor omitempty (Go quirk).
func TestPhaseCompleted_TwoShapes(t *testing.T) {
	// Site A: full 6-field payload.
	siteA := inbound.PhaseCompletedPayload{
		PhaseID:            "p1",
		PhaseType:          "spec",
		EndedAt:            time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC),
		Confidence:         0.85,
		EnvelopeStatus:     "DONE",
		EnvelopeConfidence: 0.85,
	}
	raw, err := json.Marshal(siteA)
	require.NoError(t, err)
	var mA map[string]any
	require.NoError(t, json.Unmarshal(raw, &mA))
	require.Len(t, mA, 6, "site-A PhaseCompleted must carry all 6 fields")
	require.Equal(t, "p1", mA["phase_id"])
	require.Equal(t, "spec", mA["phase_type"])

	// Site B (from apply finalize): only 2 envelope-derived fields.
	siteB := inbound.PhaseCompletedFromApplyPayload{
		EnvelopeStatus:     "DONE",
		EnvelopeConfidence: 0.85,
	}
	raw, err = json.Marshal(siteB)
	require.NoError(t, err)
	var mB map[string]any
	require.NoError(t, json.Unmarshal(raw, &mB))
	require.Len(t, mB, 2, "site-B (from apply) must carry only 2 fields, got %v", mB)
	require.Equal(t, "DONE", mB["envelope_status"])
}
