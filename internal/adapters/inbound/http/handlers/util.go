package handlers

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/trace"
)

// atoiDefault parses s as int, returning d on error or empty input.
func atoiDefault(s string, d int) int {
	if s == "" {
		return d
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return d
	}
	return n
}

// traceAttrs returns the slog attributes for the W3C Trace stored in ctx.
// When no Trace is present (e.g. in unit tests without the TraceW3C middleware),
// it returns an empty slice so callers do not need to guard.
//
// Convention (ADR-0005 P2.2a): handlers that emit structured log events SHOULD
// include traceAttrs(ctx) so log lines carry trace_id and span_id even outside
// the Logging middleware's automatic enrichment:
//
//	slog.Default().LogAttrs(ctx, slog.LevelInfo, "change created",
//	    append(traceAttrs(ctx), slog.String("change_id", id))...)
//
// The TraceHandler wrapper on the logger already injects these attributes
// automatically when ctx carries a Trace — traceAttrs is provided for
// call-sites that build a new *slog.Logger (e.g. tests) where the wrapper is
// not present.
func traceAttrs(ctx context.Context) []slog.Attr {
	t, ok := trace.FromContext(ctx)
	if !ok {
		return nil
	}
	return []slog.Attr{
		slog.String("trace_id", t.TraceID),
		slog.String("span_id", t.SpanID),
	}
}
