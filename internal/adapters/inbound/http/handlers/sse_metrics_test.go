package handlers_test

// sse_metrics_test.go — Commit 3 TDD: SSEConnectionsActive.
//
// SSEHandler.Stream must increment SSEConnectionsActive when the stream
// opens (after Subscribe succeeds) and decrement it when the client
// disconnects (via defer).

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/inbound/http/handlers"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	domainphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/obs"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
)

// --- test doubles ---

// blockingStream keeps a Subscribe channel open until the context is
// cancelled — this lets us observe SSEConnectionsActive > 0 mid-stream.
type blockingStream struct{}

func (s *blockingStream) Subscribe(ctx context.Context, _ ids.PhaseID) (<-chan inbound.Event, func(), error) {
	ch := make(chan inbound.Event)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, func() {}, nil
}

func (s *blockingStream) Publish(_ context.Context, _ ids.PhaseID, _ inbound.Event) error {
	return nil
}

// noopEventStore returns no history.
type noopEventStore struct{}

func (noopEventStore) Append(_ context.Context, _ ids.PhaseID, _ inbound.Event) (int64, error) {
	return 1, nil
}

func (noopEventStore) Replay(_ context.Context, _ ids.PhaseID, _ int64) ([]inbound.Event, error) {
	return nil, nil
}

// nonTerminalPhases always returns a non-terminal phase (PhaseSpec / Running).
type nonTerminalPhasesMet struct{}

func (nonTerminalPhasesMet) Get(_ context.Context, _ ids.PhaseID) (*domainphase.Phase, error) {
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	pid, _ := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5P01")
	p, err := domainphase.New(pid, cid, domainphase.PhaseApply, 3)
	return p, err
}

func newSSERequestMet(t *testing.T, phaseID ids.PhaseID) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/phases/%s/events", phaseID.String()), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("phase_id", phaseID.String())
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// TestSSE_SSEConnectionsActive_IncDecLifecycle verifies the gauge tracks
// open connections: it goes up after Subscribe and back down on disconnect.
func TestSSE_SSEConnectionsActive_IncDecLifecycle(t *testing.T) {
	m := obs.NewMetrics()

	h := handlers.NewSSEHandler(
		&blockingStream{},
		noopEventStore{},
		nonTerminalPhasesMet{},
		5*time.Second,
		func(w http.ResponseWriter, _ error) { w.WriteHeader(http.StatusInternalServerError) },
		func(w http.ResponseWriter, code int, _ any) { w.WriteHeader(code) },
		shared.FixedIDGenerator([]string{
			"01ARZ3NDEKTSV4RRFFQ69G5EV1",
			"01ARZ3NDEKTSV4RRFFQ69G5EV2",
			"01ARZ3NDEKTSV4RRFFQ69G5EV3",
		}),
		m, // metrics — not yet accepted; test will FAIL to compile until GREEN
	)

	phaseID, _ := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5P01")
	req := newSSERequestMet(t, phaseID)

	// Cancel to simulate client disconnect.
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()

	// Run Stream in a goroutine — it blocks until ctx is cancelled.
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.Stream(w, req)
	}()

	// Poll until the gauge increments (Subscribe has been called).
	require.Eventually(t, func() bool {
		return testutil.ToFloat64(m.SSEConnectionsActive) >= 1
	}, 500*time.Millisecond, 10*time.Millisecond, "SSEConnectionsActive should be 1 after stream opens")

	// Simulate client disconnect.
	cancel()
	<-done

	afterClose := testutil.ToFloat64(m.SSEConnectionsActive)
	require.Equal(t, float64(0), afterClose, "SSEConnectionsActive should decrement to 0 after stream closes")
}
