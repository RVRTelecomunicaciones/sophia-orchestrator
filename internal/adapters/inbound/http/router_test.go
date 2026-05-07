package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	httpinbound "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/inbound/http"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/inbound/http/middleware"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// --- fakes ---

type fakeChanges struct {
	created *change.Change
	getErr  error
	created2 *change.Change
}

func (s *fakeChanges) Create(_ context.Context, in inbound.CreateChangeInput) (*change.Change, error) {
	if s.created != nil {
		return s.created, nil
	}
	id, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	c, _ := change.New(id, in.Name, in.Project, in.ArtifactStoreMode, in.BaseRef, time.Now())
	s.created2 = c
	return c, nil
}
func (s *fakeChanges) Get(_ context.Context, _ ids.ChangeID) (*change.Change, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.created2, nil
}
func (s *fakeChanges) List(_ context.Context, _, _ string, _, _ int) ([]*change.Change, error) {
	return []*change.Change{}, nil
}
func (s *fakeChanges) Abort(_ context.Context, _ ids.ChangeID, _ string) error { return nil }

type fakePhases struct{}

func (s *fakePhases) Run(_ context.Context, in inbound.RunPhaseInput) (*inbound.RunPhaseOutput, error) {
	pid, _ := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5P01")
	return &inbound.RunPhaseOutput{
		PhaseID: pid, Status: phase.PhaseStatusRunning,
		EventsURL: "/api/v1/changes/" + in.ChangeID.String() + "/phases/" + pid.String() + "/events",
		StartedAt: time.Now().Format(time.RFC3339),
	}, nil
}
func (s *fakePhases) Get(_ context.Context, _ ids.PhaseID) (*phase.Phase, error) {
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	pid, _ := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5P01")
	p, _ := phase.New(pid, cid, phase.PhaseSpec, 3)
	return p, nil
}
func (s *fakePhases) Resume(ctx context.Context, id ids.PhaseID) (*inbound.RunPhaseOutput, error) {
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	return s.Run(ctx, inbound.RunPhaseInput{ChangeID: cid, PhaseType: phase.PhaseSpec})
}
func (s *fakePhases) Approve(_ context.Context, _ ids.PhaseID, _, _ string) error { return nil }
func (s *fakePhases) Reject(_ context.Context, _ ids.PhaseID, _, _ string) error  { return nil }

type fakeApply struct{}

func (s *fakeApply) GetBoard(_ context.Context, pid ids.PhaseID) (*apply.Board, error) {
	bid, _ := ids.ParseBoardID("01ARZ3NDEKTSV4RRFFQ69G5B01")
	return apply.NewBoard(bid, pid), nil
}

type fakeEvents struct {
	mu       sync.Mutex
	channels []chan inbound.Event
}

func (s *fakeEvents) Subscribe(_ context.Context, _ ids.PhaseID) (<-chan inbound.Event, func(), error) {
	ch := make(chan inbound.Event, 4)
	s.mu.Lock()
	s.channels = append(s.channels, ch)
	s.mu.Unlock()
	return ch, func() { close(ch) }, nil
}

func (s *fakeEvents) Publish(_ context.Context, _ ids.PhaseID, ev inbound.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.channels {
		select {
		case c <- ev:
		default:
		}
	}
	return nil
}

type fakeAuthn struct {
	rejectAll bool
}

func (a *fakeAuthn) Validate(_ middleware.ContextProvider, key string) (string, error) {
	if a.rejectAll || key == "" {
		return "", errors.New("invalid")
	}
	return "test-project", nil
}

