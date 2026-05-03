package session_test

import (
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/stretchr/testify/require"
)

func mkIDs(t *testing.T) (ids.SessionID, ids.ChangeID, ids.PhaseID) {
	t.Helper()
	sid, err := ids.ParseSessionID("01ARZ3NDEKTSV4RRFFQ69G5S01")
	require.NoError(t, err)
	cid, err := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	require.NoError(t, err)
	pid, err := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5P01")
	require.NoError(t, err)
	return sid, cid, pid
}

func TestRole_IsValid(t *testing.T) {
	roles := []session.AgentRole{
		session.RoleSDDInit, session.RoleSDDExplore, session.RoleSDDProposal,
		session.RoleSDDSpec, session.RoleSDDDesign, session.RoleSDDTasks,
		session.RoleSDDVerify, session.RoleSDDArchive,
		session.RoleTeamLead, session.RoleImplement,
	}
	for _, r := range roles {
		require.True(t, r.IsValid(), "role %q must be valid", r)
	}
	require.False(t, session.AgentRole("nope").IsValid())
}

func TestProvider_V1_OnlyOpenCode(t *testing.T) {
	require.True(t, session.ProviderOpenCode.IsValidV1())
	require.False(t, session.ProviderClaudeCode.IsValidV1())
	require.False(t, session.ProviderCursor.IsValidV1())
	require.False(t, session.ProviderGemini.IsValidV1())
	require.False(t, session.Provider("nope").IsValidV1())
}

func TestProvider_IsValid_AllKnown(t *testing.T) {
	require.True(t, session.ProviderOpenCode.IsValid())
	require.True(t, session.ProviderClaudeCode.IsValid())
	require.True(t, session.ProviderCursor.IsValid())
	require.True(t, session.ProviderGemini.IsValid())
	require.False(t, session.Provider("nope").IsValid())
}

func TestNew_Valid(t *testing.T) {
	sid, cid, pid := mkIDs(t)
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	s, err := session.New(sid, cid, pid, session.RoleSDDSpec, session.ProviderOpenCode, "deadbeef", "opencode run", now)
	require.NoError(t, err)
	require.Equal(t, session.StatusPending, s.Status())
	require.Equal(t, session.RoleSDDSpec, s.Role())
	require.Equal(t, session.ProviderOpenCode, s.Provider())
}

func TestNew_RejectsClaudeCodeInV1(t *testing.T) {
	sid, cid, pid := mkIDs(t)
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	_, err := session.New(sid, cid, pid, session.RoleSDDSpec, session.ProviderClaudeCode, "h", "c", now)
	require.ErrorIs(t, err, session.ErrInvalidProvider)
}

func TestNew_RejectsInvalidRole(t *testing.T) {
	sid, cid, pid := mkIDs(t)
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	_, err := session.New(sid, cid, pid, session.AgentRole("nope"), session.ProviderOpenCode, "h", "c", now)
	require.ErrorIs(t, err, session.ErrInvalidRole)
}

func TestNew_RejectsEmptyPromptHash(t *testing.T) {
	sid, cid, pid := mkIDs(t)
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	_, err := session.New(sid, cid, pid, session.RoleSDDSpec, session.ProviderOpenCode, "", "c", now)
	require.ErrorIs(t, err, session.ErrEmptyPromptHash)
}

func TestMarkRunning_FromPending(t *testing.T) {
	sid, cid, pid := mkIDs(t)
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	s, _ := session.New(sid, cid, pid, session.RoleSDDSpec, session.ProviderOpenCode, "h", "c", now)
	require.NoError(t, s.MarkRunning())
	require.Equal(t, session.StatusRunning, s.Status())
}

func TestRecordOutcome_Success(t *testing.T) {
	sid, cid, pid := mkIDs(t)
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	s, _ := session.New(sid, cid, pid, session.RoleSDDSpec, session.ProviderOpenCode, "h", "c", now)
	require.NoError(t, s.MarkRunning())
	env := &envelope.Envelope{SchemaVersion: envelope.SchemaVersionV1, Phase: "spec", ChangeName: "x", Project: "y", Status: envelope.StatusDone, Confidence: 0.85}
	require.NoError(t, s.RecordOutcome(env, 0, now))
	require.Equal(t, session.StatusDone, s.Status())
	require.NotNil(t, s.Envelope())
	require.NotNil(t, s.ExitCode())
	require.Equal(t, 0, *s.ExitCode())
}

