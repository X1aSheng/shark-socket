package circuitbreaker

import (
	"errors"
	"testing"
	"time"
)

func TestCircuitBreakerTransitions(t *testing.T) {
	b := New(2, 10*time.Millisecond)
	b.Failure()
	if b.State() != Closed {
		t.Fatalf("state = %s, want closed", b.State())
	}
	b.Failure()
	if !errors.Is(b.Allow(), ErrOpen) {
		t.Fatal("breaker did not open")
	}
	time.Sleep(20 * time.Millisecond)
	if err := b.Allow(); err != nil {
		t.Fatalf("allow half-open: %v", err)
	}
	b.Success()
	if b.State() != Closed {
		t.Fatalf("state = %s, want closed", b.State())
	}
}
