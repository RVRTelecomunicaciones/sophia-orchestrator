package runtime_test

// metrics_test.go — Commit 3 TDD: RuntimeCallsTotal.
//
// Client.Execute must increment RuntimeCallsTotal{capability, status}
// after each successful or failed call.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/runtime"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/obs"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

func newClientWithMetrics(t *testing.T, srv *httptest.Server, m *obs.Metrics) *runtime.Client {
	t.Helper()
	cfg := runtime.DefaultConfig(srv.URL, "test-key")
	cfg.HTTPBase.MaxAttempts = 1
	cfg.Clock = shared.FixedClock(time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC))
	cfg.Metrics = m // not yet in Config; test will fail to compile until GREEN
	c, err := runtime.New(cfg)
	require.NoError(t, err)
	return c
}

func TestMetrics_RuntimeCallsTotal_SuccessIncrements(t *testing.T) {
	m := obs.NewMetrics()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(successReceipt(t, []byte("hi"), nil, 0))
	}))
	t.Cleanup(srv.Close)

	c := newClientWithMetrics(t, srv, m)

	before := testutil.ToFloat64(m.RuntimeCallsTotal.WithLabelValues("shell.exec@v1", "ok"))
	_, err := c.Execute(context.Background(), outbound.ExecutionRequest{
		Capability: "shell.exec@v1",
		Payload:    []byte(`{"cmd":"echo hi"}`),
		TimeoutMS:  5000,
	})
	require.NoError(t, err)
	after := testutil.ToFloat64(m.RuntimeCallsTotal.WithLabelValues("shell.exec@v1", "ok"))

	require.Equal(t, before+1, after, "RuntimeCallsTotal{capability=shell.exec@v1, status=ok} must increment on success")
}

func TestMetrics_RuntimeCallsTotal_ErrorIncrements(t *testing.T) {
	m := obs.NewMetrics()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	c := newClientWithMetrics(t, srv, m)

	before := testutil.ToFloat64(m.RuntimeCallsTotal.WithLabelValues("shell.exec@v1", "error"))
	_, err := c.Execute(context.Background(), outbound.ExecutionRequest{
		Capability: "shell.exec@v1",
		Payload:    []byte(`{"cmd":"fail"}`),
		TimeoutMS:  5000,
	})
	require.Error(t, err)
	after := testutil.ToFloat64(m.RuntimeCallsTotal.WithLabelValues("shell.exec@v1", "error"))

	require.Equal(t, before+1, after, "RuntimeCallsTotal{capability=shell.exec@v1, status=error} must increment on error")
}
