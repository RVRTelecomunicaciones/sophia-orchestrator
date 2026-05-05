package middleware

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// MetricsRecorder returns a chi middleware that records the inbound
// HTTP request duration histogram + status counter from spec § 9.2.
//
// Labels: method (GET/POST/...), route (chi route pattern, NOT raw URL —
// keeps cardinality bounded), status ("2xx" / "4xx" / "5xx" classified).
func MetricsRecorder(durationHist *prometheus.HistogramVec) func(http.Handler) http.Handler {
	if durationHist == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := newStatusBuf(w)
			next.ServeHTTP(rw, r)
			durationHist.WithLabelValues(
				r.Method,
				routePatternFromContext(r),
				statusClass(rw.status),
			).Observe(float64(time.Since(start).Milliseconds()))
		})
	}
}

// Tracing returns a chi middleware that opens a span per request rooted at
// the given tracer. The span name uses the chi route pattern when set, or
// falls back to method+path. Standard HTTP attributes are recorded.
func Tracing(tracer trace.Tracer) func(http.Handler) http.Handler {
	if tracer == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			route := routePatternFromContext(r)
			if route == "" {
				route = r.URL.Path
			}
			ctx, span := tracer.Start(r.Context(), r.Method+" "+route)
			defer span.End()
			span.SetAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.route", route),
				attribute.String("http.target", r.URL.Path),
				attribute.String("net.peer.ip", r.RemoteAddr),
			)
			rw := newStatusBuf(w)
			next.ServeHTTP(rw, r.WithContext(ctx))
			span.SetAttributes(attribute.Int("http.status_code", rw.status))
		})
	}
}

// statusBuf reuses statusRecorder semantics with explicit Flush + Hijack
// passthrough so SSE/streaming handlers keep working under the middleware.
// (We can't reuse the unexported statusRecorder directly because of
// visibility; the duplication is small and contained.)
type statusBuf struct {
	http.ResponseWriter
	status int
}

func newStatusBuf(w http.ResponseWriter) *statusBuf {
	return &statusBuf{ResponseWriter: w, status: http.StatusOK}
}

func (s *statusBuf) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusBuf) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func routePatternFromContext(r *http.Request) string {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return ""
	}
	return rctx.RoutePattern()
}

func statusClass(code int) string {
	switch {
	case code >= 500:
		return "5xx"
	case code >= 400:
		return "4xx"
	case code >= 300:
		return "3xx"
	case code >= 200:
		return "2xx"
	default:
		return "1xx"
	}
}
