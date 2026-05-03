package change_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	appchange "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/change"
	domainchange "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// fakeRepo is a hand-rolled in-memory ChangeRepository. We don't use a
// mocking library because the API is small and Go convention favors
// hand-written fakes for clarity.
type fakeRepo struct {
	mu      sync.Mutex
	byID    map[string]*domainchange.Change
	byPair  map[string]*domainchange.Change // "project|name" -> change
	saveErr error
	findErr error
	listErr error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		byID:   map[string]*domainchange.Change{},
		byPair: map[string]*domainchange.Change{},
	}
}

func (r *fakeRepo) Save(_ context.Context, c *domainchange.Change) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.saveErr != nil {
		return r.saveErr
	}
	r.byID[c.ID().String()] = c
	r.byPair[c.Project()+"|"+c.Name()] = c
	return nil
}

func (r *fakeRepo) FindByID(_ context.Context, id ids.ChangeID) (*domainchange.Change, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.findErr != nil {
		return nil, r.findErr
	}
	c, ok := r.byID[id.String()]
	if !ok {
		return nil, outbound.ErrNotFound
	}
	return c, nil
}

func (r *fakeRepo) FindByProjectName(_ context.Context, project, name string) (*domainchange.Change, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.findErr != nil {
		return nil, r.findErr
	}
	c, ok := r.byPair[project+"|"+name]
	if !ok {
		return nil, outbound.ErrNotFound
	}
	return c, nil
}

