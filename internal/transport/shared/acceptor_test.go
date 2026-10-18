package shared

import (
	"net/netip"
	"testing"
	"time"
)

var (
	peerA = netip.MustParseAddr("203.0.113.7")
	peerB = netip.MustParseAddr("198.51.100.9")
)

func TestAcceptorUnlimited(t *testing.T) {
	a := NewAcceptor(0, 0, 0)
	for i := 0; i < 1000; i++ {
		if got := a.TryAccept(peerA); got != AcceptOK {
			t.Fatalf("unlimited acceptor rejected at %d with %v", i, got)
		}
	}
}

func TestAcceptorMaxConns(t *testing.T) {
	a := NewAcceptor(3, 0, 0)
	for i := 0; i < 3; i++ {
		if got := a.TryAccept(peerA); got != AcceptOK {
			t.Fatalf("accept %d = %v, want ok", i, got)
		}
	}
	if got := a.TryAccept(peerB); got != RejectCapacity {
		t.Fatalf("4th accept = %v, want capacity", got)
	}
	a.Done(peerA)
	if got := a.TryAccept(peerB); got != AcceptOK {
		t.Fatalf("accept after Done = %v, want ok", got)
	}
}

// TestAcceptorPerPeerLimit is A5: one peer must not be able to take every slot.
func TestAcceptorPerPeerLimit(t *testing.T) {
	a := NewAcceptor(10, 0, 2)
	if got := a.TryAccept(peerA); got != AcceptOK {
		t.Fatalf("1st for peer A = %v, want ok", got)
	}
	if got := a.TryAccept(peerA); got != AcceptOK {
		t.Fatalf("2nd for peer A = %v, want ok", got)
	}
	if got := a.TryAccept(peerA); got != RejectPeer {
		t.Fatalf("3rd for peer A = %v, want peer", got)
	}
	// The whole point: peer B still has the other eight slots.
	if got := a.TryAccept(peerB); got != AcceptOK {
		t.Fatalf("peer B = %v, want ok while peer A is at its ceiling", got)
	}
	a.Done(peerA)
	if got := a.TryAccept(peerA); got != AcceptOK {
		t.Fatalf("peer A after Done = %v, want ok", got)
	}
	if got := a.Active(); got != 3 {
		t.Fatalf("Active() = %d, want 3", got)
	}
}

// TestAcceptorPeerLimitDoesNotLeakOnRateReject: a rejection by the rate limiter
// must give the per-peer slot back, or a throttled peer would slowly fill its
// own quota with nothing to show for it.
func TestAcceptorPeerLimitDoesNotLeakOnRateReject(t *testing.T) {
	a := NewAcceptor(10, 1, 1) // one token, one per second, one per peer
	if got := a.TryAccept(peerA); got != AcceptOK {
		t.Fatalf("first = %v, want ok", got)
	}
	if got := a.TryAccept(peerA); got != RejectPeer {
		t.Fatalf("second = %v, want peer (checked before the rate limiter)", got)
	}
	if got := a.PeerActive(peerA); got != 1 {
		t.Fatalf("PeerActive = %d, want 1", got)
	}
	if got := a.Active(); got != 1 {
		t.Fatalf("Active = %d, want 1", got)
	}
}

func TestAcceptorRateLimit(t *testing.T) {
	a := NewAcceptor(0, 10, 0) // 10 per second, bucket starts full
	accepted, rejected := int64(0), 0
	for i := 0; i < 101; i++ {
		switch a.TryAccept(peerA) {
		case AcceptOK:
			accepted++
		case RejectRate:
			rejected++
		}
	}
	if accepted == 0 || rejected == 0 {
		t.Fatalf("accepted, rejected = %d, %d; want both to be non-zero", accepted, rejected)
	}
	// A rate rejection must not leave the connection counted: Active has to be
	// exactly the number of connections that were actually admitted.
	if got := a.Active(); got != accepted {
		t.Fatalf("Active() = %d after %d accepts and %d rejections, want %d", got, accepted, rejected, accepted)
	}
}

func TestAcceptorRateLimitRefills(t *testing.T) {
	a := NewAcceptor(0, 100, 0) // 100 per second
	for i := 0; i < 100; i++ {
		if got := a.TryAccept(peerA); got != AcceptOK {
			t.Fatalf("accept %d = %v, want ok", i, got)
		}
	}
	if got := a.TryAccept(peerA); got != RejectRate {
		t.Fatalf("after draining the bucket = %v, want rate", got)
	}
	time.Sleep(50 * time.Millisecond)
	if got := a.TryAccept(peerA); got != AcceptOK {
		t.Fatalf("after refill = %v, want ok", got)
	}
}

func TestAcceptorSubOneRate(t *testing.T) {
	a := NewAcceptor(0, 0.5, 0) // 1 connection every 2 seconds
	// Right after creation allowance is 0.5 (< 1), so a burst accept is rejected.
	if got := a.TryAccept(peerA); got != RejectRate {
		t.Fatalf("accept with fewer than one token = %v, want rate", got)
	}
	// Wait for the bucket to refill to a full token.
	time.Sleep(1100 * time.Millisecond)
	if got := a.TryAccept(peerA); got != AcceptOK {
		t.Fatalf("after the bucket refilled to one token = %v, want ok", got)
	}
	// Token consumed; an immediate re-accept must be rejected until it refills.
	if got := a.TryAccept(peerA); got != RejectRate {
		t.Fatalf("second accept before refill = %v, want rate", got)
	}
}

func TestAcceptorActive(t *testing.T) {
	a := NewAcceptor(10, 0, 0)
	for i := 0; i < 5; i++ {
		a.TryAccept(peerA)
	}
	if a.Active() != 5 {
		t.Fatalf("Active() = %d, want 5", a.Active())
	}
	a.Done(peerA)
	a.Done(peerA)
	if a.Active() != 3 {
		t.Fatalf("Active() = %d, want 3", a.Active())
	}
}

func TestRejectReasonString(t *testing.T) {
	cases := map[RejectReason]string{
		AcceptOK:        "ok",
		RejectCapacity:  "capacity",
		RejectRate:      "rate",
		RejectPeer:      "peer",
		RejectReason(9): "unknown",
	}
	for r, want := range cases {
		if got := r.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", r, got, want)
		}
	}
}