func TestRecordOutcome_Failure(t *testing.T) {
	sid, cid, pid := mkIDs(t)
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	s, _ := session.New(sid, cid, pid, session.RoleSDDSpec, session.ProviderOpenCode, "h", "c", now)
	require.NoError(t, s.MarkRunning())
	require.NoError(t, s.RecordOutcome(nil, 1, now))
	require.Equal(t, session.StatusFailed, s.Status())
}

func TestMarkTimeout(t *testing.T) {
	sid, cid, pid := mkIDs(t)
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	s, _ := session.New(sid, cid, pid, session.RoleSDDSpec, session.ProviderOpenCode, "h", "c", now)
	require.NoError(t, s.MarkRunning())
	require.NoError(t, s.MarkTimeout(now))
	require.Equal(t, session.StatusTimeout, s.Status())
}

func TestRecordOutcome_RejectsNotRunning(t *testing.T) {
	sid, cid, pid := mkIDs(t)
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	s, _ := session.New(sid, cid, pid, session.RoleSDDSpec, session.ProviderOpenCode, "h", "c", now)
	require.ErrorIs(t, s.RecordOutcome(nil, 1, now), session.ErrNotRunning)
}

func TestAssignWorktree(t *testing.T) {
	sid, cid, pid := mkIDs(t)
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	s, _ := session.New(sid, cid, pid, session.RoleImplement, session.ProviderOpenCode, "h", "c", now)
	wid, err := ids.ParseWorktreeID("01ARZ3NDEKTSV4RRFFQ69G5W01")
	require.NoError(t, err)
	s.AssignWorktree(wid)
	require.NotNil(t, s.WorktreeID())
	require.Equal(t, wid, *s.WorktreeID())
}

func TestNew_RejectsEmptyCommand(t *testing.T) {
	sid, cid, pid := mkIDs(t)
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	_, err := session.New(sid, cid, pid, session.RoleSDDSpec, session.ProviderOpenCode, "h", "", now)
	require.ErrorIs(t, err, session.ErrEmptyCommand)
}

func TestMarkRunning_RejectsRunning(t *testing.T) {
	sid, cid, pid := mkIDs(t)
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	s, _ := session.New(sid, cid, pid, session.RoleSDDSpec, session.ProviderOpenCode, "h", "c", now)
	require.NoError(t, s.MarkRunning())
	err := s.MarkRunning()
	require.ErrorIs(t, err, session.ErrTerminal)
}

func TestMarkTimeout_RejectsTerminal(t *testing.T) {
	sid, cid, pid := mkIDs(t)
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	s, _ := session.New(sid, cid, pid, session.RoleSDDSpec, session.ProviderOpenCode, "h", "c", now)
	require.NoError(t, s.MarkRunning())
	require.NoError(t, s.MarkTimeout(now))
	require.ErrorIs(t, s.MarkTimeout(now), session.ErrTerminal)
}

func TestHydrate_PreservesAllFields(t *testing.T) {
	sid, cid, pid := mkIDs(t)
	wid, err := ids.ParseWorktreeID("01ARZ3NDEKTSV4RRFFQ69G5W01")
	require.NoError(t, err)
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	end := now.Add(1 * time.Minute)
	exit := 0
	env := &envelope.Envelope{SchemaVersion: envelope.SchemaVersionV1, Phase: "spec", ChangeName: "x", Project: "y", Status: envelope.StatusDone, Confidence: 0.9}
	s := session.Hydrate(sid, cid, pid, session.RoleSDDSpec, session.ProviderOpenCode, &wid, "h", "c", session.StatusDone, &exit, env, now, &end)
	require.Equal(t, session.StatusDone, s.Status())
	require.Equal(t, &wid, s.WorktreeID())
	require.Equal(t, &end, s.EndedAt())
	require.NotNil(t, s.Envelope())
}
