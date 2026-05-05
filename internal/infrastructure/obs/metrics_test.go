package obs_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/obs"
	"github.com/stretchr/testify/require"
)

func TestNewMetrics_RegistersAllInstruments(t *testing.T) {
	m := obs.NewMetrics()
	require.NotNil(t, m)
	require.NotNil(t, m.Handler())
}

func TestHandler_ExposesPrometheusFormat(t *testing.T) {
	m := obs.NewMetrics()

	// Touch a few metrics to ensure they're emitted in /metrics output.
	m.PhasesTotal.WithLabelValues("spec", "done").Inc()
	m.IronLawViolationsTotal.WithLabelValues("IL5_NO_FIX_4_WITHOUT_ESCALATION").Inc()
	m.SpawnGovernorActive.Set(2)

	srv := httptest.NewServer(m.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	out := string(body)

	// Verify the metrics we touched appear with their values.
	require.True(t, strings.Contains(out, `sophia_orchestator_phases_total{phase_type="spec",status="done"} 1`),
		"expected phases_total counter in /metrics output, got:\n%s", out)
	require.Contains(t, out, `sophia_orchestator_iron_law_violations_total{law_id="IL5_NO_FIX_4_WITHOUT_ESCALATION"} 1`)
	require.Contains(t, out, `sophia_orchestator_spawn_governor_active 2`)
}

func TestHandler_ServesUnderRouteIntegration(t *testing.T) {
	m := obs.NewMetrics()
	mux := http.NewServeMux()
	mux.Handle("/metrics", m.Handler())
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/plain")
}
