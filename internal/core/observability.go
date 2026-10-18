package core

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"
)

// Logger is the minimal structured logging surface used by runtime components.
type Logger interface {
	Debug(msg string, attrs ...any)
	Info(msg string, attrs ...any)
	Warn(msg string, attrs ...any)
	Error(msg string, attrs ...any)
}

type slogLogger struct {
	inner *slog.Logger
}

func NewSlogLogger(logger *slog.Logger) Logger {
	if logger == nil {
		logger = slog.Default()
	}
	return slogLogger{inner: logger}
}

func (l slogLogger) Debug(msg string, attrs ...any) { l.inner.Debug(msg, attrs...) }
func (l slogLogger) Info(msg string, attrs ...any)  { l.inner.Info(msg, attrs...) }
func (l slogLogger) Warn(msg string, attrs ...any)  { l.inner.Warn(msg, attrs...) }
func (l slogLogger) Error(msg string, attrs ...any) { l.inner.Error(msg, attrs...) }

type nopLogger struct{}

func NopLogger() Logger                { return nopLogger{} }
func (nopLogger) Debug(string, ...any) {}
func (nopLogger) Info(string, ...any)  {}
func (nopLogger) Warn(string, ...any)  {}
func (nopLogger) Error(string, ...any) {}

// LeveledLogger is a Logger whose threshold can be changed while it is in use.
//
// The level lives in a slog.LevelVar, which slog consults for every record it
// handles. SetLevel therefore takes effect on the very next line without
// rebuilding the handler or replacing the logger value that components already
// hold -- which is what makes "reload the log level" possible at all: the
// alternative (swap the Logger inside every component) would need a lock on the
// logging hot path.
type LeveledLogger struct {
	inner *slog.Logger
	level *slog.LevelVar
}

// NewLeveledLogger builds a text logger writing to out at the given level.
func NewLeveledLogger(out io.Writer, level slog.Level) *LeveledLogger {
	levelVar := &slog.LevelVar{}
	levelVar.Set(level)
	handler := slog.NewTextHandler(out, &slog.HandlerOptions{Level: levelVar})
	return &LeveledLogger{inner: slog.New(handler), level: levelVar}
}

// SetLevel changes the threshold for all subsequent records.
func (l *LeveledLogger) SetLevel(level slog.Level) {
	if l == nil || l.level == nil {
		return
	}
	l.level.Set(level)
}

// Level returns the current threshold.
func (l *LeveledLogger) Level() slog.Level {
	if l == nil || l.level == nil {
		return slog.LevelInfo
	}
	return l.level.Level()
}

func (l *LeveledLogger) Debug(msg string, attrs ...any) {
	if l == nil {
		return
	}
	l.inner.Debug(msg, attrs...)
}

func (l *LeveledLogger) Info(msg string, attrs ...any) {
	if l == nil {
		return
	}
	l.inner.Info(msg, attrs...)
}

func (l *LeveledLogger) Warn(msg string, attrs ...any) {
	if l == nil {
		return
	}
	l.inner.Warn(msg, attrs...)
}

func (l *LeveledLogger) Error(msg string, attrs ...any) {
	if l == nil {
		return
	}
	l.inner.Error(msg, attrs...)
}

// ParseLevel converts a configuration string ("debug", "info", "warn",
// "error") into a slog level. The empty string means info, so a config that
// omits log_level keeps the previous default behaviour.
func ParseLevel(name string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unknown log level %q (want debug, info, warn or error)", name)
	}
}

// Metrics is intentionally small. Concrete exporters can adapt it to Prometheus
// or another backend without leaking vendor types into core.
type Metrics interface {
	IncCounter(name string, labels ...string)
	SetGauge(name string, value float64, labels ...string)
	ObserveHistogram(name string, value float64, labels ...string)
}

type nopMetrics struct{}

func NopMetrics() Metrics                                      { return nopMetrics{} }
func (nopMetrics) IncCounter(string, ...string)                {}
func (nopMetrics) SetGauge(string, float64, ...string)         {}
func (nopMetrics) ObserveHistogram(string, float64, ...string) {}

// Tracer is a minimal span factory for protocol/runtime instrumentation.
type Tracer interface {
	Start(ctx context.Context, name string, attrs ...any) (context.Context, Span)
}

type Span interface {
	End()
	RecordError(error)
}

type nopTracer struct{}
type nopSpan struct{}

func NopTracer() Tracer { return nopTracer{} }
func (nopTracer) Start(ctx context.Context, _ string, _ ...any) (context.Context, Span) {
	return ctx, nopSpan{}
}
func (nopSpan) End()              {}
func (nopSpan) RecordError(error) {}

type StageTimeouts struct {
	StopAccept    time.Duration
	Drain         time.Duration
	CloseSessions time.Duration
	Finalize      time.Duration
}
