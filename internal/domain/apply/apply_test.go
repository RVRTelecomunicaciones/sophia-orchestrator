package apply_test

import (
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/stretchr/testify/require"
)

func mustBoardID(t *testing.T, raw string) ids.BoardID {
	t.Helper()
	id, err := ids.ParseBoardID(raw)
	require.NoError(t, err)
	return id
}

func mustPhaseID(t *testing.T, raw string) ids.PhaseID {
	t.Helper()
	id, err := ids.ParsePhaseID(raw)
	require.NoError(t, err)
	return id
}

func mustGroupID(t *testing.T, raw string) ids.GroupID {
	t.Helper()
	id, err := ids.ParseGroupID(raw)
	require.NoError(t, err)
	return id
}

func mustTaskID(t *testing.T, raw string) ids.TaskID {
	t.Helper()
	id, err := ids.ParseTaskID(raw)
	require.NoError(t, err)
	return id
}

func mustSessionID(t *testing.T, raw string) ids.SessionID {
	t.Helper()
	id, err := ids.ParseSessionID(raw)
	require.NoError(t, err)
	return id
}

func TestBoard_NewIsBuilding(t *testing.T) {
	b := apply.NewBoard(mustBoardID(t, "01ARZ3NDEKTSV4RRFFQ69G5B01"), mustPhaseID(t, "01ARZ3NDEKTSV4RRFFQ69G5P01"))
	require.Equal(t, apply.BoardStatusBuilding, b.Status())
}

func TestBoard_Lifecycle(t *testing.T) {
	b := apply.NewBoard(mustBoardID(t, "01ARZ3NDEKTSV4RRFFQ69G5B01"), mustPhaseID(t, "01ARZ3NDEKTSV4RRFFQ69G5P01"))
	require.NoError(t, b.Start())
	require.Equal(t, apply.BoardStatusRunning, b.Status())
	require.NoError(t, b.Complete())
	require.Equal(t, apply.BoardStatusCompleted, b.Status())
	require.ErrorIs(t, b.Start(), apply.ErrInvalidBoardTransition)
}

func TestBoard_FailFromRunning(t *testing.T) {
	b := apply.NewBoard(mustBoardID(t, "01ARZ3NDEKTSV4RRFFQ69G5B01"), mustPhaseID(t, "01ARZ3NDEKTSV4RRFFQ69G5P01"))
	require.NoError(t, b.Start())
	require.NoError(t, b.Fail())
	require.Equal(t, apply.BoardStatusFailed, b.Status())
}

