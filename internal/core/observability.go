package core

import (
	"context"
	"log/slog"
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

// ConfigSnapshot is the immutable runtime configuration view owned by Gateway.
type ConfigSnapshot struct {
	Shutdown StageTimeouts
	Started  time.Time
}

type StageTimeouts struct {
	StopAccept    time.Duration
	Drain         time.Duration
	CloseSessions time.Duration
	Finalize      time.Duration
}
