// Package telemetry is the engine's own OTel instrumentation — the
// dogfooding path (master plan §3.4): Argus must score well on its own
// telemetry, and CI enforces it. Export is opt-in (empty endpoint = no-op
// providers, zero network activity); the shapes emitted here deliberately
// hold the engine to the same rules it scores others against: complete
// resource identity, units on every metric, no unit tokens in metric
// names, bounded attribute cardinality, non-client root spans.
package telemetry

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	mnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
	tnoop "go.opentelemetry.io/otel/trace/noop"
)

// Config controls the engine's self-instrumentation.
type Config struct {
	// Endpoint is the OTLP gRPC target (host:port, insecure — the sampled
	// mirror path). Empty disables all export.
	Endpoint       string
	ServiceName    string
	ServiceVersion string
	Environment    string
	ExportInterval time.Duration
}

// Telemetry bundles the instruments the engine uses about itself.
type Telemetry struct {
	Tracer trace.Tracer
	// ExportTicks counts score-export cycles (unit "1"; the name carries no
	// unit token — MET-005 applies to us too).
	ExportTicks metric.Int64Counter
	Shutdown    func(context.Context) error
}

// Setup builds tracer + meter providers exporting to cfg.Endpoint, or no-op
// providers when the endpoint is empty.
func Setup(ctx context.Context, cfg Config) (*Telemetry, error) {
	if cfg.Endpoint == "" {
		t := tnoop.NewTracerProvider().Tracer("argus")
		c, err := mnoop.NewMeterProvider().Meter("argus").Int64Counter("argus.engine.export.ticks")
		if err != nil {
			return nil, err
		}
		return &Telemetry{Tracer: t, ExportTicks: c, Shutdown: func(context.Context) error { return nil }}, nil
	}

	if cfg.ServiceName == "" {
		cfg.ServiceName = "argus-engine"
	}
	if cfg.Environment == "" {
		cfg.Environment = "dev"
	}
	if cfg.ExportInterval <= 0 {
		cfg.ExportInterval = 15 * time.Second
	}

	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersion(cfg.ServiceVersion),
		attribute.String("deployment.environment.name", cfg.Environment),
	))
	if err != nil {
		return nil, err
	}

	traceExp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(cfg.Endpoint), otlptracegrpc.WithInsecure())
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(traceExp, sdktrace.WithBatchTimeout(cfg.ExportInterval)),
	)

	metricExp, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(cfg.Endpoint), otlpmetricgrpc.WithInsecure())
	if err != nil {
		return nil, err
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp,
			sdkmetric.WithInterval(cfg.ExportInterval))),
	)

	ticks, err := mp.Meter("argus").Int64Counter("argus.engine.export.ticks",
		metric.WithUnit("1"),
		metric.WithDescription("Score export cycles completed by this engine."))
	if err != nil {
		return nil, err
	}

	return &Telemetry{
		Tracer:      tp.Tracer("argus"),
		ExportTicks: ticks,
		Shutdown: func(ctx context.Context) error {
			return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx))
		},
	}, nil
}
