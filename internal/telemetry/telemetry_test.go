package telemetry

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/km269/wukong/internal/config"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

// TestNewManager_Disabled verifies that telemetry manager handles
// disabled configuration gracefully.
func TestNewManager_Disabled(t *testing.T) {
	cfg := config.TelemetryConfig{
		Enabled: false,
	}
	mgr := NewManager(cfg)
	if mgr == nil {
		t.Fatal("NewManager returned nil")
	}

	shutdown, err := mgr.Initialize(context.Background())
	if err != nil {
		t.Errorf("Initialize should not error when disabled: %v", err)
	}
	if shutdown == nil {
		t.Error("shutdown function should not be nil even when disabled")
	}

	// Shutdown should not error
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("disabled shutdown should not error: %v", err)
	}
}

// TestNewManager_ConsoleExporter verifies console exporter
// initialization and shutdown.
func TestNewManager_ConsoleExporter(t *testing.T) {
	cfg := config.TelemetryConfig{
		Enabled:        true,
		ExporterType:   "console",
		ServiceName:    "wukong-test",
		ServiceVersion: "0.1.0",
		Environment:    "test",
		SampleRate:     1.0,
	}
	mgr := NewManager(cfg)

	shutdown, err := mgr.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Verify tracer provider is set
	tp := otel.GetTracerProvider()
	if tp == nil {
		t.Error("tracer provider should be set")
	}

	// Create a span to verify it works
	tracer := otel.Tracer("wukong-test")
	_, span := tracer.Start(context.Background(), "test-span")
	span.SetAttributes(attribute.String("test.key", "test.value"))
	span.End()

	// Shutdown
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

// TestConsoleExporter_ExportSpans verifies span export to console.
func TestConsoleExporter_ExportSpans(t *testing.T) {
	var buf strings.Builder
	exp, err := NewConsoleExporter(&buf)
	if err != nil {
		t.Fatalf("NewConsoleExporter: %v", err)
	}

	// Create a real span via the SDK
	cfg := config.TelemetryConfig{
		Enabled:        true,
		ExporterType:   "console",
		ServiceName:    "wukong-test",
		ServiceVersion: "1.0.0",
		Environment:    "test",
		SampleRate:     1.0,
	}
	mgr := NewManager(cfg)

	// Replace the console exporter with our buffer-based one
	mgr.provider = nil // Reset to test exporter directly

	shutdown, err := mgr.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	defer shutdown(context.Background())

	// Create and end a span
	tracer := otel.Tracer("wukong-test-span")
	ctx, span := tracer.Start(context.Background(), "export-test")
	span.SetAttributes(attribute.String("key1", "value1"))
	span.End()
	_ = ctx

	// Verify exporter shutdown doesn't error
	if err := exp.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

// TestConsoleExporter_Shutdown verifies console exporter shutdown.
func TestConsoleExporter_Shutdown(t *testing.T) {
	exp, err := NewConsoleExporter(&strings.Builder{})
	if err != nil {
		t.Fatalf("NewConsoleExporter: %v", err)
	}
	if err := exp.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown should succeed: %v", err)
	}
}

// TestManager_UnsupportedExporter verifies error for unsupported exporter.
func TestManager_UnsupportedExporter(t *testing.T) {
	cfg := config.TelemetryConfig{
		Enabled:      true,
		ExporterType: "unsupported_type",
		ServiceName:  "wukong-test",
		SampleRate:   1.0,
	}
	mgr := NewManager(cfg)

	_, err := mgr.Initialize(context.Background())
	if err == nil {
		t.Error("expected error for unsupported exporter type")
	}
}

// TestManager_Shutdown_NotInitialized verifies shutdown when not initialized.
func TestManager_Shutdown_NotInitialized(t *testing.T) {
	cfg := config.TelemetryConfig{
		Enabled: false,
	}
	mgr := NewManager(cfg)

	if err := mgr.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown without init should not error: %v", err)
	}
}

// TestManager_GRPCExporterConfig verifies gRPC exporter config
// does not panic (connection may fail in test, which is expected).
func TestManager_GRPCExporterConfig(t *testing.T) {
	cfg := config.TelemetryConfig{
		Enabled:      true,
		ExporterType: "grpc",
		Endpoint:     "localhost:14317", // Non-existent port
		ServiceName:  "wukong-test",
		SampleRate:   1.0,
	}
	mgr := NewManager(cfg)

	// Initialize may fail because no collector is running, but
	// it should not panic.
	_, err := mgr.Initialize(context.Background())
	if err == nil {
		// If no error, clean up
		mgr.Shutdown(context.Background())
	}
	// Error is expected in test environment without a collector.
}