func newSrv(t *testing.T, deps httpinbound.Deps) *httptest.Server {
	t.Helper()
	r := httpinbound.NewRouter(deps)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func defaultDeps() httpinbound.Deps {
	return httpinbound.Deps{
		Changes:   &fakeChanges{},
		Phases:    &fakePhases{},
		Apply:     &fakeApply{},
		Events:    &fakeEvents{},
		Auth:      &fakeAuthn{},
		StartedAt: time.Now(),
		Ready:     func() error { return nil },
	}
}

func TestHealth_Public(t *testing.T) {
	srv := newSrv(t, defaultDeps())
	resp, err := http.Get(srv.URL + "/api/v1/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestReady_OK(t *testing.T) {
	srv := newSrv(t, defaultDeps())
	resp, err := http.Get(srv.URL + "/api/v1/ready")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestReady_NotReady(t *testing.T) {
	deps := defaultDeps()
	deps.Ready = func() error { return errors.New("db down") }
	srv := newSrv(t, deps)
	resp, _ := http.Get(srv.URL + "/api/v1/ready")
	defer resp.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

func TestAuth_RequiredOnProtectedEndpoints(t *testing.T) {
	srv := newSrv(t, defaultDeps())
	resp, _ := http.Get(srv.URL + "/api/v1/changes")
	defer resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestAuth_AcceptsValidKey(t *testing.T) {
	srv := newSrv(t, defaultDeps())
	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/changes", nil)
	req.Header.Set("X-Sophia-API-Key", "valid-key")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestCreateChange_Roundtrip(t *testing.T) {
	srv := newSrv(t, defaultDeps())
	body := `{"name":"feat-x","project":"proj","artifact_store_mode":"memory-engine","base_ref":"main"}`
	req, _ := http.NewRequest("POST", srv.URL+"/api/v1/changes", strings.NewReader(body))
	req.Header.Set("X-Sophia-API-Key", "valid")
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var got map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Equal(t, "feat-x", got["name"])
	require.Equal(t, "proj", got["project"])
	require.Equal(t, "memory-engine", got["artifact_store_mode"])
}

func TestCreateChange_BadBody(t *testing.T) {
	srv := newSrv(t, defaultDeps())
	req, _ := http.NewRequest("POST", srv.URL+"/api/v1/changes", strings.NewReader("not json"))
	req.Header.Set("X-Sophia-API-Key", "valid")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestRunPhase_Returns202(t *testing.T) {
	srv := newSrv(t, defaultDeps())
	req, _ := http.NewRequest("POST",
		srv.URL+"/api/v1/changes/01ARZ3NDEKTSV4RRFFQ69G5C01/phases/spec/run",
		strings.NewReader(`{"task_description":"draft"}`))
	req.Header.Set("X-Sophia-API-Key", "valid")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	var got map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.NotEmpty(t, got["phase_id"])
	require.NotEmpty(t, got["events_url"])
}

func TestRunPhase_RejectsInvalidPhaseType(t *testing.T) {
	srv := newSrv(t, defaultDeps())
	req, _ := http.NewRequest("POST",
		srv.URL+"/api/v1/changes/01ARZ3NDEKTSV4RRFFQ69G5C01/phases/nonsense/run",
		strings.NewReader(`{}`))
	req.Header.Set("X-Sophia-API-Key", "valid")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestSSE_StreamReceivesEvents(t *testing.T) {
	deps := defaultDeps()
	events := deps.Events.(*fakeEvents)
	srv := newSrv(t, deps)

	// Subscribe in a goroutine via HTTP.
	req, _ := http.NewRequest("GET",
		srv.URL+"/api/v1/phases/01ARZ3NDEKTSV4RRFFQ69G5P01/events",
		nil)
	req.Header.Set("X-Sophia-API-Key", "valid")
	req.Header.Set("Accept", "text/event-stream")
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	// Publish an event after a tiny delay so the subscription is in place.
	time.Sleep(30 * time.Millisecond)
	pid, _ := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5P01")
	_ = events.Publish(context.Background(), pid, inbound.Event{
		Type: "phase.started", Timestamp: time.Now(), Payload: map[string]any{"phase": "spec"},
	})

	// Read up to the first non-open event.
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])
	require.Contains(t, body, "event: open")
}

func TestNotFound_JSON(t *testing.T) {
	srv := newSrv(t, defaultDeps())
	resp, _ := http.Get(srv.URL + "/no/such/route")
	defer resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
}

// avoid unused import for outbound
var _ = outbound.ErrNotFound
