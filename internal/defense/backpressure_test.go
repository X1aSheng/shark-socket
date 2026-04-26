package defense

import (
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// BackpressureController
// ---------------------------------------------------------------------------

func TestCheckQueueLevel_OK(t *testing.T) {
	bc := NewBackpressureController(0.8, 1.0)
	if got := bc.CheckQueueLevel(5, 100); got != "ok" {
		t.Fatalf("expected ok, got %q", got)
	}
}

func TestCheckQueueLevel_Warn(t *testing.T) {
	bc := NewBackpressureController(0.8, 1.0)
	if got := bc.CheckQueueLevel(85, 100); got != "warn" {
		t.Fatalf("expected warn, got %q", got)
	}
}

func TestCheckQueueLevel_Close(t *testing.T) {
	bc := NewBackpressureController(0.8, 1.0)
	if got := bc.CheckQueueLevel(100, 100); got != "close" {
		t.Fatalf("expected close, got %q", got)
	}
}

func TestCheckQueueLevel_ZeroCapacity(t *testing.T) {
	bc := NewBackpressureController(0.8, 1.0)
	if got := bc.CheckQueueLevel(10, 0); got != "ok" {
		t.Fatalf("expected ok for zero capacity, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// BroadcastLimiter
// ---------------------------------------------------------------------------

func TestBroadcastLimiter_Allow(t *testing.T) {
	bl := NewBroadcastLimiter(1024, 50*time.Millisecond)
	if !bl.Allow("topic1", 512) {
		t.Fatal("expected first broadcast to be allowed")
	}
}

func TestBroadcastLimiter_DenyOversized(t *testing.T) {
	bl := NewBroadcastLimiter(1024, 50*time.Millisecond)
	if bl.Allow("topic1", 2048) {
		t.Fatal("expected oversized broadcast to be denied")
	}
}

func TestBroadcastLimiter_EnforcesMinInterval(t *testing.T) {
	bl := NewBroadcastLimiter(1024, 200*time.Millisecond)

	if !bl.Allow("topic1", 100) {
		t.Fatal("first call should be allowed")
	}
	if bl.Allow("topic1", 100) {
		t.Fatal("second call within minInterval should be denied")
	}

	time.Sleep(250 * time.Millisecond)

	if !bl.Allow("topic1", 100) {
		t.Fatal("call after minInterval should be allowed")
	}
}

func TestBroadcastLimiter_DifferentTopics(t *testing.T) {
	bl := NewBroadcastLimiter(1024, 200*time.Millisecond)

	if !bl.Allow("a", 10) {
		t.Fatal("topic a should be allowed")
	}
	if !bl.Allow("b", 10) {
		t.Fatal("topic b should be allowed independently")
	}
}
