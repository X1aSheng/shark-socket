package shared

import (
	"context"
	"testing"
	"time"
)

// TestBackoffDelayScheduleGrowsAndCaps verifies the delay doubles from min and
// never exceeds max. This is the mechanism that keeps a persistently failing
// accept/read loop from spinning the CPU.
func TestBackoffDelayScheduleGrowsAndCaps(t *testing.T) {
	const (
		min = 2 * time.Millisecond
		max = 16 * time.Millisecond
	)
	b := NewBackoff(min, max)
	want := []time.Duration{2, 4, 8, 16, 16, 16}
	for i, w := range want {
		if !b.Wait(context.Background()) {
			t.Fatalf("Wait %d returned false on a live context", i)
		}
		if got := b.Delay(); got != w*time.Millisecond {
			t.Fatalf("step %d: delay = %v, want %v", i, got, w*time.Millisecond)
		}
	}
}

// TestBackoffResetClearsDelay verifies a successful operation restores the
// minimum delay immediately.
func TestBackoffResetClearsDelay(t *testing.T) {
	b := NewBackoff(2*time.Millisecond, 32*time.Millisecond)
	for i := 0; i < 3; i++ {
		b.Wait(context.Background())
	}
	if b.Delay() <= 2*time.Millisecond {
		t.Fatalf("delay did not grow before reset: %v", b.Delay())
	}
	b.Reset()
	if got := b.Delay(); got != 0 {
		t.Fatalf("after Reset delay = %v, want 0", got)
	}
	if !b.Wait(context.Background()) {
		t.Fatal("Wait returned false on a live context")
	}
	if got := b.Delay(); got != 2*time.Millisecond {
		t.Fatalf("after Reset the delay restarted at %v, want 2ms", got)
	}
}

// TestBackoffWaitAbortsOnContextCancel verifies a cancelled context unblocks
// Wait so a shutting-down server does not linger in a backoff sleep.
func TestBackoffWaitAbortsOnContextCancel(t *testing.T) {
	b := NewBackoff(5*time.Second, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if b.Wait(ctx) {
		t.Fatal("Wait returned true for a cancelled context")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Wait did not abort promptly on cancel: took %v", elapsed)
	}
}

// TestNewBackoffClampsInvalidBounds verifies the guard cannot be disabled by a
// zero or inverted bound.
func TestNewBackoffClampsInvalidBounds(t *testing.T) {
	for _, tc := range []struct {
		name     string
		min, max time.Duration
		wantMin  time.Duration
		wantMax  time.Duration
	}{
		{"zero-zero", 0, 0, DefaultBackoffMin, DefaultBackoffMax},
		{"negative", -time.Second, -time.Second, DefaultBackoffMin, DefaultBackoffMax},
		{"max-below-min", time.Second, time.Millisecond, time.Second, time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBackoff(tc.min, tc.max)
			if b.min != tc.wantMin || b.max != tc.wantMax {
				t.Fatalf("bounds = (%v, %v), want (%v, %v)", b.min, b.max, tc.wantMin, tc.wantMax)
			}
		})
	}
}
