package http_base_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/http_base"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/trace"
	"github.com/stretchr/testify/require"
)

func newTracePropClient(t *testing.T, srv *httptest.Server, propagate bool) *http_base.Client {
	t.Helper()
	cfg := http_base.DefaultConfig("trace-test", srv.URL)
	cfg.HTTPTimeout = 200 * time.Millisecond
	cfg.MaxAttempts = 1
	cfg.JitterFraction = 0
	cfg.PropagateTrace = propagate
	c, err := http_base.New(cfg)
	require.NoError(t, err)
	return c
}

// Test 1: context with a Trace → outbound request carries Traceparent,
// same trace_id, new span_id.
func TestTracePropagation_SameTraceID_NewSpanID(t *testing.T) {
	var received http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	// Inject a known parent trace into the context.
	parentTrace, err := trace.Parse("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	require.NoError(t, err)
	ctx := trace.NewContext(t.Context(), parentTrace)

	c := newTracePropClient(t, srv, true)
	require.NoError(t, c.GetJSON(ctx, "/x", nil))

	tp := received.Get("Traceparent")
	require.NotEmpty(t, tp, "outbound request must carry Traceparent header")

	parts := strings.Split(tp, "-")
	require.Len(t, parts, 4, "Traceparent must have 4 segments")

	outTraceID := parts[1]
	outSpanID := parts[2]

	require.Equal(t, parentTrace.TraceID, outTraceID, "trace_id must be preserved through the child span")
	require.NotEqual(t, parentTrace.SpanID, outSpanID, "span_id must be rotated for the child span")
	require.Len(t, outSpanID, 16)
}

// Test 2: PropagateTrace=false → no Traceparent header added.
func TestTracePropagation_Disabled_NoHeader(t *testing.T) {
	var received http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	parentTrace, err := trace.Parse("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	require.NoError(t, err)
	ctx := trace.NewContext(t.Context(), parentTrace)

	c := newTracePropClient(t, srv, false)
	require.NoError(t, c.GetJSON(ctx, "/x", nil))

	tp := received.Get("Traceparent")
	require.Empty(t, tp, "Traceparent must not be added when PropagateTrace=false")
}

// Test 3: context without a Trace + PropagateTrace=true → a fresh trace is
// still emitted (so cold-start calls without an inbound traceparent remain
// traceable).
func TestTracePropagation_NoParentTrace_FreshGenerated(t *testing.T) {
	var received http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTracePropClient(t, srv, true)
	require.NoError(t, c.GetJSON(t.Context(), "/x", nil))

	tp := received.Get("Traceparent")
	require.NotEmpty(t, tp, "a fresh Traceparent must be generated even without a parent context trace")
	parsed, err := trace.Parse(tp)
	require.NoError(t, err)
	require.Len(t, parsed.TraceID, 32)
	require.Len(t, parsed.SpanID, 16)
}

// Test 4: DefaultConfig has PropagateTrace=true.
func TestDefaultConfig_PropagateTraceIsTrue(t *testing.T) {
	cfg := http_base.DefaultConfig("x", "http://localhost:9999")
	require.True(t, cfg.PropagateTrace)
}
