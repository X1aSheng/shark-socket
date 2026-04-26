package circuitbreaker

import (
	"sync/atomic"
	"time"
)

// State represents the circuit breaker state.
type State int32

const (
	Closed   State = 0
	Open     State = 1
	HalfOpen State = 2
)

func (s State) String() string {
	switch s {
	case Closed:
		return "Closed"
	case Open:
		return "Open"
	case HalfOpen:
		return "HalfOpen"
	default:
		return "Unknown"
	}
}

// CircuitBreaker implements the circuit breaker pattern.
type CircuitBreaker struct {
	state     atomic.Int32
	failures  atomic.Int64
	threshold int64
	timeout   time.Duration
	openAt    atomic.Int64
}

// New creates a new CircuitBreaker.
func New(threshold int64, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		threshold: threshold,
		timeout:   timeout,
	}
}

// State returns the current state.
func (cb *CircuitBreaker) State() State {
	return State(cb.state.Load())
}

// Do executes fn through the circuit breaker.
func (cb *CircuitBreaker) Do(fn func() error) error {
	current := State(cb.state.Load())

	switch current {
	case Open:
		if time.Since(time.Unix(0, cb.openAt.Load())) > cb.timeout {
			cb.state.CompareAndSwap(int32(Open), int32(HalfOpen))
		} else {
			return ErrCircuitOpen
		}
	case HalfOpen:
		// allow one probe call
	}

	err := fn()
	if err != nil {
		cb.onFailure()
	} else {
		cb.onSuccess()
	}
	return err
}

func (cb *CircuitBreaker) onSuccess() {
	cb.failures.Store(0)
	cb.state.Store(int32(Closed))
}

func (cb *CircuitBreaker) onFailure() {
	f := cb.failures.Add(1)
	if f >= cb.threshold {
		if cb.state.CompareAndSwap(int32(Closed), int32(Open)) {
			cb.openAt.Store(time.Now().UnixNano())
		}
	}
	if State(cb.state.Load()) == HalfOpen {
		cb.state.Store(int32(Open))
		cb.openAt.Store(time.Now().UnixNano())
	}
}

// Failures returns the current consecutive failure count.
func (cb *CircuitBreaker) Failures() int64 {
	return cb.failures.Load()
}

// Reset forces the circuit breaker back to Closed state.
func (cb *CircuitBreaker) Reset() {
	cb.failures.Store(0)
	cb.state.Store(int32(Closed))
}
