package circuitbreaker

import (
	"errors"
	"sync"
	"time"
)

var ErrOpen = errors.New("circuit breaker open")

type State string

const (
	Closed   State = "closed"
	Open     State = "open"
	HalfOpen State = "half-open"
)

type Breaker struct {
	mu             sync.Mutex
	state          State
	failures       int
	threshold      int
	openedAt       time.Time
	timeout        time.Duration
	halfOpenActive bool
}

type Snapshot struct {
	State     State
	Failures  int
	Threshold int
	OpenedAt  time.Time
	Timeout   time.Duration
}

func New(threshold int, timeout time.Duration) *Breaker {
	if threshold <= 0 {
		threshold = 1
	}
	if timeout <= 0 {
		timeout = time.Second
	}
	return &Breaker{state: Closed, threshold: threshold, timeout: timeout}
}

func (b *Breaker) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == Open {
		if time.Since(b.openedAt) >= b.timeout {
			b.state = HalfOpen
		} else {
			return ErrOpen
		}
	}
	if b.state == HalfOpen {
		if b.halfOpenActive {
			return ErrOpen
		}
		b.halfOpenActive = true
	}
	return nil
}

func (b *Breaker) Success() {
	b.mu.Lock()
	b.failures = 0
	b.state = Closed
	b.halfOpenActive = false
	b.mu.Unlock()
}

func (b *Breaker) Failure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	b.halfOpenActive = false
	if b.failures >= b.threshold {
		b.state = Open
		b.openedAt = time.Now()
	}
}

func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

func (b *Breaker) Snapshot() Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return Snapshot{
		State:     b.state,
		Failures:  b.failures,
		Threshold: b.threshold,
		OpenedAt:  b.openedAt,
		Timeout:   b.timeout,
	}
}

func (b *Breaker) Execute(fn func() error) error {
	if err := b.Allow(); err != nil {
		return err
	}
	if err := fn(); err != nil {
		b.Failure()
		return err
	}
	b.Success()
	return nil
}
