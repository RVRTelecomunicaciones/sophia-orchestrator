package hybrid_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/artifact/hybrid"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

type fakeStore struct {
	mode       change.ArtifactStoreMode
	saveCalls  atomic.Int64
	loadCalls  atomic.Int64
	saveErr    error
	loadErr    error
	loaded     *outbound.Artifact
}

func (s *fakeStore) Mode() change.ArtifactStoreMode { return s.mode }
func (s *fakeStore) Save(_ context.Context, _ outbound.SaveArtifactInput) error {
	s.saveCalls.Add(1)
	return s.saveErr
}
func (s *fakeStore) Load(_ context.Context, _ string) (*outbound.Artifact, error) {
	s.loadCalls.Add(1)
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	return s.loaded, nil
}

func TestNew_PanicsOnNil(t *testing.T) {
	require.Panics(t, func() { _ = hybrid.New(nil, &fakeStore{}) })
	require.Panics(t, func() { _ = hybrid.New(&fakeStore{}, nil) })
}

func TestMode(t *testing.T) {
	s := hybrid.New(&fakeStore{}, &fakeStore{})
	require.Equal(t, change.ArtifactStoreHybrid, s.Mode())
}

func TestSave_WritesBothBackends(t *testing.T) {
	p := &fakeStore{}
	sec := &fakeStore{}
	s := hybrid.New(p, sec)
	require.NoError(t, s.Save(context.Background(), outbound.SaveArtifactInput{TopicKey: "k"}))
	require.Equal(t, int64(1), p.saveCalls.Load())
	require.Equal(t, int64(1), sec.saveCalls.Load())
}

func TestSave_PrimaryFailureShortCircuits(t *testing.T) {
	p := &fakeStore{saveErr: errors.New("primary boom")}
	sec := &fakeStore{}
	s := hybrid.New(p, sec)
	err := s.Save(context.Background(), outbound.SaveArtifactInput{TopicKey: "k"})
	require.Error(t, err)
	require.Equal(t, int64(0), sec.saveCalls.Load(), "secondary not called when primary fails")
}

func TestSave_SecondaryFailurePropagates(t *testing.T) {
	p := &fakeStore{}
	sec := &fakeStore{saveErr: errors.New("secondary boom")}
	s := hybrid.New(p, sec)
	err := s.Save(context.Background(), outbound.SaveArtifactInput{TopicKey: "k"})
	require.Error(t, err)
}

func TestLoad_PrimarySuccessSkipsSecondary(t *testing.T) {
	p := &fakeStore{loaded: &outbound.Artifact{TopicKey: "k", Content: []byte("primary")}}
	sec := &fakeStore{loaded: &outbound.Artifact{TopicKey: "k", Content: []byte("secondary")}}
	s := hybrid.New(p, sec)
	a, err := s.Load(context.Background(), "k")
	require.NoError(t, err)
	require.Equal(t, "primary", string(a.Content))
	require.Equal(t, int64(0), sec.loadCalls.Load(), "secondary not called when primary succeeds")
}

func TestLoad_PrimaryNotFoundFallsBack(t *testing.T) {
	p := &fakeStore{loadErr: outbound.ErrNotFound}
	sec := &fakeStore{loaded: &outbound.Artifact{TopicKey: "k", Content: []byte("secondary")}}
	s := hybrid.New(p, sec)
	a, err := s.Load(context.Background(), "k")
	require.NoError(t, err)
	require.Equal(t, "secondary", string(a.Content))
}

func TestLoad_PrimaryOtherErrorPropagates(t *testing.T) {
	p := &fakeStore{loadErr: errors.New("network down")}
	sec := &fakeStore{}
	s := hybrid.New(p, sec)
	_, err := s.Load(context.Background(), "k")
	require.Error(t, err)
	require.NotErrorIs(t, err, outbound.ErrNotFound)
	require.Equal(t, int64(0), sec.loadCalls.Load())
}

func TestLoad_BothNotFound(t *testing.T) {
	p := &fakeStore{loadErr: outbound.ErrNotFound}
	sec := &fakeStore{loadErr: outbound.ErrNotFound}
	s := hybrid.New(p, sec)
	_, err := s.Load(context.Background(), "k")
	require.ErrorIs(t, err, outbound.ErrNotFound)
}
