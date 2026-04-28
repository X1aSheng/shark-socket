package errs

import "errors"

// System errors.
var (
	ErrServerClosed     = errors.New("shark: server closed")
	ErrServerNotStarted = errors.New("shark: server not started")
	ErrListenFailed     = errors.New("shark: listen failed")
)

// Session errors.
var (
	ErrSessionNotFound  = errors.New("shark: session not found")
	ErrSessionClosed    = errors.New("shark: session closed")
	ErrSessionCapacity  = errors.New("shark: session capacity exceeded")
	ErrDuplicateSession = errors.New("shark: duplicate session")
	ErrSessionLimit     = errors.New("shark: session limit reached")
)

// Message errors.
var (
	ErrMessageTooLarge = errors.New("shark: message too large")
	ErrInvalidMessage  = errors.New("shark: invalid message")
	ErrWriteQueueFull  = errors.New("shark: write queue full")
	ErrInvalidFrame    = errors.New("shark: invalid frame")
	ErrFrameTooLarge   = errors.New("shark: frame too large")
)

// Timeout errors.
var (
	ErrReadTimeout      = errors.New("shark: read timeout")
	ErrWriteTimeout     = errors.New("shark: write timeout")
	ErrIdleTimeout      = errors.New("shark: idle timeout")
	ErrHeartbeatTimeout = errors.New("shark: heartbeat timeout")
)

// Protocol errors.
var (
	ErrCoAPInvalidMessage = errors.New("shark: coap invalid message")
	ErrUnsupportedVersion = errors.New("shark: unsupported version")
)

// Plugin control errors (not real errors — control plugin chain behavior).
var (
	ErrSkip            = errors.New("shark: plugin skip")
	ErrDrop            = errors.New("shark: plugin drop")
	ErrBlock           = errors.New("shark: plugin block")
	ErrPluginRejected  = errors.New("shark: plugin rejected")
	ErrPluginDuplicate = errors.New("shark: plugin duplicate")
)

// Security errors.
var (
	ErrRateLimited         = errors.New("shark: rate limited")
	ErrBlacklisted         = errors.New("shark: ip blacklisted")
	ErrAutoBanned          = errors.New("shark: auto banned")
	ErrMessageRateExceeded = errors.New("shark: message rate exceeded")
)

// Resource errors.
var (
	ErrResourceExhausted = errors.New("shark: resource exhausted")
	ErrFDLimit           = errors.New("shark: file descriptor limit")
	ErrMemoryLimit       = errors.New("shark: memory limit")
	ErrOverloaded        = errors.New("shark: server overloaded")
)

// Infrastructure errors.
var (
	ErrCacheMiss      = errors.New("shark: cache miss")
	ErrStoreNotFound  = errors.New("shark: store not found")
	ErrPubSubClosed   = errors.New("shark: pubsub closed")
	ErrCircuitOpen    = errors.New("shark: circuit breaker open")
	ErrInfrastructure = errors.New("shark: infrastructure error")
	ErrDegraded       = errors.New("shark: service degraded")
)

// Gateway errors.
var (
	ErrNoServerRegistered = errors.New("shark: no server registered")
	ErrDuplicateProtocol  = errors.New("shark: duplicate protocol")
	ErrGracefulShutdown   = errors.New("shark: graceful shutdown")
)

// Encoding errors (SendTyped path).
var (
	ErrEncodeFailure = errors.New("shark: encode failure")
	ErrDecodeFailure = errors.New("shark: decode failure")
)

// IsRetryable returns true for errors where the operation may succeed on retry.
func IsRetryable(err error) bool {
	return errors.Is(err, ErrWriteQueueFull) ||
		errors.Is(err, ErrCircuitOpen)
}

// IsFatal returns true for errors that indicate a permanent failure.
func IsFatal(err error) bool {
	return errors.Is(err, ErrSessionClosed) ||
		errors.Is(err, ErrServerClosed)
}

// IsRecoverable returns true for errors that may resolve automatically.
func IsRecoverable(err error) bool {
	return errors.Is(err, ErrCircuitOpen) ||
		errors.Is(err, ErrInfrastructure)
}

// IsSecurityRejection returns true for security-related rejection errors.
func IsSecurityRejection(err error) bool {
	return errors.Is(err, ErrBlacklisted) ||
		errors.Is(err, ErrAutoBanned) ||
		errors.Is(err, ErrRateLimited)
}

// IsPluginControl returns true for plugin chain control flow "errors".
func IsPluginControl(err error) bool {
	return errors.Is(err, ErrSkip) ||
		errors.Is(err, ErrDrop) ||
		errors.Is(err, ErrBlock)
}
