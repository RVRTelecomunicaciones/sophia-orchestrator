package middleware

import (
	"io"
	"log/slog"
	"net/http"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/trace"
)

// TraceW3C returns chi-compatible middleware that implements W3C traceparent
// propagation (ADR-0005 P2.2a). For each inbound request it:
//
//  1. Tries to parse a Traceparent header.
//  2. Falls back to X-Request-ID if Traceparent is absent.
//  3. Generates a fresh Trace if both are absent or Traceparent is malformed
//     (malformed header logs a WARN so operators can catch mis-configured callers).
//  4. Stores the Trace in the request context via trace.NewContext.
//  5. Echoes the (possibly re-generated) traceparent on the response so callers
//     can correlate across retries and re-redirects.
//
// rand must be crypto/rand.Reader in production. Tests may pass a deterministic
// io.Reader (R12 injectable-randomness pattern).
//
// Wire this as the FIRST middleware in the chain (before Logging, Auth, etc.)
// so every subsequent middleware and handler can read trace.FromContext(ctx).
func TraceW3C(rand io.Reader, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t, err := traceFromRequest(r, rand)
			if err != nil {
				// Malformed Traceparent: generate a fresh one and warn.
				// We do NOT reject the request — malformed header is a caller
				// misconfiguration, not a security issue, and rejecting would break
				// callers during the roll-out window.
				logger.LogAttrs(r.Context(), slog.LevelWarn, "trace: malformed Traceparent header, generating fresh trace",
					slog.String("raw_header", r.Header.Get("Traceparent")),
					slog.String("error", err.Error()),
				)
				fresh, genErr := trace.New(rand)
				if genErr != nil {
					// Last resort: serve without trace (should never happen unless
					// rand is broken — treat as internal error silently).
					next.ServeHTTP(w, r)
					return
				}
				t = fresh
			}

			// Inject into context and echo on response.
			ctx := trace.NewContext(r.Context(), t)
			w.Header().Set("Traceparent", t.String())
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// traceFromRequest resolves the Trace for the current request using the
// priority order: Traceparent → X-Request-ID → generate fresh.
func traceFromRequest(r *http.Request, rand io.Reader) (trace.Trace, error) {
	if h := r.Header.Get("Traceparent"); h != "" {
		return trace.Parse(h)
	}
	if h := r.Header.Get("X-Request-ID"); h != "" {
		return trace.FromRequestID(h, rand)
	}
	return trace.New(rand)
}
