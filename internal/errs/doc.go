// Package errs provides a comprehensive error taxonomy for the shark-socket framework.
//
// This package defines all framework-level errors with semantic categorization
// for proper error handling and classification.
//
// # Error Categories
//
//	┌──────────────────────────────────────────────────────────────┐
//	│                    Error Hierarchy                            │
//	├──────────────────────────────────────────────────────────────┤
//	│  System         │ Server lifecycle (closed, not started)      │
//	│  Session        │ Session management (not found, capacity)     │
//	│  Message        │ Message processing (too large, invalid)     │
//	│  Timeout        │ Time-based failures (read, write, idle)     │
//	│  Protocol       │ Protocol-specific errors                     │
//	│  Plugin         │ Plugin control flow (skip, drop, block)     │
//	│  Security       │ Security rejections (rate, ban, blacklist)  │
//	│  Resource       │ Resource exhaustion (FD, memory, overload)   │
//	│  Infrastructure │ External dependencies (cache, store, etc.)  │
//	│  Gateway        │ Multi-protocol gateway errors                │
//	│  Encoding       │ Message encoding/decoding failures          │
//	└──────────────────────────────────────────────────────────────┘
//
// # System Errors
//
// Errors related to server lifecycle:
//
//	ErrServerClosed     // Server.Shutdown() was called
//	ErrServerNotStarted // Server.Start() not called
//	ErrListenFailed    // net.Listen or tls.Listen failed
//
// # Session Errors
//
// Errors related to session management:
//
//	ErrSessionNotFound   // SessionManager.Get() found no session
//	ErrSessionClosed     // Session already closed
//	ErrSessionCapacity   // Session count at maximum
//	ErrDuplicateSession  // Session ID already registered
//	ErrSessionLimit      // Global session limit reached
//
// # Message Errors
//
// Errors related to message processing:
//
//	ErrMessageTooLarge   // Payload exceeds MaxMessageSize
//	ErrInvalidMessage   // Malformed or unprocessable message
//	ErrWriteQueueFull   // Session write queue at capacity
//	ErrInvalidFrame     // Corrupt or malformed frame
//	ErrFrameTooLarge    // Frame exceeds protocol limit
//
// # Timeout Errors
//
// Time-based failures:
//
//	ErrReadTimeout      // No data received within ReadTimeout
//	ErrWriteTimeout     // Write did not complete within timeout
//	ErrIdleTimeout     // Connection idle beyond IdleTimeout
//	ErrHeartbeatTimeout // Heartbeat not received within timeout
//
// # Protocol Errors
//
// Protocol-specific validation failures:
//
//	ErrCoAPInvalidMessage  // Invalid CoAP message format
//	ErrUnsupportedVersion  // Unsupported protocol version
//
// # Plugin Control Errors
//
// These are NOT actual errors — they control plugin chain execution:
//
//	ErrSkip   // Skip remaining plugins, continue to handler
//	ErrDrop   // Silently discard the message
//	ErrBlock  // Reject the connection immediately
//
// Use IsPluginControl() to identify these control flow signals.
//
// # Security Errors
//
// Security-related rejection errors:
//
//	ErrRateLimited          // Rate limit exceeded
//	ErrBlacklisted         // IP/CIDR in blacklist
//	ErrAutoBanned          // Automatically banned (threshold)
//	ErrMessageRateExceeded // Message rate limit hit
//
// # Resource Errors
//
// Resource exhaustion conditions:
//
//	ErrResourceExhausted // General resource exhaustion
//	ErrFDLimit          // File descriptor limit reached
//	ErrMemoryLimit      // Memory limit exceeded
//	ErrOverloaded       // Server overloaded, rejecting requests
//
// # Infrastructure Errors
//
// External dependency failures:
//
//	ErrCacheMiss        // Key not found in cache
//	ErrStoreNotFound   // Key not found in store
//	ErrPubSubClosed    // PubSub subscription closed
//	ErrCircuitOpen     // Circuit breaker is open
//	ErrInfrastructure  // Generic infrastructure failure
//	ErrDegraded        // Service operating in degraded mode
//
// # Gateway Errors
//
// Multi-protocol gateway errors:
//
//	ErrNoServerRegistered  // No servers registered with gateway
//	ErrDuplicateProtocol   // Protocol already registered
//	ErrGracefulShutdown   // Graceful shutdown signal (handler can react)
//
// # Encoding Errors
//
// Errors in the SendTyped path:
//
//	ErrEncodeFailure  // Message encoding failed
//	ErrDecodeFailure  // Message decoding failed
//
// # Classification Functions
//
// The package provides classification functions for error handling:
//
//	IsRetryable(err)          // May succeed on retry (queue full, circuit open)
//	IsFatal(err)              // Permanent failure (closed session/server)
//	IsRecoverable(err)       // May auto-recover (circuit open, infra)
//	IsSecurityRejection(err)  // Security layer rejection
//	IsPluginControl(err)      // Plugin control flow signal
//
// # Usage Examples
//
// Error handling with classification:
//
//	err := handler.Process(msg)
//	switch {
//	case errs.IsFatal(err):
//	    log.Fatal("fatal error:", err)
//	case errs.IsSecurityRejection(err):
//	    log.Warn("security rejection:", err)
//	case errs.IsRetryable(err):
//	    return retry()
//	default:
//	    log.Error("unexpected error:", err)
//	}
//
// Plugin control flow:
//
//	// In a plugin's OnMessage
//	out, err := plugin.Process(data)
//	if errs.IsPluginControl(err) {
//	    return nil, err  // Propagate control signal
//	}
//	return out, nil
//
// # Error Wrapping
//
// Use errors.Is() for proper error chain checking:
//
//	// Wrap errors with context
//	fmt.Errorf("tcp session: %w", errs.ErrSessionClosed)
//
//	// Check wrapped errors
//	if errors.Is(err, errs.ErrWriteQueueFull) {
//	    // Handle queue full
//	}
//
// # Design Principles
//
//  1. Errors are sentinels (var ErrX = errors.New(...))
//     Allows errors.Is() comparisons without imports
//
//  2. Semantic categorization enables proper handling
//     Not just "error occurred" but "what kind of error"
//
//  3. Plugin control via errors (not separate return)
//     Maintains Go error handling conventions
//
//  4. Recovery suggestions via classification
//     Callers know whether to retry, fail, or degrade
package errs
