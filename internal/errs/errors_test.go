package errs

import (
	"errors"
	"fmt"
	"testing"
)

func TestAllErrorsNonNil(t *testing.T) {
	errs := []error{
		ErrServerClosed, ErrServerNotStarted, ErrListenFailed,
		ErrSessionNotFound, ErrSessionClosed, ErrSessionCapacity, ErrSessionLimit,
		ErrMessageTooLarge, ErrInvalidMessage, ErrWriteQueueFull, ErrInvalidFrame, ErrFrameTooLarge,
		ErrReadTimeout, ErrWriteTimeout, ErrIdleTimeout, ErrHeartbeatTimeout,
		ErrCoAPInvalidMessage, ErrUnsupportedVersion,
		ErrSkip, ErrDrop, ErrBlock, ErrPluginRejected, ErrPluginDuplicate,
		ErrRateLimited, ErrBlacklisted, ErrAutoBanned, ErrMessageRateExceeded,
		ErrResourceExhausted, ErrFDLimit, ErrMemoryLimit, ErrOverloaded,
		ErrCacheMiss, ErrStoreNotFound, ErrPubSubClosed, ErrCircuitOpen, ErrInfrastructure, ErrDegraded,
		ErrNoServerRegistered, ErrDuplicateProtocol, ErrGracefulShutdown,
		ErrEncodeFailure, ErrDecodeFailure,
	}
	for _, e := range errs {
		if e == nil {
			t.Errorf("error should not be nil: %v", e)
		}
	}
}

func TestAllErrorsHaveSharkPrefix(t *testing.T) {
	errs := []error{
		ErrServerClosed, ErrSessionClosed, ErrWriteQueueFull,
		ErrSkip, ErrDrop, ErrBlock,
		ErrRateLimited, ErrBlacklisted,
		ErrCircuitOpen, ErrEncodeFailure,
	}
	for _, e := range errs {
		if e.Error()[:5] != "shark" {
			t.Errorf("error %q should have 'shark:' prefix", e)
		}
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{ErrWriteQueueFull, true},
		{ErrCircuitOpen, true},
		{ErrSessionClosed, false},
		{ErrBlacklisted, false},
		{fmt.Errorf("wrapped: %w", ErrWriteQueueFull), true},
		{fmt.Errorf("wrapped: %w", ErrCircuitOpen), true},
		{errors.New("other"), false},
		{nil, false},
	}
	for _, tt := range tests {
		if got := IsRetryable(tt.err); got != tt.want {
			t.Errorf("IsRetryable(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestIsFatal(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{ErrSessionClosed, true},
		{ErrServerClosed, true},
		{ErrWriteQueueFull, false},
		{fmt.Errorf("wrapped: %w", ErrSessionClosed), true},
		{fmt.Errorf("wrapped: %w", ErrServerClosed), true},
		{nil, false},
	}
	for _, tt := range tests {
		if got := IsFatal(tt.err); got != tt.want {
			t.Errorf("IsFatal(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestIsRecoverable(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{ErrCircuitOpen, true},
		{ErrInfrastructure, true},
		{ErrSessionClosed, false},
		{fmt.Errorf("wrapped: %w", ErrCircuitOpen), true},
		{nil, false},
	}
	for _, tt := range tests {
		if got := IsRecoverable(tt.err); got != tt.want {
			t.Errorf("IsRecoverable(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestIsSecurityRejection(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{ErrBlacklisted, true},
		{ErrAutoBanned, true},
		{ErrRateLimited, true},
		{ErrMessageRateExceeded, false},
		{ErrSessionClosed, false},
		{fmt.Errorf("wrapped: %w", ErrBlacklisted), true},
		{nil, false},
	}
	for _, tt := range tests {
		if got := IsSecurityRejection(tt.err); got != tt.want {
			t.Errorf("IsSecurityRejection(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestIsPluginControl(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{ErrSkip, true},
		{ErrDrop, true},
		{ErrBlock, true},
		{ErrPluginRejected, false},
		{ErrPluginDuplicate, false},
		{fmt.Errorf("wrapped: %w", ErrSkip), true},
		{fmt.Errorf("wrapped: %w", ErrDrop), true},
		{nil, false},
	}
	for _, tt := range tests {
		if got := IsPluginControl(tt.err); got != tt.want {
			t.Errorf("IsPluginControl(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}
