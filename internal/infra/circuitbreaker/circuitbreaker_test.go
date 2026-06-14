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

func TestCircuitBreakerExecute(t *testing.T) {
	b := New(1, time.Minute)
	errBoom := errors.New("boom")
	if err := b.Execute(func() error { return errBoom }); !errors.Is(err, errBoom) {
		t.Fatalf("execute error = %v, want %v", err, errBoom)
	}
	if b.State() != Open {
		t.Fatalf("state = %s, want open", b.State())
	}
	if err := b.Execute(func() error { return nil }); !errors.Is(err, ErrOpen) {
		t.Fatalf("execute while open = %v, want %v", err, ErrOpen)
	}
}

func TestCircuitBreakerHalfOpenAllowsSingleProbe(t *testing.T) {
	b := New(1, 10*time.Millisecond)
	b.Failure()
	time.Sleep(20 * time.Millisecond)
	if err := b.Allow(); err != nil {
		t.Fatalf("first half-open probe: %v", err)
	}
	if err := b.Allow(); !errors.Is(err, ErrOpen) {
		t.Fatalf("second half-open probe = %v, want %v", err, ErrOpen)
	}
	b.Success()
	if b.State() != Closed {
		t.Fatalf("state = %s, want closed", b.State())
	}
}

func TestCircuitBreakerSnapshot(t *testing.T) {
	b := New(2, time.Second)
	b.Failure()
	snap := b.Snapshot()
	if snap.State != Closed || snap.Failures != 1 || snap.Threshold != 2 {
		t.Fatalf("snapshot = %#v", snap)
	}
}

func TestNewCircuitBreakerDefaultConfig(t *testing.T) {
	cb := New(3, time.Second)
	if cb == nil {
		t.Fatal("circuit breaker should not be nil")
	}
}

func TestNewCircuitBreakerCustomConfig(t *testing.T) {
	cb := New(5, 500*time.Millisecond)
	if cb == nil {
		t.Fatal("circuit breaker should not be nil")
	}
}
