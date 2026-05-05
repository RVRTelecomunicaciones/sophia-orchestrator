package obs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
	"go.opentelemetry.io/otel/trace"
)

// TraceConfig parameterizes Tracer setup.
type TraceConfig struct {
	// Enabled toggles span emission. When false, OTEL setup returns a
	// no-op TracerProvider and Shutdown is a no-op.
	Enabled bool
	// Endpoint is the OTLP HTTP exporter target (e.g. "localhost:4318").
	// Empty + Enabled=true falls back to stdouttrace for dev.
	Endpoint string
	// Insecure skips TLS for OTLP HTTP. Only sensible in dev.
	Insecure bool
	// ServiceName, Version, Environment are recorded as resource attributes.
	ServiceName string
	Version     string
	Environment string
	// SampleRatio in [0.0, 1.0]. 1.0 = always sample.
	SampleRatio float64
}

// DefaultTraceConfig returns dev-friendly defaults.
func DefaultTraceConfig() TraceConfig {
	return TraceConfig{
		Enabled:     false,
		ServiceName: "sophia-orchestator",
		Version:     "v0.1.0",
		Environment: "dev",
		SampleRatio: 1.0,
	}
}

// Tracer wraps the configured TracerProvider with a Shutdown handle.
type Tracer struct {
	tp       *sdktrace.TracerProvider
	shutdown func(context.Context) error
	enabled  bool
}

// NewTracer initializes OpenTelemetry tracing. On Enabled=false, returns
// a Tracer whose Shutdown + Tracer methods are no-ops; the global
// otel.GetTracerProvider() stays at its default no-op provider.
func NewTracer(ctx context.Context, cfg TraceConfig) (*Tracer, error) {
	if !cfg.Enabled {
		return &Tracer{enabled: false, shutdown: func(context.Context) error { return nil }}, nil
	}
	if cfg.SampleRatio <= 0 || cfg.SampleRatio > 1 {
		cfg.SampleRatio = 1.0
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.Version),
			semconv.DeploymentEnvironmentName(cfg.Environment),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("obs.NewTracer: resource: %w", err)
	}

	exporter, err := buildExporter(ctx, cfg)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(2*time.Second)),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(cfg.SampleRatio)),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	t := &Tracer{tp: tp, enabled: true}
	t.shutdown = func(c context.Context) error {
		return tp.Shutdown(c) //nolint:wrapcheck
	}
	return t, nil
}

// Shutdown flushes pending spans + closes the exporter. Safe to call from
// signal handlers; bounded by the supplied context.
func (t *Tracer) Shutdown(ctx context.Context) error {
	if t == nil || t.shutdown == nil {
		return nil
	}
	return t.shutdown(ctx)
}

// Enabled reports whether tracing is wired (true ⇒ spans will be emitted).
func (t *Tracer) Enabled() bool { return t != nil && t.enabled }

// Tracer returns the named tracer from the global TracerProvider. Safe to
// call regardless of Enabled (returns a no-op tracer when disabled).
func (t *Tracer) Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}

func buildExporter(ctx context.Context, cfg TraceConfig) (sdktrace.SpanExporter, error) {
	if cfg.Endpoint == "" {
		// Dev fallback: stdouttrace dumps spans to process stdout.
		return stdouttrace.New(stdouttrace.WithPrettyPrint())
	}
	opts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(cfg.Endpoint)}
	if cfg.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	exp, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("obs.NewTracer: otlp: %w", errors.Join(err))
	}
	return exp, nil
}
