package logger

import (
	"context"
	"log/slog"
	"os"
)

// Logger provides structured logging.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
	With(args ...any) Logger
	WithContext(ctx context.Context) Logger
}

type slogLogger struct {
	inner *slog.Logger
}

// NewSlogLogger creates a Logger backed by slog with JSON output.
func NewSlogLogger() Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	return &slogLogger{inner: slog.New(handler)}
}

// NewSlogLoggerWithHandler creates a Logger with a custom slog handler.
func NewSlogLoggerWithHandler(h slog.Handler) Logger {
	return &slogLogger{inner: slog.New(h)}
}

func (l *slogLogger) Debug(msg string, args ...any) { l.inner.Debug(msg, args...) }
func (l *slogLogger) Info(msg string, args ...any)  { l.inner.Info(msg, args...) }
func (l *slogLogger) Warn(msg string, args ...any)  { l.inner.Warn(msg, args...) }
func (l *slogLogger) Error(msg string, args ...any) { l.inner.Error(msg, args...) }

func (l *slogLogger) With(args ...any) Logger {
	return &slogLogger{inner: l.inner.With(args...)}
}

func (l *slogLogger) WithContext(ctx context.Context) Logger {
	args := extractContextFields(ctx)
	if len(args) > 0 {
		return &slogLogger{inner: l.inner.With(args...)}
	}
	return l
}

func extractContextFields(ctx context.Context) []any {
	var args []any
	if traceID, ok := ctx.Value("trace_id").(string); ok {
		args = append(args, "trace_id", traceID)
	}
	if requestID, ok := ctx.Value("request_id").(string); ok {
		args = append(args, "request_id", requestID)
	}
	return args
}

type nopLogger struct{}

func (nopLogger) Debug(string, ...any)          {}
func (nopLogger) Info(string, ...any)           {}
func (nopLogger) Warn(string, ...any)           {}
func (nopLogger) Error(string, ...any)          {}
func (l nopLogger) With(...any) Logger          { return l }
func (l nopLogger) WithContext(context.Context) Logger { return l }

// NopLogger returns a no-op logger for benchmarks and testing.
func NopLogger() Logger { return nopLogger{} }

var defaultLogger Logger = NewSlogLogger()

// SetDefault replaces the global default logger.
func SetDefault(l Logger) { defaultLogger = l }

// GetDefault returns the global default logger.
func GetDefault() Logger { return defaultLogger }
