package circuitbreaker

import (
	"errors"
	"testing"
	"time"
)

func TestNew_CircuitStartsClosed(t *testing.T) {
	cb := New(3, 5*time.Second)

	if s := cb.State(); s != Closed {
		t.Fatalf("expected Closed, got %v", s)
	}
}

func TestThreshold_ReachesOpen(t *testing.T) {
	cb := New(3, 5*time.Second)

	failErr := errors.New("fail")
	for i := 0; i < 3; i++ {
		if err := cb.Do(func() error { return failErr }); err != failErr {
			t.Fatalf("call %d: expected %v, got %v", i, failErr, err)
		}
	}

	if s := cb.State(); s != Open {
		t.Fatalf("expected Open after threshold failures, got %v", s)
	}
}

func TestDo_ReturnsErrCircuitOpen_WhenOpen(t *testing.T) {
	cb := New(1, 10*time.Second)

	// Trigger Open state with one failure.
	_ = cb.Do(func() error { return errors.New("boom") })

	err := cb.Do(func() error { return nil })
	if err != ErrCircuitOpen {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestHalfOpen_TransitionAfterTimeout(t *testing.T) {
	cb := New(1, 50*time.Millisecond)

	// Open the circuit.
	_ = cb.Do(func() error { return errors.New("boom") })
	if cb.State() != Open {
		t.Fatal("expected Open")
	}

	// Wait for timeout to elapse.
	time.Sleep(100 * time.Millisecond)

	// The next call should transition to HalfOpen and execute the function.
	var executed bool
	err := cb.Do(func() error {
		executed = true
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error in half-open probe, got %v", err)
	}
	if !executed {
		t.Fatal("expected function to be executed in half-open state")
	}
}

func TestHalfOpen_SuccessTransitionsToClosed(t *testing.T) {
	cb := New(1, 50*time.Millisecond)

	// Open the circuit.
	_ = cb.Do(func() error { return errors.New("boom") })

	// Wait for timeout.
	time.Sleep(100 * time.Millisecond)

	// Successful probe should transition to Closed.
	_ = cb.Do(func() error { return nil })

	if s := cb.State(); s != Closed {
		t.Fatalf("expected Closed after successful half-open call, got %v", s)
	}
	if f := cb.Failures(); f != 0 {
		t.Fatalf("expected 0 failures after reset, got %d", f)
	}
}

func TestHalfOpen_FailureTransitionsBackToOpen(t *testing.T) {
	cb := New(1, 50*time.Millisecond)

	// Open the circuit.
	_ = cb.Do(func() error { return errors.New("boom") })

	// Wait for timeout so it enters HalfOpen.
	time.Sleep(100 * time.Millisecond)

	// Failed probe should transition back to Open.
	_ = cb.Do(func() error { return errors.New("still failing") })

	if s := cb.State(); s != Open {
		t.Fatalf("expected Open after failed half-open call, got %v", s)
	}
}

func TestReset_ForcesBackToClosed(t *testing.T) {
	cb := New(1, 10*time.Second)

	// Open the circuit.
	_ = cb.Do(func() error { return errors.New("boom") })
	if cb.State() != Open {
		t.Fatal("expected Open")
	}

	cb.Reset()

	if s := cb.State(); s != Closed {
		t.Fatalf("expected Closed after Reset, got %v", s)
	}
	if f := cb.Failures(); f != 0 {
		t.Fatalf("expected 0 failures after Reset, got %d", f)
	}
}
