// Package logger provides structured logging abstractions for shark-socket.
//
// This package defines the Logger interface and built-in implementations for
// framework logging. All framework components log through this interface,
// enabling flexibility in production deployments.
//
// # Logger Interface
//
//	type Logger interface {
//	    Debug(msg string, args ...any)
//	    Info(msg string, args ...any)
//	    Warn(msg string, args ...any)
//	    Error(msg string, args ...any)
//	    With(args ...any) Logger
//	    WithContext(ctx context.Context) Logger
//	}
//
// # Log Levels
//
// | Level | Use | Example |
// |-------|-----|---------|
// | Debug | Frame parsing, plugin execution | "reading frame: 1024 bytes" |
// | Info | Connection lifecycle, server events | "server started :18000" |
// | Warn | Rate limits, LRU eviction, retries | "rate limit exceeded ip=x.x.x.x" |
// | Error | Abnormal disconnections, panics | "connection closed: read timeout" |
//
// # Structured Logging
//
// The With() method adds structured context:
//
//	log := logger.Info("server started")
//	log = log.With("host", "0.0.0.0", "port", 18000)
//	log.Info("listener ready")
//
// Output (JSON format):
//
//	{"level":"INFO","msg":"listener ready","host":"0.0.0.0","port":18000}
//
// # Access Logger
//
// HTTP/WebSocket access logging uses a separate interface:
//
//	type AccessLogger interface {
//	    LogAccess(r *AccessLog)
//	}
//
//	AccessLog struct {
//	    Timestamp  time.Time
//	    Method     string
//	    Path       string
//	    Status     int
//	    Duration   time.Duration
//	    BytesIn    int64
//	    BytesOut   int64
//	    RemoteAddr string
//	    UserAgent  string
//	    RequestID  string
//	}
//
// # Built-in Implementations
//
// | Implementation | Use Case | Dependencies |
// |---------------|----------|--------------|
// | slogLogger | Production (Go 1.21+) | Standard library |
// | nopLogger | Benchmarks, tests | None |
// | zapLogger | High-performance needs | uber-go/zap |
//
// # Default Logger
//
// Default is slogLogger (Go 1.21+ JSON structured logging):
//
//	logger.SetDefault(logger.NewSlogLogger(os.Stderr, "json"))
//	log := logger.GetDefault()
//
// Can be replaced at runtime for testing or custom formats.
//
// # Context Propagation
//
// WithContext extracts trace_id and request_id from context:
//
//	log := logger.WithContext(ctx)
//	log.Info("processing request")
//	// Output: {"msg":"processing request","trace_id":"abc","request_id":"123"}
//
// Required context fields:
//   - trace_id: Distributed tracing identifier
//   - request_id: Per-request identifier
//
// # Key Field Conventions
//
// Framework logging uses consistent field names:
//
// | Field | Type | Description |
// |-------|------|-------------|
// | session_id | uint64 | Session identifier |
// | protocol | string | Protocol type |
// | remote_addr | string | Client address |
// | plugin_name | string | Plugin name in logs |
// | error | error | Error object |
// | duration_ms | float64 | Operation duration |
// | trace_id | string | Distributed trace ID |
// | request_id | string | Per-request ID |
// | msg_size | int | Message payload size |
// | queue_depth | int | Write queue depth |
//
// # Sampling
//
// High-frequency logs can be sampled:
//
//	import "github.com/X1aSheng/shark-socket/internal/defense/sampler"
//
//	sampler := sampler.NewLogSampler(time.Second)
//	for i := 0; i < 1000; i++ {
//	    sampler.Log("repeated message")
//	}
//	// Output: First log fully, subsequent logs summarized
//	// "repeated message [995 additional same messages]"
//
// This prevents log flooding from repeated events.
//
// # Async Writing
//
// Production deployments should use async logging:
//
//	asyncLogger := logger.NewAsyncLogger(
//	    logger.NewSlogLogger(os.Stderr, "json"),
//	    4096,  // buffer size
//	)
//	logger.SetDefault(asyncLogger)
//
// Benefits:
//   - Log writes don't block application threads
//   - Batched writes reduce I/O overhead
//   - Graceful degradation on queue full (sync fallback)
//
// # Security: Log Sanitization
//
// Framework sanitizes sensitive data in logs:
//
//  1. Payload truncation
//     Only first 64 bytes of message payload logged
//     Rest indicated with "...truncated"
//
//  2. Metadata filtering
//     Fields named "password", "token", "key" excluded
//
//  3. Error sanitization
//     Internal details not exposed in client-facing errors
//
// # Performance
//
// Logging performance comparison (per log call):
//
// | Implementation | Latency | Notes |
// |---------------|---------|-------|
// | slogLogger | ~1μs | Go 1.21+ built-in |
// | zapLogger | ~0.5μs | Optimized for speed |
// | nopLogger | ~10ns | No-op, benchmark use |
//
// For high-throughput servers, consider:
//  1. Sampling for debug logs
//  2. Async writer with buffer tuning
//  3. Selective field inclusion
//
// # Integration
//
// Framework components accept Logger via options:
//
//	import "github.com/X1aSheng/shark-socket/internal/infra/logger"
//
//	srv := tcp.NewServer(handler,
//	    tcp.WithLogger(myLogger),
//	)
//
// If no logger specified, default logger is used.
package logger
