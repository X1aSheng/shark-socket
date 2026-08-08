package shared

import (
	"testing"
	"time"
)

func TestAcceptorUnlimited(t *testing.T) {
	a := NewAcceptor(0, 0)
	for i := 0; i < 1000; i++ {
		if !a.TryAccept() {
			t.Fatalf("unlimited acceptor rejected at %d", i)
		}
	}
}

func TestAcceptorMaxConns(t *testing.T) {
	a := NewAcceptor(3, 0)
	if !a.TryAccept() || !a.TryAccept() || !a.TryAccept() {
		t.Fatal("first 3 accepts should succeed")
	}
	if a.TryAccept() {
		t.Fatal("4th accept should be rejected")
	}
	a.Done()
	if !a.TryAccept() {
		t.Fatal("accept should succeed after Done()")
	}
}

func TestAcceptorRateLimit(t *testing.T) {
	a := NewAcceptor(0, 10) // 10 per second
	// First accept should succeed
	if !a.TryAccept() {
		t.Fatal("first accept should succeed")
	}
	// Subsequent rapid accepts should be rejected
	rejected := 0
	for i := 0; i < 100; i++ {
		if !a.TryAccept() {
			rejected++
		}
	}
	if rejected == 0 {
		t.Fatal("expected rate limiting to kick in")
	}
}

func TestAcceptorRateLimitRefills(t *testing.T) {
	a := NewAcceptor(0, 100) // 100 per second
	// Consume all tokens
	for i := 0; i < 100; i++ {
		if !a.TryAccept() {
			t.Fatalf("accept %d should have succeeded", i)
		}
	}
	// Should be empty now
	if a.TryAccept() {
		t.Fatal("should be rate limited")
	}
	// Wait for refill
	time.Sleep(50 * time.Millisecond)
	// Should have some tokens back
	if !a.TryAccept() {
		t.Fatal("should have refilled at least 1 token after 50ms")
	}
}

func TestAcceptorSubOneRate(t *testing.T) {
	a := NewAcceptor(0, 0.5) // 1 connection every 2 seconds
	// Right after creation allowance is 0.5 (< 1), so a burst accept is rejected.
	if a.TryAccept() {
		t.Fatal("accept with fewer than one token should be rejected")
	}
	// Wait for the bucket to refill to a full token.
	time.Sleep(1100 * time.Millisecond)
	if !a.TryAccept() {
		t.Fatal("should accept after the bucket refilled to one token")
	}
	// Token consumed; an immediate re-accept must be rejected until it refills.
	if a.TryAccept() {
		t.Fatal("second accept before refill should be rejected")
	}
}

func TestAcceptorActive(t *testing.T) {
	a := NewAcceptor(10, 0)
	for i := 0; i < 5; i++ {
		a.TryAccept()
	}
	if a.Active() != 5 {
		t.Fatalf("Active() = %d, want 5", a.Active())
	}
	a.Done()
	a.Done()
	if a.Active() != 3 {
		t.Fatalf("Active() = %d, want 3", a.Active())
	}
}
