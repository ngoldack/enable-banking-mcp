// Package telemetry wires in-process OpenTelemetry tracing and metrics for the
// MCP server. Instrumentation lives inside the application (the OTel Go SDK),
// so the server runs as a single, unprivileged, hardened container — no eBPF
// agent, no privileged/shareProcessNamespace requirement.
//
// Setup is a no-op unless an OTLP endpoint is configured via the standard
// OTEL_EXPORTER_OTLP_ENDPOINT (or signal-specific) environment variables. This
// keeps the binary at zero telemetry overhead when observability is not wired
// up, while staying fully 12-factor / Kubernetes friendly.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// ServiceName is the canonical service.name reported to OpenTelemetry backends.
const ServiceName = "fin-mcp"

// ShutdownFunc flushes and releases the telemetry providers. Call it on
// shutdown (with a bounded context) to ensure buffered spans/metrics are
// exported before the process exits.
type ShutdownFunc func(context.Context) error

// Setup installs global OpenTelemetry trace and metric providers exporting over
// OTLP/HTTP, plus a W3C TraceContext + Baggage propagator. It returns a
// ShutdownFunc that gracefully tears the providers down.
//
// When no OTLP endpoint is configured, Setup installs nothing and returns a
// no-op ShutdownFunc — the global OTel providers stay no-op, so all
// instrumentation (otelhttp, manual spans, metrics) compiles to near-zero cost.
func Setup(ctx context.Context, serviceVersion string) (ShutdownFunc, error) {
	noop := func(context.Context) error { return nil }
	if !enabled() {
		return noop, nil
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithHost(),
		resource.WithAttributes(serviceAttrs(serviceVersion)...),
	)
	if err != nil {
		return nil, fmt.Errorf("build otel resource: %w", err)
	}

	traceExp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create otlp trace exporter: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp),
		sdktrace.WithResource(res),
	)

	metricExp, err := otlpmetrichttp.New(ctx)
	if err != nil {
		// Tear down the trace provider we already created before bailing out.
		_ = tp.Shutdown(ctx)
		return nil, fmt.Errorf("create otlp metric exporter: %w", err)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp)),
		sdkmetric.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return func(ctx context.Context) error {
		return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx))
	}, nil
}

// enabled reports whether any standard OTLP endpoint environment variable is set.
func enabled() bool {
	for _, k := range []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
	} {
		if os.Getenv(k) != "" {
			return true
		}
	}
	return false
}

// serviceAttrs returns the resource attributes. service.name defaults to
// ServiceName but yields to OTEL_SERVICE_NAME (honored by resource.WithFromEnv)
// when the operator sets it.
func serviceAttrs(version string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{attribute.String("service.version", version)}
	if os.Getenv("OTEL_SERVICE_NAME") == "" {
		attrs = append(attrs, attribute.String("service.name", ServiceName))
	}
	return attrs
}
