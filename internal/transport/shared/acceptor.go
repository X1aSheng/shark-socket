package shared

import (
	"sync"
	"sync/atomic"
	"time"
)

// Acceptor provides rate-limited and capacity-bounded connection acceptance.
type Acceptor struct {
	maxConns  int64
	active    atomic.Int64
	rate      float64
	lastCheck atomic.Int64 // UnixNano
	mu        sync.Mutex   // guards allowance and rate check atomicity
	allowance float64
}

// NewAcceptor creates an Acceptor with optional limits. Zero values mean unlimited.
func NewAcceptor(maxConns int64, acceptRate float64) *Acceptor {
	a := &Acceptor{maxConns: maxConns, rate: acceptRate, allowance: acceptRate}
	a.lastCheck.Store(time.Now().UnixNano())
	return a
}

// TryAccept returns true if a new connection is allowed under both the
// concurrent connection cap and the accept rate limit.
func (a *Acceptor) TryAccept() bool {
	if a.rate > 0 {
		a.mu.Lock()
		now := time.Now().UnixNano()
		last := a.lastCheck.Swap(now)
		elapsed := float64(now-last) / float64(time.Second)
		// The bucket must be able to hold at least one token, otherwise a rate
		// below 1/s (e.g. 0.5 = one connection every two seconds) could never
		// reach the >= 1 token threshold below and would reject every connection.
		burst := a.rate
		if burst < 1 {
			burst = 1
		}
		a.allowance += elapsed * a.rate
		if a.allowance > burst {
			a.allowance = burst
		}
		ok := a.allowance >= 1
		if ok {
			a.allowance--
		}
		a.mu.Unlock()
		if !ok {
			return false
		}
	}
	// Atomic capacity check + increment: a plain Load-then-Add lets concurrent
	// callers (e.g. simultaneous WebSocket upgrades) exceed maxConns by the
	// number of in-flight callers.
	for {
		cur := a.active.Load()
		if a.maxConns > 0 && cur >= a.maxConns {
			return false
		}
		if a.active.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

// Done decrements the active connection count.
func (a *Acceptor) Done() {
	a.active.Add(-1)
}

// Active returns the current active connection count.
func (a *Acceptor) Active() int64 {
	return a.active.Load()
}
