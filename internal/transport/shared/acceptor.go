package shared

import (
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

// RejectReason says why a connection attempt was refused. It is returned rather
// than logged because the caller knows which transport it is and can attribute
// the refusal to the right metric.
type RejectReason uint8

const (
	AcceptOK RejectReason = iota
	// RejectCapacity means the server-wide connection ceiling is reached.
	RejectCapacity
	// RejectRate means the accept rate limiter had no token.
	RejectRate
	// RejectPeer means this one peer is at its own ceiling while the server has
	// room. See PeerQuota.
	RejectPeer
)

func (r RejectReason) String() string {
	switch r {
	case AcceptOK:
		return "ok"
	case RejectCapacity:
		return "capacity"
	case RejectRate:
		return "rate"
	case RejectPeer:
		return "peer"
	}
	return "unknown"
}

// Acceptor provides rate-limited and capacity-bounded connection acceptance.
//
// Capacity and the per-peer ceiling share one PeerQuota, so "how many sockets
// are open" and "how many of them are this peer's" are the same count seen two
// ways: there is no way for the two to drift apart, and Done cannot release one
// without the other.
type Acceptor struct {
	quota     *PeerQuota
	rate      float64
	lastCheck atomic.Int64 // UnixNano
	mu        sync.Mutex   // guards allowance and rate check atomicity
	allowance float64
}

// NewAcceptor creates an Acceptor with optional limits. Zero or negative values
// mean unlimited on that dimension.
func NewAcceptor(maxConns int64, acceptRate float64, maxPerPeer int64) *Acceptor {
	a := &Acceptor{
		quota:     NewPeerQuota(maxConns, maxPerPeer),
		rate:      acceptRate,
		allowance: acceptRate,
	}
	a.lastCheck.Store(time.Now().UnixNano())
	return a
}

// TryAccept reports whether a new connection from peer is allowed under the
// connection ceiling, the per-peer ceiling and the accept rate limit.
//
// Capacity is checked first so a connection rejected by the ceiling does not
// consume a rate-limit token: consuming one would pollute the limiter's
// accounting and throttle the next legitimate connection for no reason.
func (a *Acceptor) TryAccept(peer netip.Addr) RejectReason {
	if a == nil {
		// A transport whose limits are all zero still constructs an Acceptor,
		// but a handler reached without Start must not panic on a nil one.
		return AcceptOK
	}
	switch a.quota.Acquire(peer) {
	case QuotaFull:
		return RejectCapacity
	case QuotaPeerFull:
		return RejectPeer
	}
	if a.rate <= 0 {
		return AcceptOK
	}
	a.mu.Lock()
	now := time.Now().UnixNano()
	last := a.lastCheck.Swap(now)
	elapsed := float64(now-last) / float64(time.Second)
	// A backwards clock step (NTP correction) must not drain the bucket:
	// a negative elapsed would leave the allowance negative and stall
	// acceptance until it recovered.
	if elapsed < 0 {
		elapsed = 0
	}
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
		// Release the slot taken above.
		a.quota.Release(peer)
		return RejectRate
	}
	return AcceptOK
}

// Done releases the slot taken by a successful TryAccept.
//
// It is safe on a nil acceptor: the transports release the slot from deferred
// cleanup that also runs on paths that never took one.
func (a *Acceptor) Done(peer netip.Addr) {
	if a == nil {
		return
	}
	a.quota.Release(peer)
}

// Active returns the current active connection count.
func (a *Acceptor) Active() int64 {
	if a == nil {
		return 0
	}
	return a.quota.Total()
}

// PeerActive returns how many connections peer currently holds.
func (a *Acceptor) PeerActive(peer netip.Addr) int64 {
	if a == nil {
		return 0
	}
	return a.quota.PeerCount(peer)
}
