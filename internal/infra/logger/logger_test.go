package logger

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
)

func TestNewSlogLogger_Creation(t *testing.T) {
	// Verify NewSlogLoggerWithHandler returns a non-nil Logger.
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, nil)
	l := NewSlogLoggerWithHandler(handler)

	if l == nil {
		t.Fatal("expected non-nil logger")
	}

	// Writing through the logger should produce output.
	l.Info("test message", "key", "value")
	if buf.Len() == 0 {
		t.Fatal("expected some log output")
	}
}

func TestNopLogger_MethodsDontPanic(t *testing.T) {
	l := NopLogger()

	// None of these should panic.
	l.Debug("debug msg", "k", "v")
	l.Info("info msg", "k", "v")
	l.Warn("warn msg", "k", "v")
	l.Error("error msg", "k", "v")

	child := l.With("request_id", "abc")
	if child == nil {
		t.Fatal("With() returned nil")
	}

	ctxChild := l.WithContext(context.Background())
	if ctxChild == nil {
		t.Fatal("WithContext() returned nil")
	}
}

func TestWith_CreatesChildLogger(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	l := NewSlogLoggerWithHandler(handler)

	child := l.With("component", "test")
	if child == nil {
		t.Fatal("With() returned nil")
	}

	child.Info("child message")
	output := buf.String()
	if output == "" {
		t.Fatal("expected log output from child logger")
	}
	// The child logger should carry the "component" attribute.
	if !contains(output, "component") || !contains(output, "test") {
		t.Fatalf("expected child logger output to contain component=test, got: %s", output)
	}
}

func TestWithContext_ExtractsFields(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	l := NewSlogLoggerWithHandler(handler)

	ctx := context.WithValue(context.Background(), "trace_id", "abc123")
	ctx = context.WithValue(ctx, "request_id", "req456")

	ctxLogger := l.WithContext(ctx)
	ctxLogger.Info("with context")
	output := buf.String()

	if !contains(output, "abc123") {
		t.Fatalf("expected output to contain trace_id abc123, got: %s", output)
	}
	if !contains(output, "req456") {
		t.Fatalf("expected output to contain request_id req456, got: %s", output)
	}
}

// contains is a simple substring check for test readability.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
