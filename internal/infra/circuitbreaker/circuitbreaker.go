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
	mu        sync.Mutex
	state     State
	failures  int
	threshold int
	openedAt  time.Time
	timeout   time.Duration
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
			return nil
		}
		return ErrOpen
	}
	return nil
}

func (b *Breaker) Success() {
	b.mu.Lock()
	b.failures = 0
	b.state = Closed
	b.mu.Unlock()
}

func (b *Breaker) Failure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
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
