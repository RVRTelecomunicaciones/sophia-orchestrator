package obs_test

import (
	"context"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/obs"
	"github.com/stretchr/testify/require"
)

func TestNewTracer_DisabledIsNoOp(t *testing.T) {
	tr, err := obs.NewTracer(context.Background(), obs.TraceConfig{Enabled: false})
	require.NoError(t, err)
	require.NotNil(t, tr)
	require.False(t, tr.Enabled())
	require.NoError(t, tr.Shutdown(context.Background()))
}

func TestNewTracer_EnabledStdoutFallback(t *testing.T) {
	cfg := obs.DefaultTraceConfig()
	cfg.Enabled = true
	cfg.SampleRatio = 1.0
	cfg.Endpoint = "" // forces stdouttrace fallback
	tr, err := obs.NewTracer(context.Background(), cfg)
	require.NoError(t, err)
	require.True(t, tr.Enabled())
	defer func() {
		_ = tr.Shutdown(context.Background())
	}()

	// Acquire a tracer + start/end a span just to exercise the path.
	tracer := tr.Tracer("test")
	_, span := tracer.Start(context.Background(), "smoke")
	span.End()
}

func TestDefaultTraceConfig_Defaults(t *testing.T) {
	c := obs.DefaultTraceConfig()
	require.False(t, c.Enabled)
	require.Equal(t, "sophia-orchestator", c.ServiceName)
	require.Equal(t, 1.0, c.SampleRatio)
}
