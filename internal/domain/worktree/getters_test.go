package worktree_test

import (
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/worktree"
	"github.com/stretchr/testify/require"
)

func TestWorktreeGetters_AllExposed(t *testing.T) {
	id := mkWID(t)
	sid, err := ids.ParseSessionID("01ARZ3NDEKTSV4RRFFQ69G5S01")
	require.NoError(t, err)
	w, err := worktree.New(id, &sid, "/var/sophia/wt/g1", "sophia/feat-x/group-1", now())
	require.NoError(t, err)

	require.Equal(t, id, w.ID())
	require.Equal(t, &sid, w.SessionID())
	require.Equal(t, "/var/sophia/wt/g1", w.Path())
	require.Equal(t, "sophia/feat-x/group-1", w.Branch())
	require.Equal(t, worktree.StatusCreated, w.Status())
	require.Equal(t, now(), w.CreatedAt())
	require.Nil(t, w.CleanedAt())
}

func TestWorktree_GetterAfterClean(t *testing.T) {
	w, _ := worktree.New(mkWID(t), nil, "/p", "b", now())
	require.NoError(t, w.Clean(now()))
	require.NotNil(t, w.CleanedAt())
	require.Equal(t, now(), *w.CleanedAt())
}
