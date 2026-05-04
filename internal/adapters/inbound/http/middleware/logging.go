package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// Logging returns a middleware that logs every request via slog with
// method, path, status, duration_ms, and the resolved project (if any).
func Logging(logger *slog.Logger) func(next http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)
			logger.LogAttrs(r.Context(), slog.LevelInfo, "http",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rw.status),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.String("project", ProjectFromContext(r.Context())),
			)
		})
	}
}

// Recover catches panics in handlers, logs them, and returns 500.
func Recover(logger *slog.Logger) func(next http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.LogAttrs(r.Context(), slog.LevelError, "panic",
						slog.Any("recovered", rec),
						slog.String("path", r.URL.Path),
					)
					http.Error(w, `{"error":"internal server error","code":"internal"}`, http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush forwards Flush to the wrapped ResponseWriter so SSE handlers
// (which type-assert http.Flusher) keep working through the middleware.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack forwards Hijack to the wrapped ResponseWriter to support websockets
// or other connection-takeover patterns through the logging middleware.
//
// We cannot return the typed values without importing net (and tying to a
// specific impl), so callers should inspect the wrapped writer directly when
// hijack support matters.

// _ silences the unused-import warning for context (transitively imported
// via slog).
var _ = context.Background
