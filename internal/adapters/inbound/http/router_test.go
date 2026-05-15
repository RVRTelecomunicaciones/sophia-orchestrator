package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	httpinbound "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/inbound/http"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/inbound/http/middleware"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/eventstream"
	phaseapp "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
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

func (s *fakePhases) Run(_ context.Context, _ inbound.RunPhaseInput) (*inbound.RunPhaseOutput, error) {
	pid, _ := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5P01")
	return &inbound.RunPhaseOutput{
		PhaseID: pid, Status: phase.PhaseStatusRunning,
		EventsURL: "/api/v1/phases/" + pid.String() + "/events",
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
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			for i, c := range s.channels {
				if c == ch {
					s.channels = append(s.channels[:i], s.channels[i+1:]...)
					break
				}
			}
			close(ch)
		})
	}
	return ch, cancel, nil
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
		Changes:    &fakeChanges{},
		Phases:     &fakePhases{},
		Apply:      &fakeApply{},
		Events:     &fakeEvents{},
		EventStore: &eventstream.NoopEventStore{}, // no replay history in unit tests
		Auth:       &fakeAuthn{},
		StartedAt:  time.Now(),
		Ready:      func() error { return nil },
		IDGen: shared.FixedIDGenerator([]string{
			"01ARZ3NDEKTSV4RRFFQ69G5EV1",
			"01ARZ3NDEKTSV4RRFFQ69G5EV2",
			"01ARZ3NDEKTSV4RRFFQ69G5EV3",
			"01ARZ3NDEKTSV4RRFFQ69G5EV4",
			"01ARZ3NDEKTSV4RRFFQ69G5EV5",
			"01ARZ3NDEKTSV4RRFFQ69G5EV6",
			"01ARZ3NDEKTSV4RRFFQ69G5EV7",
			"01ARZ3NDEKTSV4RRFFQ69G5EV8",
		}),
	}
}

func TestHealth_Public(t *testing.T) {
	srv := newSrv(t, defaultDeps())
	resp, err := http.Get(srv.URL + "/api/v1/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestReady_OK asserts the happy path returns 200 + the canonical envelope
// {"status":"ready","checks":{"db":"ok"}} per ADR-0005 P1.4.
func TestReady_OK(t *testing.T) {
	srv := newSrv(t, defaultDeps())
	start := time.Now()
	resp, err := http.Get(srv.URL + "/api/v1/ready")
	elapsed := time.Since(start)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	// Latency budget per ADR-0005 P1.4 (happy path < 100ms). The probe is
	// a no-op fake, so anything above 100ms here would be loopback/scheduler
	// noise rather than a real regression; still useful as a smoke check.
	require.Less(t, elapsed, 100*time.Millisecond, "ready happy path must be <100ms")

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, "ready", body["status"])
	checks, ok := body["checks"].(map[string]any)
	require.True(t, ok, "checks must be a JSON object")
	require.Equal(t, "ok", checks["db"])
}

// TestReady_NotReady asserts 503 + degraded envelope including the underlying
// error string under checks.db when the readiness probe fails.
func TestReady_NotReady(t *testing.T) {
	deps := defaultDeps()
	deps.Ready = func() error { return errors.New("db down") }
	srv := newSrv(t, deps)
	resp, _ := http.Get(srv.URL + "/api/v1/ready")
	defer resp.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, "degraded", body["status"])
	checks, ok := body["checks"].(map[string]any)
	require.True(t, ok, "checks must be a JSON object")
	require.Equal(t, "db down", checks["db"])
}

