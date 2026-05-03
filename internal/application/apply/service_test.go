package apply_test

import (
	"context"
	"errors"
	"testing"

	appapply "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/apply"
	domainapply "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

type fakeRepo struct {
	board   *domainapply.Board
	findErr error
}

func (r *fakeRepo) SaveBoard(_ context.Context, _ *domainapply.Board) error    { return nil }
func (r *fakeRepo) FindBoardByPhaseID(_ context.Context, _ ids.PhaseID) (*domainapply.Board, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	if r.board == nil {
		return nil, outbound.ErrNotFound
	}
	return r.board, nil
}
func (r *fakeRepo) SaveGroup(_ context.Context, _ *domainapply.Group) error                 { return nil }
func (r *fakeRepo) SaveTask(_ context.Context, _ *domainapply.Task) error                   { return nil }
func (r *fakeRepo) FindTaskByID(_ context.Context, _ ids.TaskID) (*domainapply.Task, error) { return nil, nil }
func (r *fakeRepo) ClaimTask(_ context.Context, _ ids.TaskID, _ ids.SessionID) (bool, error) {
	return false, nil
}

func mkBoardID(t *testing.T) ids.BoardID {
	t.Helper()
	id, err := ids.ParseBoardID("01ARZ3NDEKTSV4RRFFQ69G5B01")
	require.NoError(t, err)
	return id
}

func mkPhaseID(t *testing.T) ids.PhaseID {
	t.Helper()
	id, err := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5P01")
	require.NoError(t, err)
	return id
}

func TestNew_PanicsOnNilRepo(t *testing.T) {
	require.Panics(t, func() { appapply.New(nil) })
}

func TestGetBoard_Found(t *testing.T) {
	board := domainapply.NewBoard(mkBoardID(t), mkPhaseID(t))
	svc := appapply.New(&fakeRepo{board: board})
	got, err := svc.GetBoard(context.Background(), mkPhaseID(t))
	require.NoError(t, err)
	require.Equal(t, board.ID(), got.ID())
}

func TestGetBoard_NotFound(t *testing.T) {
	svc := appapply.New(&fakeRepo{})
	_, err := svc.GetBoard(context.Background(), mkPhaseID(t))
	require.ErrorIs(t, err, outbound.ErrNotFound)
}

func TestGetBoard_PropagatesError(t *testing.T) {
	svc := appapply.New(&fakeRepo{findErr: errors.New("boom")})
	_, err := svc.GetBoard(context.Background(), mkPhaseID(t))
	require.Error(t, err)
}