func TestBoard_AddGroup_OnlyDuringBuilding(t *testing.T) {
	b := apply.NewBoard(mustBoardID(t, "01ARZ3NDEKTSV4RRFFQ69G5B01"), mustPhaseID(t, "01ARZ3NDEKTSV4RRFFQ69G5P01"))
	g := apply.NewGroup(mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G01"), b.ID(), "g1", nil)
	require.NoError(t, b.AddGroup(g))
	require.NoError(t, b.Start())
	g2 := apply.NewGroup(mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G02"), b.ID(), "g2", nil)
	require.ErrorIs(t, b.AddGroup(g2), apply.ErrInvalidBoardTransition)
}

func TestGroup_Lifecycle(t *testing.T) {
	g := apply.NewGroup(mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G01"), mustBoardID(t, "01ARZ3NDEKTSV4RRFFQ69G5B01"), "g1", nil)
	require.NoError(t, g.Start())
	require.NoError(t, g.Complete())
	require.ErrorIs(t, g.Start(), apply.ErrInvalidGroupTransition)
}

func TestGroup_AssignWorktree(t *testing.T) {
	g := apply.NewGroup(mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G01"), mustBoardID(t, "01ARZ3NDEKTSV4RRFFQ69G5B01"), "g1", nil)
	g.AssignWorktree("/var/sophia/wt/g1", "sophia/feat-x/group-1")
	require.Equal(t, "/var/sophia/wt/g1", g.WorktreePath())
	require.Equal(t, "sophia/feat-x/group-1", g.BranchName())
}

func TestValidateDAG_Acyclic(t *testing.T) {
	a := mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G01")
	b := mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G02")
	c := mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G03")

	groups := []*apply.Group{
		apply.NewGroup(a, mustBoardID(t, "01ARZ3NDEKTSV4RRFFQ69G5B01"), "a", nil),
		apply.NewGroup(b, mustBoardID(t, "01ARZ3NDEKTSV4RRFFQ69G5B01"), "b", nil),
		apply.NewGroup(c, mustBoardID(t, "01ARZ3NDEKTSV4RRFFQ69G5B01"), "c", []ids.GroupID{a, b}),
	}
	require.NoError(t, apply.ValidateDAG(groups))
}

func TestValidateDAG_DetectsCycle(t *testing.T) {
	a := mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G01")
	b := mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G02")

	groups := []*apply.Group{
		apply.NewGroup(a, mustBoardID(t, "01ARZ3NDEKTSV4RRFFQ69G5B01"), "a", []ids.GroupID{b}),
		apply.NewGroup(b, mustBoardID(t, "01ARZ3NDEKTSV4RRFFQ69G5B01"), "b", []ids.GroupID{a}),
	}
	require.ErrorIs(t, apply.ValidateDAG(groups), apply.ErrCycle)
}

func TestNewTask_RejectsEmptyDescription(t *testing.T) {
	_, err := apply.NewTask(
		mustTaskID(t, "01ARZ3NDEKTSV4RRFFQ69G5T01"),
		mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G01"),
		"",
		[]string{"src/**/*.go"},
	)
	require.ErrorIs(t, err, apply.ErrEmptyDescription)
}

func TestNewTask_RejectsEmptyFilesPattern(t *testing.T) {
	_, err := apply.NewTask(
		mustTaskID(t, "01ARZ3NDEKTSV4RRFFQ69G5T01"),
		mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G01"),
		"do x",
		nil,
	)
	require.ErrorIs(t, err, apply.ErrEmptyFilesPattern)
}

func TestTask_FilesPattern_DefensiveCopy(t *testing.T) {
	patterns := []string{"src/a", "src/b"}
	tk, _ := apply.NewTask(
		mustTaskID(t, "01ARZ3NDEKTSV4RRFFQ69G5T01"),
		mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G01"),
		"do x",
		patterns,
	)
	patterns[0] = "tampered"
	require.Equal(t, []string{"src/a", "src/b"}, tk.FilesPattern())
	out := tk.FilesPattern()
	out[0] = "tampered2"
	require.Equal(t, []string{"src/a", "src/b"}, tk.FilesPattern())
}

func TestTask_ClaimReleaseRoundtrip(t *testing.T) {
	tk, _ := apply.NewTask(
		mustTaskID(t, "01ARZ3NDEKTSV4RRFFQ69G5T01"),
		mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G01"),
		"do x",
		[]string{"src/**/*.go"},
	)
	sid := mustSessionID(t, "01ARZ3NDEKTSV4RRFFQ69G5S01")
	require.NoError(t, tk.Claim(sid))
	require.Equal(t, apply.TaskStatusClaimed, tk.Status())
	require.Equal(t, &sid, tk.ClaimedBy())
	require.NoError(t, tk.Release())
	require.Equal(t, apply.TaskStatusPending, tk.Status())
	require.Nil(t, tk.ClaimedBy())
}

func TestTask_DoubleClaim_Errors(t *testing.T) {
	tk, _ := apply.NewTask(
		mustTaskID(t, "01ARZ3NDEKTSV4RRFFQ69G5T01"),
		mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G01"),
		"do x",
		[]string{"src/**/*.go"},
	)
	sid := mustSessionID(t, "01ARZ3NDEKTSV4RRFFQ69G5S01")
	require.NoError(t, tk.Claim(sid))
	require.ErrorIs(t, tk.Claim(sid), apply.ErrAlreadyClaimed)
}

func TestTask_RecordAttempt_EscalatesAtThird(t *testing.T) {
	tk, _ := apply.NewTask(
		mustTaskID(t, "01ARZ3NDEKTSV4RRFFQ69G5T01"),
		mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G01"),
		"do x",
		[]string{"src/**/*.go"},
	)
	require.NoError(t, tk.RecordAttempt(false))
	require.Equal(t, apply.TaskStatusFailed, tk.Status())
	require.NoError(t, tk.RecordAttempt(false))
	require.Equal(t, apply.TaskStatusFailed, tk.Status())
	err := tk.RecordAttempt(false)
	require.ErrorIs(t, err, apply.ErrEscalationRequired)
	require.Equal(t, apply.TaskStatusBlocked, tk.Status())
	require.Equal(t, 3, tk.Attempts())
}

func TestTask_RecordAttempt_SuccessShortCircuits(t *testing.T) {
	tk, _ := apply.NewTask(
		mustTaskID(t, "01ARZ3NDEKTSV4RRFFQ69G5T01"),
		mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G01"),
		"do x",
		[]string{"src/**/*.go"},
	)
	require.NoError(t, tk.RecordAttempt(true))
	require.Equal(t, apply.TaskStatusDone, tk.Status())
}

func TestTask_Complete_RequiresRunning(t *testing.T) {
	tk, _ := apply.NewTask(
		mustTaskID(t, "01ARZ3NDEKTSV4RRFFQ69G5T01"),
		mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G01"),
		"do x",
		[]string{"src/**/*.go"},
	)
	env := &envelope.Envelope{SchemaVersion: envelope.SchemaVersionV1, Phase: "apply", ChangeName: "x", Project: "y", Status: envelope.StatusDone, Confidence: 0.85}
	require.ErrorIs(t, tk.Complete(env), apply.ErrInvalidTaskTransition)
}

func TestBoard_Fail_RejectsAfterCompleted(t *testing.T) {
	b := apply.NewBoard(mustBoardID(t, "01ARZ3NDEKTSV4RRFFQ69G5B01"), mustPhaseID(t, "01ARZ3NDEKTSV4RRFFQ69G5P01"))
	require.NoError(t, b.Start())
	require.NoError(t, b.Complete())
	require.ErrorIs(t, b.Fail(), apply.ErrInvalidBoardTransition)
}

func TestBoard_Complete_RejectsNotRunning(t *testing.T) {
	b := apply.NewBoard(mustBoardID(t, "01ARZ3NDEKTSV4RRFFQ69G5B01"), mustPhaseID(t, "01ARZ3NDEKTSV4RRFFQ69G5P01"))
	require.ErrorIs(t, b.Complete(), apply.ErrInvalidBoardTransition)
}

func TestBoard_HydrateRoundtrip(t *testing.T) {
	g := apply.NewGroup(mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G01"), mustBoardID(t, "01ARZ3NDEKTSV4RRFFQ69G5B01"), "g", nil)
	b := apply.HydrateBoard(mustBoardID(t, "01ARZ3NDEKTSV4RRFFQ69G5B01"), mustPhaseID(t, "01ARZ3NDEKTSV4RRFFQ69G5P01"), apply.BoardStatusRunning, []*apply.Group{g})
	require.Equal(t, apply.BoardStatusRunning, b.Status())
	require.Len(t, b.Groups(), 1)
}

func TestGroup_Complete_RejectsNotRunning(t *testing.T) {
	g := apply.NewGroup(mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G01"), mustBoardID(t, "01ARZ3NDEKTSV4RRFFQ69G5B01"), "g", nil)
	require.ErrorIs(t, g.Complete(), apply.ErrInvalidGroupTransition)
}

func TestGroup_Fail_RejectsAfterCompleted(t *testing.T) {
	g := apply.NewGroup(mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G01"), mustBoardID(t, "01ARZ3NDEKTSV4RRFFQ69G5B01"), "g", nil)
	require.NoError(t, g.Start())
	require.NoError(t, g.Complete())
	require.ErrorIs(t, g.Fail(), apply.ErrInvalidGroupTransition)
}

func TestGroup_AddTask_RejectsAfterStart(t *testing.T) {
	g := apply.NewGroup(mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G01"), mustBoardID(t, "01ARZ3NDEKTSV4RRFFQ69G5B01"), "g", nil)
	tk, err := apply.NewTask(mustTaskID(t, "01ARZ3NDEKTSV4RRFFQ69G5T01"), g.ID(), "do", []string{"a"})
	require.NoError(t, err)
	require.NoError(t, g.Start())
	require.ErrorIs(t, g.AddTask(tk), apply.ErrInvalidGroupTransition)
}

func TestTask_Release_RejectsPending(t *testing.T) {
	tk, _ := apply.NewTask(mustTaskID(t, "01ARZ3NDEKTSV4RRFFQ69G5T01"), mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G01"), "do", []string{"src"})
	require.ErrorIs(t, tk.Release(), apply.ErrInvalidTaskTransition)
}

func TestTask_MarkRunning_RejectsPending(t *testing.T) {
	tk, _ := apply.NewTask(mustTaskID(t, "01ARZ3NDEKTSV4RRFFQ69G5T01"), mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G01"), "do", []string{"src"})
	require.ErrorIs(t, tk.MarkRunning(), apply.ErrInvalidTaskTransition)
}

func TestTask_Complete_RejectsNilEnvelope(t *testing.T) {
	tk, _ := apply.NewTask(mustTaskID(t, "01ARZ3NDEKTSV4RRFFQ69G5T01"), mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G01"), "do", []string{"src"})
	sid := mustSessionID(t, "01ARZ3NDEKTSV4RRFFQ69G5S01")
	require.NoError(t, tk.Claim(sid))
	require.NoError(t, tk.MarkRunning())
	require.ErrorIs(t, tk.Complete(nil), apply.ErrInvalidTaskTransition)
}

func TestTask_Complete_FromRunning(t *testing.T) {
	tk, _ := apply.NewTask(
		mustTaskID(t, "01ARZ3NDEKTSV4RRFFQ69G5T01"),
		mustGroupID(t, "01ARZ3NDEKTSV4RRFFQ69G5G01"),
		"do x",
		[]string{"src/**/*.go"},
	)
	sid := mustSessionID(t, "01ARZ3NDEKTSV4RRFFQ69G5S01")
	require.NoError(t, tk.Claim(sid))
	require.NoError(t, tk.MarkRunning())
	env := &envelope.Envelope{SchemaVersion: envelope.SchemaVersionV1, Phase: "apply", ChangeName: "x", Project: "y", Status: envelope.StatusDone, Confidence: 0.85}
	require.NoError(t, tk.Complete(env))
	require.Equal(t, apply.TaskStatusDone, tk.Status())
	require.NotNil(t, tk.Envelope())
}