// TestReady_ContextTimeout asserts that a probe which exceeds its own
// deadline surfaces as 503 + the timeout error string. The orchestrator's
// readinessFor() in bootstrap caps the probe at 2s with context.WithTimeout;
// here we simulate that contract by returning a context.DeadlineExceeded.
func TestReady_ContextTimeout(t *testing.T) {
	deps := defaultDeps()
	deps.Ready = func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
		defer cancel()
		// Force the deadline to elapse before observing it.
		time.Sleep(5 * time.Millisecond)
		// Simulate "pg ping timeout" wrapping context.DeadlineExceeded.
		if err := ctx.Err(); err != nil {
			return errors.New("readiness: pg: " + err.Error())
		}
		return nil
	}
	srv := newSrv(t, deps)
	resp, _ := http.Get(srv.URL + "/api/v1/ready")
	defer resp.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, "degraded", body["status"])
	checks := body["checks"].(map[string]any)
	require.Contains(t, checks["db"], "context deadline exceeded")
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

// TestEventsURL_WireContract guards against drift between
// phase.DefaultServiceConfig().EventsURLTemplate and the SSE route
// registered in router.go. Reproduces the 2026-05-14 bug where the
// template produced "/api/v1/changes/{cid}/phases/{pid}/events" while
// the router only knew "/api/v1/phases/{pid}/events" — every client
// that followed the events_url field received 404.
func TestEventsURL_WireContract(t *testing.T) {
	deps := defaultDeps()
	deps.Phases = &fakePhasesTerminal{}
	srv := newSrv(t, deps)

	pid := "01ARZ3NDEKTSV4RRFFQ69G5P01"
	eventsURL := fmt.Sprintf(phaseapp.DefaultServiceConfig().EventsURLTemplate, pid)

	req, _ := http.NewRequest("GET", srv.URL+eventsURL, nil)
	req.Header.Set("X-Sophia-API-Key", "valid")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.NotEqual(t, http.StatusNotFound, resp.StatusCode,
		"events_url %q must hit a registered SSE route; check EventsURLTemplate vs router.go", eventsURL)
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

// --- Phase 3.8: error envelope + new error code coverage ---

// TestErrorEnvelope_Shape asserts the canonical envelope for any error
// is {code, error, details?} per sophia-wire-v1 §9.1.
func TestErrorEnvelope_Shape(t *testing.T) {
	srv := newSrv(t, defaultDeps())
	resp, err := http.Get(srv.URL + "/api/v1/changes")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var env map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&env))
	require.NotEmpty(t, env["code"], "error envelope MUST include `code`")
	require.NotEmpty(t, env["error"], "error envelope MUST include `error`")
}

// TestList_LimitTooLarge asserts the changes-list endpoint returns
// 400 + limit_too_large when limit > 100 (sophia-wire-v1 §9.2).
func TestList_LimitTooLarge(t *testing.T) {
	srv := newSrv(t, defaultDeps())
	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/changes?limit=500", nil)
	req.Header.Set("X-Sophia-API-Key", "valid")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var env map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&env))
	require.Equal(t, "limit_too_large", env["code"])
	require.Contains(t, env, "details")
}

// fakePhasesTerminal returns a phase already in PhaseStatusBlocked, so
// the SSE handler short-circuits with 410 + phase_terminal_no_events.
type fakePhasesTerminal struct{ fakePhases }

func (s *fakePhasesTerminal) Get(_ context.Context, _ ids.PhaseID) (*phase.Phase, error) {
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	pid, _ := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5P01")
	// Hydrate directly into PhaseStatusBlocked (terminal).
	return phase.Hydrate(pid, cid, phase.PhaseSpec, phase.PhaseStatusBlocked,
		nil, 0, 1, 1, nil, nil), nil
}

// TestSSE_PhaseTerminalNoEvents asserts attaching to a terminal phase
// returns 410 + phase_terminal_no_events (sophia-wire-v1 §9.2).
func TestSSE_PhaseTerminalNoEvents(t *testing.T) {
	deps := defaultDeps()
	deps.Phases = &fakePhasesTerminal{}
	srv := newSrv(t, deps)

	req, _ := http.NewRequest("GET",
		srv.URL+"/api/v1/phases/01ARZ3NDEKTSV4RRFFQ69G5P01/events", nil)
	req.Header.Set("X-Sophia-API-Key", "valid")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusGone, resp.StatusCode)
	var env map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&env))
	require.Equal(t, "phase_terminal_no_events", env["code"])
}
