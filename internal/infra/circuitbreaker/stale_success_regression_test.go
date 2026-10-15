package circuitbreaker

import (
	"testing"
	"time"
)

// TestSuccessFromOpenDoesNotCloseBreaker verifies that a stale success cannot
// close an open breaker. A request admitted while Closed can finish after a
// later failure opened the breaker; that late success must not skip the open
// window, otherwise the protection is defeated by any in-flight request.
func TestSuccessFromOpenDoesNotCloseBreaker(t *testing.T) {
	b := New(1, time.Hour)

	// Admit a request while the breaker is closed.
	if err := b.Allow(); err != nil {
		t.Fatalf("Allow while closed: %v", err)
	}
	// A concurrent request fails and trips the breaker.
	b.Failure()
	if b.State() != Open {
		t.Fatalf("state = %v, want open", b.State())
	}
	// The first request now completes successfully.
	b.Success()
	if b.State() != Open {
		t.Fatalf("state after stale Success = %v, want it to stay open", b.State())
	}
	if err := b.Allow(); err != ErrOpen {
		t.Fatalf("Allow after stale Success = %v, want ErrOpen", err)
	}
}

// TestSuccessClosesHalfOpenBreaker verifies the legitimate path: a successful
// probe in the half-open state closes the breaker.
func TestSuccessClosesHalfOpenBreaker(t *testing.T) {
	b := New(1, time.Millisecond)
	b.Failure()
	if b.State() != Open {
		t.Fatalf("state = %v, want open", b.State())
	}
	time.Sleep(5 * time.Millisecond)
	if err := b.Allow(); err != nil {
		t.Fatalf("Allow after timeout: %v", err)
	}
	if b.State() != HalfOpen {
		t.Fatalf("state = %v, want half-open", b.State())
	}
	b.Success()
	if b.State() != Closed {
		t.Fatalf("state = %v, want closed after a successful probe", b.State())
	}
}
