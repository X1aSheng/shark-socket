package defense

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestNewOverloadProtector(t *testing.T) {
	var count int64
	op := NewOverloadProtector(100, 50, func() int64 {
		return atomic.LoadInt64(&count)
	})
	if op == nil {
		t.Fatal("expected non-nil OverloadProtector")
	}
}

func TestIsOverloaded_InitiallyFalse(t *testing.T) {
	op := NewOverloadProtector(100, 50, func() int64 { return 0 })
	if op.IsOverloaded() {
		t.Fatal("expected IsOverloaded to be false initially")
	}
}

func TestHighWater_TriggersOverload(t *testing.T) {
	var count int64
	op := NewOverloadProtector(100, 50, func() int64 {
		return atomic.LoadInt64(&count)
	})
	op.checkInterval = 10 * time.Millisecond
	op.Start()
	defer op.Stop()

	// Exceed high watermark.
	atomic.StoreInt64(&count, 110)

	// Wait for check loop to detect.
	time.Sleep(50 * time.Millisecond)

	if !op.IsOverloaded() {
		t.Fatal("expected IsOverloaded to be true after exceeding high water")
	}
}

func TestLowWater_RecoversFromOverload(t *testing.T) {
	var count int64
	op := NewOverloadProtector(100, 50, func() int64 {
		return atomic.LoadInt64(&count)
	})
	op.checkInterval = 10 * time.Millisecond
	op.Start()
	defer op.Stop()

	// Trigger overload.
	atomic.StoreInt64(&count, 110)
	time.Sleep(50 * time.Millisecond)
	if !op.IsOverloaded() {
		t.Fatal("expected overload after high water")
	}

	// Drop below low watermark.
	atomic.StoreInt64(&count, 30)
	time.Sleep(50 * time.Millisecond)

	if op.IsOverloaded() {
		t.Fatal("expected IsOverloaded to be false after dropping below low water")
	}
}

func TestStop_StopsCheckLoop(t *testing.T) {
	var count int64
	op := NewOverloadProtector(100, 50, func() int64 {
		return atomic.LoadInt64(&count)
	})
	op.checkInterval = 10 * time.Millisecond
	op.Start()

	// Stop should not panic.
	op.Stop()

	// Changing count after Stop should not trigger overload (loop is dead).
	atomic.StoreInt64(&count, 999)
	time.Sleep(30 * time.Millisecond)
	if op.IsOverloaded() {
		t.Fatal("expected no overload change after Stop")
	}
}