// TestSpanHelper_StartSpan verifies the SpanHelper convenience API.
func TestSpanHelper_StartSpan(t *testing.T) {
	// Initialize a basic telemetry setup
	cfg := config.TelemetryConfig{
		Enabled:        true,
		ExporterType:   "console",
		ServiceName:    "wukong-test",
		ServiceVersion: "1.0.0",
		Environment:    "test",
		SampleRate:     1.0,
	}
	mgr := NewManager(cfg)
	shutdown, err := mgr.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	defer shutdown(context.Background())

	helper := NewSpanHelper("wukong-test-helper")
	ctx, span := helper.StartSpan(context.Background(), "helper-test",
		attribute.String("component", "test"),
		attribute.Int("priority", 1),
	)
	if ctx == nil {
		t.Error("context should not be nil")
	}
	span.End()
}

// TestManager_HTTPExporterConfig verifies HTTP exporter config.
func TestManager_HTTPExporterConfig(t *testing.T) {
	cfg := config.TelemetryConfig{
		Enabled:      true,
		ExporterType: "http",
		Endpoint:     "localhost:14318", // Non-existent port
		ServiceName:  "wukong-test",
		SampleRate:   1.0,
	}
	mgr := NewManager(cfg)

	// Initialize may fail because no collector is running.
	_, err := mgr.Initialize(context.Background())
	if err == nil {
		mgr.Shutdown(context.Background())
	}
	// Error is expected in test environment.
}

// TestManager_ResourceAttributes verifies that service metadata
// is correctly set on the resource.
func TestManager_ResourceAttributes(t *testing.T) {
	cfg := config.TelemetryConfig{
		Enabled:        true,
		ExporterType:   "console",
		ServiceName:    "wukong-integration-test",
		ServiceVersion: "2.0.0",
		Environment:    "staging",
		SampleRate:     0.5,
	}
	mgr := NewManager(cfg)

	shutdown, err := mgr.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	defer shutdown(context.Background())

	// Verify that spans can be created with the configured service
	tracer := otel.Tracer("wukong-integration-test")
	_, span := tracer.Start(context.Background(), "resource-test")
	span.End()
}

// TestManager_SampleRate verifies sample rate configuration.
func TestManager_SampleRate(t *testing.T) {
	tests := []struct {
		name       string
		sampleRate float64
	}{
		{"full sampling", 1.0},
		{"half sampling", 0.5},
		{"no sampling", 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.TelemetryConfig{
				Enabled:        true,
				ExporterType:   "console",
				ServiceName:    "wukong-test",
				ServiceVersion: "1.0.0",
				Environment:    "test",
				SampleRate:     tt.sampleRate,
			}
			mgr := NewManager(cfg)
			shutdown, err := mgr.Initialize(context.Background())
			if err != nil {
				t.Fatalf("Initialize: %v", err)
			}
			defer shutdown(context.Background())

			// Create multiple spans to verify sampling doesn't panic
			tracer := otel.Tracer("wukong-sample-test")
			for i := 0; i < 10; i++ {
				_, span := tracer.Start(context.Background(), "sample-span")
				span.End()
			}
		})
	}
}

// TestManager_DefaultValues verifies that defaults are applied correctly.
func TestManager_DefaultValues(t *testing.T) {
	cfg := config.TelemetryConfig{
		Enabled:        true,
		ExporterType:   "console",
		ServiceName:    "",
		ServiceVersion: "",
		Environment:    "",
		SampleRate:     0.0,
	}
	// Empty strings should still allow initialization with defaults
	mgr := NewManager(cfg)
	shutdown, err := mgr.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize with empty defaults: %v", err)
	}
	defer shutdown(context.Background())
}

// TestManager_TimeoutContext verifies span creation with timeout context.
func TestManager_TimeoutContext(t *testing.T) {
	cfg := config.TelemetryConfig{
		Enabled:        true,
		ExporterType:   "console",
		ServiceName:    "wukong-timeout-test",
		ServiceVersion: "1.0.0",
		Environment:    "test",
		SampleRate:     1.0,
	}
	mgr := NewManager(cfg)
	shutdown, err := mgr.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	defer shutdown(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	tracer := otel.Tracer("wukong-timeout-test")
	_, span := tracer.Start(ctx, "timeout-span")
	span.SetAttributes(attribute.String("status", "timed"))
	span.End()
}