func (r *fakeRepo) List(_ context.Context, project, _ string, limit, _ int) ([]*domainchange.Change, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.listErr != nil {
		return nil, r.listErr
	}
	out := make([]*domainchange.Change, 0)
	for _, c := range r.byID {
		if project != "" && c.Project() != project {
			continue
		}
		out = append(out, c)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func newSvc(t *testing.T) (*appchange.Service, *fakeRepo) {
	t.Helper()
	repo := newFakeRepo()
	clk := shared.FixedClock(time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC))
	idGen := shared.FixedIDGenerator([]string{
		"01ARZ3NDEKTSV4RRFFQ69G5C01",
		"01ARZ3NDEKTSV4RRFFQ69G5C02",
		"01ARZ3NDEKTSV4RRFFQ69G5C03",
	})
	return appchange.New(repo, clk, idGen), repo
}

func TestNew_PanicsOnNilDeps(t *testing.T) {
	require.Panics(t, func() {
		appchange.New(nil, shared.SystemClock{}, shared.FixedIDGenerator(nil))
	})
}

func TestCreate_Valid(t *testing.T) {
	svc, _ := newSvc(t)
	c, err := svc.Create(context.Background(), inbound.CreateChangeInput{
		Name:              "feat-x",
		Project:           "proj",
		ArtifactStoreMode: domainchange.ArtifactStoreMemoryEngine,
		BaseRef:           "main",
	})
	require.NoError(t, err)
	require.Equal(t, "feat-x", c.Name())
	require.Equal(t, "proj", c.Project())
	require.Equal(t, domainchange.StatusActive, c.Status())
}

func TestCreate_RejectsAlreadyExists(t *testing.T) {
	svc, _ := newSvc(t)
	_, err := svc.Create(context.Background(), inbound.CreateChangeInput{
		Name: "feat-x", Project: "proj", ArtifactStoreMode: domainchange.ArtifactStoreMemoryEngine,
	})
	require.NoError(t, err)

	_, err = svc.Create(context.Background(), inbound.CreateChangeInput{
		Name: "feat-x", Project: "proj", ArtifactStoreMode: domainchange.ArtifactStoreMemoryEngine,
	})
	require.ErrorIs(t, err, appchange.ErrAlreadyExists)
}

func TestCreate_PropagatesDomainValidationError(t *testing.T) {
	svc, _ := newSvc(t)
	_, err := svc.Create(context.Background(), inbound.CreateChangeInput{
		Name: "", Project: "proj", ArtifactStoreMode: domainchange.ArtifactStoreMemoryEngine,
	})
	require.ErrorIs(t, err, domainchange.ErrEmptyName)
}

func TestCreate_RejectsInvalidArtifactStore(t *testing.T) {
	svc, _ := newSvc(t)
	_, err := svc.Create(context.Background(), inbound.CreateChangeInput{
		Name: "feat-x", Project: "proj", ArtifactStoreMode: domainchange.ArtifactStoreMode("nope"),
	})
	require.ErrorIs(t, err, domainchange.ErrInvalidArtifactStore)
}

func TestCreate_RejectsInvalidIDFromGenerator(t *testing.T) {
	repo := newFakeRepo()
	clk := shared.FixedClock(time.Now())
	idGen := shared.FixedIDGenerator([]string{"not-a-ulid"})
	svc := appchange.New(repo, clk, idGen)
	_, err := svc.Create(context.Background(), inbound.CreateChangeInput{
		Name: "feat-x", Project: "proj", ArtifactStoreMode: domainchange.ArtifactStoreMemoryEngine,
	})
	require.ErrorIs(t, err, ids.ErrInvalidID)
}

func TestCreate_PropagatesRepoSaveError(t *testing.T) {
	repo := newFakeRepo()
	repo.saveErr = errors.New("boom")
	clk := shared.FixedClock(time.Now())
	idGen := shared.FixedIDGenerator([]string{"01ARZ3NDEKTSV4RRFFQ69G5C01"})
	svc := appchange.New(repo, clk, idGen)
	_, err := svc.Create(context.Background(), inbound.CreateChangeInput{
		Name: "x", Project: "y", ArtifactStoreMode: domainchange.ArtifactStoreMemoryEngine,
	})
	require.Error(t, err)
}

func TestCreate_PropagatesUnexpectedFindError(t *testing.T) {
	repo := newFakeRepo()
	repo.findErr = errors.New("db down")
	clk := shared.FixedClock(time.Now())
	idGen := shared.FixedIDGenerator([]string{"01ARZ3NDEKTSV4RRFFQ69G5C01"})
	svc := appchange.New(repo, clk, idGen)
	_, err := svc.Create(context.Background(), inbound.CreateChangeInput{
		Name: "x", Project: "y", ArtifactStoreMode: domainchange.ArtifactStoreMemoryEngine,
	})
	require.Error(t, err)
}

func TestGet_Found(t *testing.T) {
	svc, _ := newSvc(t)
	c, _ := svc.Create(context.Background(), inbound.CreateChangeInput{
		Name: "feat-x", Project: "proj", ArtifactStoreMode: domainchange.ArtifactStoreMemoryEngine,
	})
	got, err := svc.Get(context.Background(), c.ID())
	require.NoError(t, err)
	require.Equal(t, c.ID(), got.ID())
}

func TestGet_NotFound(t *testing.T) {
	svc, _ := newSvc(t)
	id, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5MIS")
	_, err := svc.Get(context.Background(), id)
	require.ErrorIs(t, err, outbound.ErrNotFound)
}

func TestList_DefaultsLimit(t *testing.T) {
	svc, _ := newSvc(t)
	_, _ = svc.Create(context.Background(), inbound.CreateChangeInput{
		Name: "a", Project: "proj", ArtifactStoreMode: domainchange.ArtifactStoreMemoryEngine,
	})
	_, _ = svc.Create(context.Background(), inbound.CreateChangeInput{
		Name: "b", Project: "proj", ArtifactStoreMode: domainchange.ArtifactStoreMemoryEngine,
	})
	res, err := svc.List(context.Background(), "proj", "", 0, -5)
	require.NoError(t, err)
	require.Len(t, res, 2)
}

func TestList_PropagatesError(t *testing.T) {
	repo := newFakeRepo()
	repo.listErr = errors.New("boom")
	clk := shared.FixedClock(time.Now())
	idGen := shared.FixedIDGenerator(nil)
	svc := appchange.New(repo, clk, idGen)
	_, err := svc.List(context.Background(), "proj", "", 10, 0)
	require.Error(t, err)
}

func TestAbort_Valid(t *testing.T) {
	svc, _ := newSvc(t)
	c, _ := svc.Create(context.Background(), inbound.CreateChangeInput{
		Name: "feat-x", Project: "proj", ArtifactStoreMode: domainchange.ArtifactStoreMemoryEngine,
	})
	require.NoError(t, svc.Abort(context.Background(), c.ID(), "user request"))
	got, _ := svc.Get(context.Background(), c.ID())
	require.Equal(t, domainchange.StatusAborted, got.Status())
}

func TestAbort_NotFound(t *testing.T) {
	svc, _ := newSvc(t)
	id, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5MIS")
	err := svc.Abort(context.Background(), id, "reason")
	require.ErrorIs(t, err, outbound.ErrNotFound)
}

func TestAbort_AlreadyTerminal(t *testing.T) {
	svc, _ := newSvc(t)
	c, _ := svc.Create(context.Background(), inbound.CreateChangeInput{
		Name: "feat-x", Project: "proj", ArtifactStoreMode: domainchange.ArtifactStoreMemoryEngine,
	})
	require.NoError(t, svc.Abort(context.Background(), c.ID(), "first"))
	err := svc.Abort(context.Background(), c.ID(), "second")
	require.ErrorIs(t, err, domainchange.ErrAlreadyTerminal)
}

func TestAbort_PropagatesSaveError(t *testing.T) {
	repo := newFakeRepo()
	clk := shared.FixedClock(time.Now())
	idGen := shared.FixedIDGenerator([]string{"01ARZ3NDEKTSV4RRFFQ69G5C01"})
	svc := appchange.New(repo, clk, idGen)
	_, err := svc.Create(context.Background(), inbound.CreateChangeInput{
		Name: "x", Project: "y", ArtifactStoreMode: domainchange.ArtifactStoreMemoryEngine,
	})
	require.NoError(t, err)
	id, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")

	repo.saveErr = errors.New("boom")
	err = svc.Abort(context.Background(), id, "reason")
	require.Error(t, err)
}
