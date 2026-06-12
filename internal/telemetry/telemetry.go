// Package telemetry provides OpenTelemetry integration for wukong
// using trpc-agent-go's built-in telemetry capabilities.
// It enables distributed tracing, metrics collection, and
// structured observability for agent operations.
package telemetry

import (
	"context"
	"fmt"
	"os"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/util"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Manager manages OpenTelemetry lifecycle for the agent.
type Manager struct {
	cfg      config.TelemetryConfig
	provider *sdktrace.TracerProvider
}

// NewManager creates a new telemetry manager.
func NewManager(cfg config.TelemetryConfig) *Manager {
	return &Manager{cfg: cfg}
}

// Initialize sets up OpenTelemetry with the configured exporter.
// Returns a shutdown function that should be deferred.
func (m *Manager) Initialize(
	ctx context.Context,
) (shutdown func(context.Context) error, err error) {
	if !m.cfg.Enabled {
		util.Logger.Debug("telemetry disabled")
		return func(ctx context.Context) error { return nil }, nil
	}

	// Create resource with service metadata
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(m.cfg.ServiceName),
			semconv.ServiceVersionKey.String(m.cfg.ServiceVersion),
			semconv.DeploymentEnvironmentKey.String(m.cfg.Environment),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create resource: %w", err)
	}

	// Create exporter based on type
	exp, err := m.createExporter(ctx)
	if err != nil {
		return nil, fmt.Errorf("create exporter: %w", err)
	}

	// Configure sampler
	sampler := sdktrace.ParentBased(
		sdktrace.TraceIDRatioBased(m.cfg.SampleRate),
	)

	// Create tracer provider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	// Set global tracer provider
	otel.SetTracerProvider(tp)

	// Set global propagator for W3C trace context
	otel.SetTextMapPropagator(
		propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	)

	m.provider = tp

	util.Logger.Info("telemetry initialized",
		"service", m.cfg.ServiceName,
		"exporter", m.cfg.ExporterType,
		"environment", m.cfg.Environment,
	)

	return tp.Shutdown, nil
}

// Shutdown gracefully shuts down the telemetry provider.
func (m *Manager) Shutdown(ctx context.Context) error {
	if m.provider != nil {
		return m.provider.Shutdown(ctx)
	}
	return nil
}

// createExporter creates the appropriate OTLP exporter.
func (m *Manager) createExporter(
	ctx context.Context,
) (sdktrace.SpanExporter, error) {
	switch m.cfg.ExporterType {
	case "grpc":
		return m.createGRPCExporter(ctx)
	case "http":
		return m.createHTTPExporter(ctx)
	case "console":
		return m.createConsoleExporter()
	default:
		return nil, fmt.Errorf(
			"unsupported exporter type: %s", m.cfg.ExporterType,
		)
	}
}

// createGRPCExporter creates an OTLP gRPC exporter.
func (m *Manager) createGRPCExporter(
	ctx context.Context,
) (sdktrace.SpanExporter, error) {
	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(m.cfg.Endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("create gRPC exporter: %w", err)
	}
	return exp, nil
}

// createHTTPExporter creates an OTLP HTTP exporter.
func (m *Manager) createHTTPExporter(
	ctx context.Context,
) (sdktrace.SpanExporter, error) {
	exp, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(m.cfg.Endpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("create HTTP exporter: %w", err)
	}
	return exp, nil
}

// createConsoleExporter creates a simple stdout exporter for
// development use.
func (m *Manager) createConsoleExporter() (
	sdktrace.SpanExporter, error,
) {
	return NewConsoleExporter(os.Stdout)
}

// ConsoleExporter writes spans to an io.Writer for development.
type ConsoleExporter struct {
	writer interface{ Write([]byte) (int, error) }
}

// NewConsoleExporter creates a console span exporter.
func NewConsoleExporter(w interface {
	Write([]byte) (int, error)
}) (*ConsoleExporter, error) {
	return &ConsoleExporter{writer: w}, nil
}

// ExportSpans writes spans to the console.
func (e *ConsoleExporter) ExportSpans(
	ctx context.Context, spans []sdktrace.ReadOnlySpan,
) error {
	for _, span := range spans {
		attrs := []any{}
		for _, attr := range span.Attributes() {
			attrs = append(attrs, string(attr.Key), attr.Value.AsString())
		}
		util.Logger.Debug("span",
			"name", span.Name(),
			"trace_id", span.SpanContext().TraceID().String(),
			"span_id", span.SpanContext().SpanID().String(),
			"attributes", attrs,
		)
	}
	return nil
}

// Shutdown is a no-op for console exporter.
func (e *ConsoleExporter) Shutdown(ctx context.Context) error {
	return nil
}

// SpanHelper provides convenience methods for creating spans.
type SpanHelper struct {
	tracerName string
}

// NewSpanHelper creates a new span helper.
func NewSpanHelper(tracerName string) *SpanHelper {
	return &SpanHelper{tracerName: tracerName}
}

// StartSpan starts a new span with the given name and attributes.
func (h *SpanHelper) StartSpan(
	ctx context.Context, name string, attrs ...attribute.KeyValue,
) (context.Context, trace.Span) {
	tracer := otel.Tracer(h.tracerName)
	ctx, span := tracer.Start(ctx, name)
	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
	return ctx, span
}
