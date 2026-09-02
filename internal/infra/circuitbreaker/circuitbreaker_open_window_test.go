package circuitbreaker

import (
	"errors"
	"testing"
	"time"
)

// TestBreakerOpenWindowNotExtendedByLateFailures verifies that failures
// reported while the breaker is already Open do not refresh openedAt, which
// would otherwise postpone the half-open probe indefinitely for callers that
// report failures without going through Allow/Execute.
func TestBreakerOpenWindowNotExtendedByLateFailures(t *testing.T) {
	b := New(2, 30*time.Millisecond)
	errBoom := errors.New("boom")

	for i := 0; i < 2; i++ {
		_ = b.Execute(func() error { return errBoom })
	}
	if b.State() != Open {
		t.Fatal("breaker should be open after threshold failures")
	}
	first := b.Snapshot()
	if first.OpenedAt.IsZero() {
		t.Fatal("openedAt should be set")
	}

	// Late failures after the breaker opened must not move the window.
	for i := 0; i < 5; i++ {
		time.Sleep(2 * time.Millisecond)
		b.Failure()
	}
	second := b.Snapshot()
	if !second.OpenedAt.Equal(first.OpenedAt) {
		t.Fatalf("openedAt moved from %v to %v; late failures extended the open window", first.OpenedAt, second.OpenedAt)
	}

	// The window still elapses and a probe is allowed afterwards.
	deadline := time.Now().Add(time.Second)
	for b.Allow() != nil {
		if time.Now().After(deadline) {
			t.Fatal("breaker never reached half-open")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if b.State() != HalfOpen {
		t.Fatalf("state = %v, want half-open", b.State())
	}
}
