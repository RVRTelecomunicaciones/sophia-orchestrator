// Package log provides the slog handler wrapper that enriches every log record
// with trace_id and span_id attributes when a W3C Trace is present in the
// request context (ADR-0005 P2.2a).
//
// Usage:
//
//	base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
//	logger := slog.New(log.NewTraceHandler(base))
//
// The TraceHandler is transparent — it delegates all Handler interface methods
// to the wrapped handler and only adds the two trace attributes on Handle.
// This means it works with any existing slog.Handler (JSON, text, test, etc.)
// without modifying callers.
package log

import (
	"context"
	"log/slog"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/trace"
)

// TraceHandler wraps any slog.Handler and injects trace_id + span_id
// attributes from the request context into every log record.
type TraceHandler struct {
	next slog.Handler
}

// NewTraceHandler constructs a TraceHandler that delegates to next.
// next must not be nil.
func NewTraceHandler(next slog.Handler) *TraceHandler {
	return &TraceHandler{next: next}
}

// Enabled delegates to the wrapped handler unchanged.
func (h *TraceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

// Handle injects trace_id and span_id attributes (when present in ctx) before
// delegating to the wrapped handler. The two trace attributes are prepended so
// they appear early in the structured log record for easy grepping.
func (h *TraceHandler) Handle(ctx context.Context, r slog.Record) error {
	if t, ok := trace.FromContext(ctx); ok {
		// Clone the record so we don't mutate the original (Handle must be safe
		// to call concurrently per the slog contract).
		r2 := r.Clone()
		r2.AddAttrs(
			slog.String("trace_id", t.TraceID),
			slog.String("span_id", t.SpanID),
		)
		return h.next.Handle(ctx, r2) //nolint:wrapcheck
	}
	return h.next.Handle(ctx, r) //nolint:wrapcheck
}

// WithAttrs delegates to the wrapped handler and rewraps the result.
func (h *TraceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return NewTraceHandler(h.next.WithAttrs(attrs))
}

// WithGroup delegates to the wrapped handler and rewraps the result.
func (h *TraceHandler) WithGroup(name string) slog.Handler {
	return NewTraceHandler(h.next.WithGroup(name))
}
