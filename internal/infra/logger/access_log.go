package logger

import (
	"context"
	"time"
)

// AccessLogEntry represents a single access log entry.
type AccessLogEntry struct {
	Timestamp   time.Time
	Protocol    string
	Method      string
	Path        string
	StatusCode  int
	BytesIn    int64
	BytesOut   int64
	Duration   time.Duration
	ClientIP   string
	UserAgent  string
	RequestID  string
	Error      error
}

// LogAccess logs an access entry using the configured access logger.
func (l *slogLogger) LogAccess(entry AccessLogEntry) {
	if l.inner == nil {
		return
	}

	args := []any{
		"timestamp", entry.Timestamp.Format(time.RFC3339),
		"protocol", entry.Protocol,
		"method", entry.Method,
		"path", entry.Path,
		"status", entry.StatusCode,
		"bytes_in", entry.BytesIn,
		"bytes_out", entry.BytesOut,
		"duration_ms", entry.Duration.Milliseconds(),
		"client_ip", entry.ClientIP,
	}

	if entry.RequestID != "" {
		args = append(args, "request_id", entry.RequestID)
	}
	if entry.UserAgent != "" {
		args = append(args, "user_agent", entry.UserAgent)
	}
	if entry.Error != nil {
		args = append(args, "error", entry.Error.Error())
	}

	level := "INFO"
	if entry.StatusCode >= 500 {
		level = "ERROR"
	} else if entry.StatusCode >= 400 {
		level = "WARN"
	}

	switch level {
	case "ERROR":
		l.inner.Error("access", args...)
	case "WARN":
		l.inner.Warn("access", args...)
	default:
		l.inner.Info("access", args...)
	}
}

// AccessLogMiddleware creates a middleware-style access logger plugin.
// It wraps the next handler and logs all requests.
type AccessLogMiddleware struct {
	logger AccessLogger
}

// NewAccessLogMiddleware creates a new access log middleware.
func NewAccessLogMiddleware(logger AccessLogger) *AccessLogMiddleware {
	return &AccessLogMiddleware{logger: logger}
}

// WithAccessLogger configures the access logger for HTTP/WebSocket protocols.
func WithAccessLogger(logger AccessLogger) Option {
	return func(o *options) {
		if o.accessLogger == nil {
			o.accessLogger = logger
		}
	}
}

// RequestIDFromContext retrieves the request ID from context.
func RequestIDFromContext(ctx context.Context) string {
	if v := ctx.Value(requestIDKeyType); v != nil {
		if id, ok := v.(string); ok {
			return id
		}
	}
	return ""
}

// AccessLogOption configures access logging behavior.
type AccessLogOption func(*accessLogConfig)

type accessLogConfig struct {
	excludePaths  []string
	includeBody   bool // reserved for future body logging feature
	slowThreshold time.Duration
}

// ExcludePaths sets paths to exclude from access logging.
func ExcludePaths(paths ...string) AccessLogOption {
	return func(c *accessLogConfig) {
		c.excludePaths = append(c.excludePaths, paths...)
	}
}

// SlowThreshold sets the threshold for slow request logging.
// Requests taking longer than this duration will be logged at WARN level.
func SlowThreshold(d time.Duration) AccessLogOption {
	return func(c *accessLogConfig) {
		c.slowThreshold = d
	}
}

// ShouldExcludePath checks if a path should be excluded from logging.
func ShouldExcludePath(path string, excludePaths []string) bool {
	for _, p := range excludePaths {
		if p == path {
			return true
		}
	}
	return false
}