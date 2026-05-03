package session_test

import (
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/stretchr/testify/require"
)

func TestSessionGetters_AllExposed(t *testing.T) {
	sid, cid, pid := mkIDs(t)
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	s, err := session.New(sid, cid, pid, session.RoleSDDSpec, session.ProviderOpenCode, "deadbeef", "opencode run", now)
	require.NoError(t, err)

	require.Equal(t, sid, s.ID())
	require.Equal(t, cid, s.ChangeID())
	require.Equal(t, pid, s.PhaseID())
	require.Equal(t, session.RoleSDDSpec, s.Role())
	require.Equal(t, session.ProviderOpenCode, s.Provider())
	require.Nil(t, s.WorktreeID())
	require.Equal(t, "deadbeef", s.PromptSHA256())
	require.Equal(t, "opencode run", s.Command())
	require.Equal(t, session.StatusPending, s.Status())
	require.Nil(t, s.ExitCode())
	require.Nil(t, s.Envelope())
	require.Equal(t, now, s.StartedAt())
	require.Nil(t, s.EndedAt())
}

func TestSessionStatus_IsValid(t *testing.T) {
	statuses := []session.Status{
		session.StatusPending,
		session.StatusRunning,
		session.StatusDone,
		session.StatusFailed,
		session.StatusTimeout,
	}
	for _, st := range statuses {
		require.True(t, st.IsValid(), "status %q must be valid", st)
	}
	require.False(t, session.Status("nope").IsValid())
}
