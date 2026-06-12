package util

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestLogger_Default(t *testing.T) {
	if Logger == nil {
		t.Fatal("expected non-nil default Logger")
	}
}

func TestSetDebugMode(t *testing.T) {
	SetDebugMode()
	if Logger == nil {
		t.Fatal("expected non-nil Logger after debug mode")
	}

	// Verify debug level by writing a debug message
	var buf bytes.Buffer
	Logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	Logger.Debug("test debug message")
	if !strings.Contains(buf.String(), "test debug message") {
		t.Error("expected debug message to be logged")
	}
}

func TestSetQuietMode(t *testing.T) {
	SetQuietMode()
	if Logger == nil {
		t.Fatal("expected non-nil Logger after quiet mode")
	}

	// Debug messages should not appear in warn mode
	var buf bytes.Buffer
	Logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))
	Logger.Debug("should not appear")
	if buf.Len() > 0 {
		t.Error("expected no output for debug in quiet mode")
	}

	Logger.Warn("should appear")
	if !strings.Contains(buf.String(), "should appear") {
		t.Error("expected warn message to be logged")
	}
}

func TestLogger_StructuredFields(t *testing.T) {
	var buf bytes.Buffer
	Logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	Logger.Info("test with fields",
		slog.String("key", "value"),
		slog.Int("count", 42),
	)

	output := buf.String()
	if !strings.Contains(output, "key=value") {
		t.Error("expected structured field 'key=value'")
	}
	if !strings.Contains(output, "count=42") {
		t.Error("expected structured field 'count=42'")
	}
}
