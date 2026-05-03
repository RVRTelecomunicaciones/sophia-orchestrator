package apply_test

import (
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/stretchr/testify/require"
)

func TestBoardGetters_AllExposed(t *testing.T) {
	bid := mustBoardID(t, "01ARZ3NDEKTSV4RRFFQ69G5B01")
	pid := mustPhaseID(t, "01ARZ3NDEKTSV4RRFFQ69G5P01")
	b := apply.NewBoard(bid, pid)
	require.Equal(t, bid, b.ID())
	require.Equal(t, pid, b.PhaseID())
	require.Equal(t, apply.BoardStatusBuilding, b.Status())
	require.Empty(t, b.Groups())
}

func TestGroupGetters_AllExposed(t *testing.T) {
	gid := mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G01")
	bid := mustBoardID(t, "01ARZ3NDEKTSV4RRFFQ69G5B01")
	dep1 := mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G02")
	g := apply.NewGroup(gid, bid, "alpha", []ids.GroupID{dep1})

	require.Equal(t, gid, g.ID())
	require.Equal(t, bid, g.BoardID())
	require.Equal(t, "alpha", g.Name())
	require.Equal(t, []ids.GroupID{dep1}, g.DependsOn())
	require.Empty(t, g.Tasks())
	require.Equal(t, apply.GroupStatusPending, g.Status())
	require.Empty(t, g.WorktreePath())
	require.Empty(t, g.BranchName())
}

func TestGroup_AddTask_Success(t *testing.T) {
	g := apply.NewGroup(mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G01"), mustBoardID(t, "01ARZ3NDEKTSV4RRFFQ69G5B01"), "g", nil)
	tk, err := apply.NewTask(mustTaskID(t, "01ARZ3NDEKTSV4RRFFQ69G5T01"), g.ID(), "do", []string{"src/**/*.go"})
	require.NoError(t, err)
	require.NoError(t, g.AddTask(tk))
	require.Len(t, g.Tasks(), 1)
}

func TestGroup_Fail_FromPending(t *testing.T) {
	g := apply.NewGroup(mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G01"), mustBoardID(t, "01ARZ3NDEKTSV4RRFFQ69G5B01"), "g", nil)
	require.NoError(t, g.Fail())
	require.Equal(t, apply.GroupStatusFailed, g.Status())
}

func TestTaskGetters_AllExposed(t *testing.T) {
	tid := mustTaskID(t, "01ARZ3NDEKTSV4RRFFQ69G5T01")
	gid := mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G01")
	tk, err := apply.NewTask(tid, gid, "do thing", []string{"src/a", "src/b"})
	require.NoError(t, err)

	require.Equal(t, tid, tk.ID())
	require.Equal(t, gid, tk.GroupID())
	require.Equal(t, "do thing", tk.Description())
	require.Equal(t, []string{"src/a", "src/b"}, tk.FilesPattern())
	require.Equal(t, apply.TaskStatusPending, tk.Status())
	require.Nil(t, tk.ClaimedBy())
	require.Equal(t, 0, tk.Attempts())
	require.Nil(t, tk.Envelope())
}
