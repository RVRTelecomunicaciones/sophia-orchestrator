package trace_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/trace"
	"github.com/stretchr/testify/require"
)

// deterministicReader returns a reader that cycles through the supplied bytes.
// Useful for making New / WithNewSpan outputs predictable in tests.
func deterministicReader(data []byte) *bytes.Reader {
	return bytes.NewReader(data)
}

// Test 1: New generates a well-formed, parseable Trace.
func TestNew_GeneratesValidTrace(t *testing.T) {
	// 16 bytes for trace_id + 8 bytes for span_id = 24 deterministic bytes.
	seed := make([]byte, 24)
	for i := range seed {
		seed[i] = byte(i + 1) // non-zero bytes so IDs are non-zero
	}
	tr, err := trace.New(deterministicReader(seed))
	require.NoError(t, err)
	require.Len(t, tr.TraceID, 32, "trace_id must be 32 hex chars")
	require.Len(t, tr.SpanID, 16, "span_id must be 16 hex chars")
	require.True(t, tr.Sampled)
}

// Test 2: New → String → Parse round-trip.
func TestNew_ParseRoundTrip(t *testing.T) {
	seed := make([]byte, 24)
	for i := range seed {
		seed[i] = byte(i + 0xAA)
	}
	tr, err := trace.New(deterministicReader(seed))
	require.NoError(t, err)

	header := tr.String()
	require.True(t, strings.HasPrefix(header, "00-"), "must begin with version 00")

	parsed, err := trace.Parse(header)
	require.NoError(t, err)
	require.Equal(t, tr.TraceID, parsed.TraceID)
	require.Equal(t, tr.SpanID, parsed.SpanID)
	require.Equal(t, tr.Sampled, parsed.Sampled)
}

// Test 3: Parse accepts a valid W3C traceparent.
func TestParse_ValidTraceparent(t *testing.T) {
	// Example from the W3C spec.
	h := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	tr, err := trace.Parse(h)
	require.NoError(t, err)
	require.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", tr.TraceID)
	require.Equal(t, "00f067aa0ba902b7", tr.SpanID)
	require.True(t, tr.Sampled)
}

// Test 4: Parse rejects unsupported version.
func TestParse_WrongVersion(t *testing.T) {
	h := "01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	_, err := trace.Parse(h)
	require.ErrorIs(t, err, trace.ErrInvalidTraceparent)
}

// Test 5: Parse rejects wrong trace_id length.
func TestParse_ShortTraceID(t *testing.T) {
	h := "00-4bf92f35-00f067aa0ba902b7-01" // trace_id too short
	_, err := trace.Parse(h)
	require.ErrorIs(t, err, trace.ErrInvalidTraceparent)
}

// Test 6: Parse rejects non-hex characters.
func TestParse_NonHexTraceID(t *testing.T) {
	h := "00-ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ-00f067aa0ba902b7-01"
	_, err := trace.Parse(h)
	require.ErrorIs(t, err, trace.ErrInvalidTraceparent)
}

// Test 7: Parse rejects all-zero trace_id.
func TestParse_AllZeroTraceID(t *testing.T) {
	h := "00-00000000000000000000000000000000-00f067aa0ba902b7-01"
	_, err := trace.Parse(h)
	require.ErrorIs(t, err, trace.ErrInvalidTraceparent)
}

// Test 8: Parse rejects all-zero span_id.
func TestParse_AllZeroSpanID(t *testing.T) {
	h := "00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01"
	_, err := trace.Parse(h)
	require.ErrorIs(t, err, trace.ErrInvalidTraceparent)
}

// Test 9: Parse rejects wrong number of segments.
func TestParse_TooFewSegments(t *testing.T) {
	h := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7" // missing flags
	_, err := trace.Parse(h)
	require.ErrorIs(t, err, trace.ErrInvalidTraceparent)
}

// Test 10: Context round-trip — store and retrieve.
func TestContext_RoundTrip(t *testing.T) {
	seed := make([]byte, 24)
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	tr, err := trace.New(deterministicReader(seed))
	require.NoError(t, err)

	ctx := trace.NewContext(context.Background(), tr)
	got, ok := trace.FromContext(ctx)
	require.True(t, ok)
	require.Equal(t, tr.TraceID, got.TraceID)
	require.Equal(t, tr.SpanID, got.SpanID)
}

// Test 11: FromContext returns false for a plain context.
func TestFromContext_Missing(t *testing.T) {
	_, ok := trace.FromContext(context.Background())
	require.False(t, ok)
}

// Test 12: ChildSpan preserves TraceID and rotates SpanID.
func TestChildSpan_PreservesTraceIDRotatesSpanID(t *testing.T) {
	// First 24 bytes for New; next 8 for ChildSpan's new span.
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = byte(i + 1)
	}

	parent, err := trace.New(deterministicReader(seed[:24]))
	require.NoError(t, err)

	ctx := trace.NewContext(context.Background(), parent)
	_, child, err := trace.ChildSpan(ctx, deterministicReader(seed[24:]))
	require.NoError(t, err)

	require.Equal(t, parent.TraceID, child.TraceID, "TraceID must be preserved in child span")
	require.NotEqual(t, parent.SpanID, child.SpanID, "SpanID must be rotated in child span")
}

// Test 13: ChildSpan on empty context generates a fresh Trace.
func TestChildSpan_EmptyContext_GeneratesFresh(t *testing.T) {
	seed := make([]byte, 24)
	for i := range seed {
		seed[i] = byte(i + 0x10)
	}
	_, child, err := trace.ChildSpan(context.Background(), deterministicReader(seed))
	require.NoError(t, err)
	require.Len(t, child.TraceID, 32)
	require.Len(t, child.SpanID, 16)
}

// Test 14: String formats both sampled and unsampled.
func TestString_SampledFlags(t *testing.T) {
	tr := trace.Trace{TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", SpanID: "00f067aa0ba902b7", Sampled: true}
	require.Equal(t, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", tr.String())

	tr.Sampled = false
	require.Equal(t, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00", tr.String())
}

// Test 15: FromRequestID with a 32-hex value preserves it as trace_id.
func TestFromRequestID_HexValue(t *testing.T) {
	spanSeed := make([]byte, 8)
	for i := range spanSeed {
		spanSeed[i] = byte(i + 1)
	}
	hexID := "4bf92f3577b34da6a3ce929d0e0e4736"
	tr, err := trace.FromRequestID(hexID, deterministicReader(spanSeed))
	require.NoError(t, err)
	require.Equal(t, hexID, tr.TraceID)
}

// Test 16: FromRequestID with a UUID (with dashes) strips dashes.
func TestFromRequestID_UUID(t *testing.T) {
	spanSeed := make([]byte, 8)
	for i := range spanSeed {
		spanSeed[i] = byte(i + 2)
	}
	uuid := "550e8400-e29b-41d4-a716-446655440000"
	expected := strings.ReplaceAll(uuid, "-", "")
	tr, err := trace.FromRequestID(uuid, deterministicReader(spanSeed))
	require.NoError(t, err)
	require.Equal(t, expected, tr.TraceID)
}
