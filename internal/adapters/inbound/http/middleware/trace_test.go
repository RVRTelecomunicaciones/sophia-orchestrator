package middleware_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/inbound/http/middleware"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/trace"
	"github.com/stretchr/testify/require"
)

// fixedReader returns a deterministic io.Reader that cycles the supplied bytes.
// It produces enough bytes for up to 3 trace/span ID generations (24 bytes each).
func fixedReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

func seed24(base byte) []byte {
	b := make([]byte, 24)
	for i := range b {
		b[i] = base + byte(i)
	}
	return b
}

func applyTraceMiddleware(rand *bytes.Reader, next http.Handler) http.Handler {
	mw := middleware.TraceW3C(rand, slog.Default())
	return mw(next)
}

// Test 1: Valid Traceparent is parsed and echoed on the response.
func TestTraceMiddleware_ValidTraceparent_Preserved(t *testing.T) {
	incoming := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	var captured trace.Trace

	handler := applyTraceMiddleware(fixedReader(seed24(1)), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		captured, ok = trace.FromContext(r.Context())
		require.True(t, ok)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Traceparent", incoming)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", captured.TraceID)
	require.Equal(t, "00f067aa0ba902b7", captured.SpanID)
	require.True(t, captured.Sampled)
	// Response must echo the same traceparent.
	require.Equal(t, incoming, rr.Header().Get("Traceparent"))
}

// Test 2: X-Request-ID used as trace_id when Traceparent is absent.
func TestTraceMiddleware_XRequestID_UsedAsTraceID(t *testing.T) {
	hexID := "4bf92f3577b34da6a3ce929d0e0e4736"
	var captured trace.Trace

	handler := applyTraceMiddleware(fixedReader(seed24(2)), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		captured, ok = trace.FromContext(r.Context())
		require.True(t, ok)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", hexID)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, hexID, captured.TraceID)
	// Response Traceparent must carry the same trace_id.
	tp := rr.Header().Get("Traceparent")
	require.True(t, strings.HasPrefix(tp, "00-"+hexID+"-"))
}

// Test 3: Both headers absent → fresh Trace generated.
func TestTraceMiddleware_NoHeaders_FreshTrace(t *testing.T) {
	var captured trace.Trace

	handler := applyTraceMiddleware(fixedReader(seed24(3)), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		captured, ok = trace.FromContext(r.Context())
		require.True(t, ok)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, captured.TraceID, 32)
	require.Len(t, captured.SpanID, 16)
	// Response must carry the echoed traceparent.
	tp := rr.Header().Get("Traceparent")
	require.NotEmpty(t, tp)
	require.True(t, strings.HasPrefix(tp, "00-"))
}

// Test 4: Malformed Traceparent → fresh Trace generated, warning logged,
// response still carries a valid Traceparent header.
func TestTraceMiddleware_MalformedTraceparent_FreshGenerated(t *testing.T) {
	var logBuf strings.Builder
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	var captured trace.Trace
	mw := middleware.TraceW3C(fixedReader(seed24(4)), logger)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		captured, ok = trace.FromContext(r.Context())
		require.True(t, ok)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Traceparent", "not-a-valid-traceparent")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, captured.TraceID, 32, "malformed header must produce a fresh trace")
	// Warning must appear in log output.
	require.Contains(t, logBuf.String(), "malformed Traceparent")
	// Response must echo a valid traceparent.
	tp := rr.Header().Get("Traceparent")
	_, err := trace.Parse(tp)
	require.NoError(t, err, "echoed Traceparent must be valid W3C")
}

// Test 5: Traceparent is present and valid, context carries it through.
func TestTraceMiddleware_ContextPropagation(t *testing.T) {
	incoming := "00-aaaabbbbccccddddaaaabbbbccccdddd-1122334455667788-01"
	handlerCalled := false

	handler := applyTraceMiddleware(fixedReader(seed24(5)), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		tr, ok := trace.FromContext(r.Context())
		require.True(t, ok)
		require.Equal(t, "aaaabbbbccccddddaaaabbbbccccdddd", tr.TraceID)
		require.Equal(t, "1122334455667788", tr.SpanID)
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/changes", nil)
	req.Header.Set("Traceparent", incoming)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.True(t, handlerCalled)
	require.Equal(t, http.StatusNoContent, rr.Code)
}
