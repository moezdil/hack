package main

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const scope = "github.com/moezdil/hack"

// Exporters follow the standard OTEL_* env vars (OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_EXPORTER_OTLP_HEADERS, ...).
// Without OTEL_EXPORTER_OTLP_ENDPOINT nothing is exported, so local runs and tests need no collector.
func setupTelemetry(ctx context.Context) (shutdown func(context.Context) error, err error) {
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return func(context.Context) error { return nil }, nil
	}
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(attribute.String("service.name", "hack")))
	if err != nil {
		return nil, err
	}
	spanExp, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return nil, err
	}
	reader, err := autoexport.NewMetricReader(ctx)
	if err != nil {
		return nil, err
	}
	logExp, err := autoexport.NewLogExporter(ctx)
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(spanExp), sdktrace.WithResource(res))
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader), sdkmetric.WithResource(res))
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)), sdklog.WithResource(res))
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	// Logs go to stdout (kubectl logs, Radar) and to Dash0, with trace ids.
	slog.SetDefault(slog.New(slog.NewMultiHandler(
		slog.NewTextHandler(os.Stdout, nil),
		otelslog.NewHandler(scope, otelslog.WithLoggerProvider(lp)),
	)))
	return func(ctx context.Context) error {
		return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx), lp.Shutdown(ctx))
	}, nil
}

// telemetry holds the instruments. They are no-ops until setupTelemetry installs real providers.
type telemetry struct {
	tracer     trace.Tracer
	meter      metric.Meter
	delay      metric.Float64Histogram
	captions   metric.Int64Counter
	reconnects metric.Int64Counter
}

func newTelemetry(h *hub) *telemetry {
	m := otel.Meter(scope)
	t := &telemetry{tracer: otel.Tracer(scope), meter: m}
	t.delay, _ = m.Float64Histogram("caption.delay", metric.WithUnit("ms"),
		metric.WithDescription("Time from speech to a pipeline stage (received, sent, displayed)"),
		metric.WithExplicitBucketBoundaries(100, 250, 500, 750, 1000, 1500, 2000, 3000, 5000, 10000))
	t.captions, _ = m.Int64Counter("captions", metric.WithDescription("Captions received, by final and status"))
	t.reconnects, _ = m.Int64Counter("sse.reconnects", metric.WithDescription("Listener streams resumed after a drop"))
	m.Int64ObservableGauge("listeners", metric.WithDescription("Connected listeners per room"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			for room, n := range h.counts() {
				o.Observe(int64(n), metric.WithAttributes(attribute.String("room", room)))
			}
			return nil
		}))
	return t
}

func (t *telemetry) recordDelay(ctx context.Context, room, stage string, ms int64) {
	if ms < 0 {
		ms = 0 // device clocks differ; never report negative delay
	}
	t.delay.Record(ctx, float64(ms), metric.WithAttributes(attribute.String("room", room), attribute.String("stage", stage)))
}
