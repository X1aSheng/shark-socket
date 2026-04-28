package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// Level represents log level.
type Level int8

const (
	LevelDebug Level = -3
	LevelInfo  Level = 0
	LevelWarn  Level = 4
	LevelError Level = 8
)

// Option configures the Logger.
type Option func(*options)

type options struct {
	level          Level
	output         io.Writer
	addSource      bool
	timeFormat     string
	timeZone       *time.Location
	contextFuncs   []ContextExtractor
	accessLogger   AccessLogger
}

// AccessLogger logs access entries.
type AccessLogger interface {
	Log(entry AccessLogEntry)
}

// ContextExtractor extracts fields from context.
type ContextExtractor func(ctx context.Context) []any

// WithLevel sets the minimum log level.
func WithLevel(level Level) Option {
	return func(o *options) { o.level = level }
}

// WithOutput sets the output writer.
func WithOutput(w io.Writer) Option {
	return func(o *options) { o.output = w }
}

// WithSource adds source location (file:line) to log output.
func WithSource(addSource bool) Option {
	return func(o *options) { o.addSource = addSource }
}

// WithTimeFormat sets the time format for log timestamps.
func WithTimeFormat(format string) Option {
	return func(o *options) { o.timeFormat = format }
}

// WithTimeZone sets the time zone for log timestamps.
func WithTimeZone(tz *time.Location) Option {
	return func(o *options) { o.timeZone = tz }
}

// WithContextExtractor adds custom context field extractors.
func WithContextExtractor(fn ContextExtractor) Option {
	return func(o *options) { o.contextFuncs = append(o.contextFuncs, fn) }
}

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
	inner  *slog.Logger
	level  Level
	ctxFns []ContextExtractor
}

// NewSlogLogger creates a Logger backed by slog with JSON output.
func NewSlogLogger() Logger {
	return NewSlogLoggerWithOptions()
}

// NewSlogLoggerWithOptions creates a Logger with custom options.
func NewSlogLoggerWithOptions(opts ...Option) Logger {
	o := &options{
		level:    LevelInfo,
		output:   os.Stdout,
		addSource: false,
		timeFormat: "2006-01-02T15:04:05.000Z07:00",
	}
	for _, opt := range opts {
		opt(o)
	}

	handler := slog.NewJSONHandler(o.output, &slog.HandlerOptions{
		Level:     slog.Level(o.level),
		AddSource: o.addSource,
		ReplaceAttr: replaceSourcePath,
	})
	logger := slog.New(handler)
	return &slogLogger{
		inner:  logger,
		level:  o.level,
		ctxFns: o.contextFuncs,
	}
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
	return &slogLogger{
		inner:  l.inner.With(args...),
		level:  l.level,
		ctxFns: l.ctxFns,
	}
}

func (l *slogLogger) WithContext(ctx context.Context) Logger {
	if ctx == nil {
		return l
	}
	args := extractContextFields(ctx)
	// Run custom context extractors
	for _, fn := range l.ctxFns {
		if customArgs := fn(ctx); len(customArgs) > 0 {
			args = append(args, customArgs...)
		}
	}
	if len(args) > 0 {
		return &slogLogger{inner: l.inner.With(args...), level: l.level, ctxFns: l.ctxFns}
	}
	return l
}

const (
	traceIDKey    = "trace_id"
	requestIDKey = "request_id"
	userIDKey    = "user_id"
	sessionIDKey = "session_id"
	protocolKey  = "protocol"
)

type contextKey int

const (
	traceIDKeyType contextKey = iota
	requestIDKeyType
	userIDKeyType
	sessionIDKeyType
	protocolKeyType
)

func extractContextFields(ctx context.Context) []any {
	if ctx == nil {
		return nil
	}
	var args []any

	// Type-safe keys (priority)
	if v := ctx.Value(traceIDKeyType); v != nil {
		if traceID, ok := v.(string); ok && traceID != "" {
			args = append(args, traceIDKey, traceID)
		}
	}
	if v := ctx.Value(requestIDKeyType); v != nil {
		if requestID, ok := v.(string); ok && requestID != "" {
			args = append(args, requestIDKey, requestID)
		}
	}
	if v := ctx.Value(userIDKeyType); v != nil {
		if userID, ok := v.(string); ok && userID != "" {
			args = append(args, userIDKey, userID)
		}
	}
	if v := ctx.Value(sessionIDKeyType); v != nil {
		if sessionID, ok := v.(string); ok && sessionID != "" {
			args = append(args, sessionIDKey, sessionID)
		}
	}
	if v := ctx.Value(protocolKeyType); v != nil {
		if proto, ok := v.(string); ok && proto != "" {
			args = append(args, protocolKey, proto)
		}
	}

	// Legacy string-based keys for backward compatibility
	if len(args) == 0 {
		if traceID, ok := ctx.Value("trace_id").(string); ok {
			args = append(args, traceIDKey, traceID)
		}
		if requestID, ok := ctx.Value("request_id").(string); ok {
			args = append(args, requestIDKey, requestID)
		}
		if userID, ok := ctx.Value("user_id").(string); ok {
			args = append(args, userIDKey, userID)
		}
		if sessionID, ok := ctx.Value("session_id").(string); ok {
			args = append(args, sessionIDKey, sessionID)
		}
		if proto, ok := ctx.Value("protocol").(string); ok {
			args = append(args, protocolKey, proto)
		}
	}

	return args
}

// ContextWithTraceID creates a context with trace ID.
func ContextWithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKeyType, traceID)
}

// ContextWithRequestID creates a context with request ID.
func ContextWithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKeyType, requestID)
}

// ContextWithUserID creates a context with user ID.
func ContextWithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKeyType, userID)
}

// ContextWithSessionID creates a context with session ID.
func ContextWithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionIDKeyType, sessionID)
}

// ContextWithProtocol creates a context with protocol type.
func ContextWithProtocol(ctx context.Context, proto string) context.Context {
	return context.WithValue(ctx, protocolKeyType, proto)
}

// modulePrefix is stripped from slog source file paths to keep log output short.
const modulePrefix = "github.com/X1aSheng/shark-socket/"

func replaceSourcePath(groups []string, a slog.Attr) slog.Attr {
	if a.Key == slog.SourceKey {
		if src, ok := a.Value.Any().(*slog.Source); ok {
			if after, found := strings.CutPrefix(src.File, modulePrefix); found {
				src.File = after
			}
		}
	}
	return a
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

var defaultLogger atomic.Pointer[loggerHolder]

type loggerHolder struct{ l Logger }

func init() {
	defaultLogger.Store(&loggerHolder{l: NewSlogLogger()})
}

// SetDefault replaces the global default logger.
func SetDefault(l Logger) { defaultLogger.Store(&loggerHolder{l: l}) }

// GetDefault returns the global default logger.
func GetDefault() Logger { return defaultLogger.Load().l }
