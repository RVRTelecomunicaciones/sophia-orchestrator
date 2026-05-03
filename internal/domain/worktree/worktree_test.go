package worktree_test

import (
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/worktree"
	"github.com/stretchr/testify/require"
)

func mkWID(t *testing.T) ids.WorktreeID {
	t.Helper()
	id, err := ids.ParseWorktreeID("01ARZ3NDEKTSV4RRFFQ69G5W01")
	require.NoError(t, err)
	return id
}

func now() time.Time {
	return time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
}

func TestStatus_IsValid(t *testing.T) {
	for _, s := range []worktree.Status{
		worktree.StatusCreated, worktree.StatusLocked,
		worktree.StatusReleased, worktree.StatusCleaned,
	} {
		require.True(t, s.IsValid(), "status %q must be valid", s)
	}
	require.False(t, worktree.Status("nope").IsValid())
}

func TestNew_Valid(t *testing.T) {
	w, err := worktree.New(mkWID(t), nil, "/var/sophia/wt/g1", "sophia/feat-x/group-1", now())
	require.NoError(t, err)
	require.Equal(t, worktree.StatusCreated, w.Status())
	require.Equal(t, "/var/sophia/wt/g1", w.Path())
	require.Equal(t, "sophia/feat-x/group-1", w.Branch())
}

func TestNew_RejectsEmptyPath(t *testing.T) {
	_, err := worktree.New(mkWID(t), nil, "", "branch", now())
	require.ErrorIs(t, err, worktree.ErrEmptyPath)
}

func TestNew_RejectsEmptyBranch(t *testing.T) {
	_, err := worktree.New(mkWID(t), nil, "/p", "", now())
	require.ErrorIs(t, err, worktree.ErrEmptyBranch)
}

func TestLifecycle_FullPath(t *testing.T) {
	w, _ := worktree.New(mkWID(t), nil, "/p", "b", now())
	require.NoError(t, w.Lock())
	require.Equal(t, worktree.StatusLocked, w.Status())
	require.NoError(t, w.Release())
	require.Equal(t, worktree.StatusReleased, w.Status())
	require.NoError(t, w.Clean(now()))
	require.Equal(t, worktree.StatusCleaned, w.Status())
	require.NotNil(t, w.CleanedAt())
}

func TestRelease_FromCreatedDirectly(t *testing.T) {
	w, _ := worktree.New(mkWID(t), nil, "/p", "b", now())
	// Direct release without lock: allowed (used when worktree creation fails partway).
	require.NoError(t, w.Release())
	require.Equal(t, worktree.StatusReleased, w.Status())
}

func TestLock_RejectsAfterClean(t *testing.T) {
	w, _ := worktree.New(mkWID(t), nil, "/p", "b", now())
	require.NoError(t, w.Clean(now()))
	require.ErrorIs(t, w.Lock(), worktree.ErrInvalidTransition)
}

func TestClean_Idempotent(t *testing.T) {
	w, _ := worktree.New(mkWID(t), nil, "/p", "b", now())
	require.NoError(t, w.Clean(now()))
	require.ErrorIs(t, w.Clean(now()), worktree.ErrInvalidTransition)
}

func TestRelease_RejectsAfterClean(t *testing.T) {
	w, _ := worktree.New(mkWID(t), nil, "/p", "b", now())
	require.NoError(t, w.Clean(now()))
	require.ErrorIs(t, w.Release(), worktree.ErrInvalidTransition)
}

func TestRelease_RejectsAfterRelease(t *testing.T) {
	w, _ := worktree.New(mkWID(t), nil, "/p", "b", now())
	require.NoError(t, w.Release())
	require.ErrorIs(t, w.Release(), worktree.ErrInvalidTransition)
}

func TestHydrate_Roundtrip(t *testing.T) {
	cleanedAt := now().Add(time.Minute)
	w := worktree.Hydrate(mkWID(t), nil, "/p", "b", worktree.StatusCleaned, now(), &cleanedAt)
	require.Equal(t, worktree.StatusCleaned, w.Status())
	require.NotNil(t, w.CleanedAt())
}
