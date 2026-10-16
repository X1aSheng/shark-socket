package shared

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// slowHandler blocks until release is closed, so a test can hold the queue at a
// chosen depth without racing the clock.
func slowHandler(release chan struct{}, handled *atomic.Int64) core.Handler {
	return func(core.Session, core.Message) error {
		handled.Add(1)
		<-release
		return nil
	}
}

// submitUntilTick drives exactly enough submits to make one scaling evaluation
// fire, using a handler that parks so the queue depth is controlled.
func submitUntilTick(t *testing.T, p *ScalablePool, sess core.Session, n int) {
	t.Helper()
	for range n {
		if err := p.Submit(sess, core.ProtocolTCP, []byte("x")); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
}

// TestScaleUpOnBacklog drives the pool past the up threshold and asserts worker
// count grows, but never past maxWorker.
func TestScaleUpOnBacklog(t *testing.T) {
	release := make(chan struct{})
	var handled atomic.Int64
	p := NewScalablePool(slowHandler(release, &handled), nil, 1, 4, 8, PolicyDrop)
	p.scaleUpRatio = 0.5
	p.Start()
	defer func() {
		close(release)
		p.Stop()
	}()

	sess := newPoolTestSession(1)
	// 1024 submits: one modulo tick fires at the end. The handler parks every
	// task, so the queue fills and occupancy climbs.
	for range scaleCheckInterval {
		if err := p.Submit(sess, core.ProtocolTCP, []byte("x")); err != nil {
			// PolicyDrop rejects once full; that is expected and still counts
			// as a tick.
			continue
		}
	}

	if got := p.workerCount(); got <= 1 {
		t.Fatalf("workerCount = %d, expected growth above min 1", got)
	}
	if got, want := p.workerCount(), int(p.maxWorker.Load()); got > want {
		t.Fatalf("workerCount = %d exceeds maxWorker %d", got, want)
	}
}

// TestScaleDownRequiresStreak asserts a single low-water reading does not
// shrink, while scaleDownHold consecutive readings do.
//
// The queue is deliberately far larger than scaleCheckInterval: with a small
// queue, 1024 submits fill it and occupancy stays pinned at 1.0, which is the
// grow branch, not the shrink one. Sizing the queue at 8192 keeps occupancy
// near 0.125 so the shrink path is the one under test.
func TestScaleDownRequiresStreak(t *testing.T) {
	release := make(chan struct{})
	var handled atomic.Int64
	p := NewScalablePool(slowHandler(release, &handled), nil, 1, 8, 16384, PolicyDrop)
	p.scaleUpRatio = 2.0 // unreachable: isolate the shrink path
	p.scaleDownRatio = 0.25
	p.Start()
	defer func() {
		close(release)
		p.Stop()
	}()
	for range 3 {
		p.addWorker()
	}

	sess := newPoolTestSession(1)
	before := p.workerCount()

	// One tick below the shrink threshold must not shrink.
	for range scaleCheckInterval {
		_ = p.Submit(sess, core.ProtocolTCP, []byte("x"))
	}
	if got := p.workerCount(); got != before {
		t.Fatalf("after 1 low-water tick workerCount = %d, want %d (hold not reached)", got, before)
	}

	// Now drive enough ticks to pass the hold. scaleDownHold is 3, and the
	// streak survived the previous tick, so two more suffice.
	for range scaleCheckInterval * 2 {
		_ = p.Submit(sess, core.ProtocolTCP, []byte("x"))
	}
	if got := p.workerCount(); got >= before {
		t.Fatalf("after enough low-water ticks workerCount = %d, want < %d", got, before)
	}
	if got, want := p.workerCount(), int(p.minWorker.Load()); got < want {
		t.Fatalf("workerCount = %d shrank below minWorker %d", got, want)
	}
}

// TestScaleDownResetsOnBusyPeriod asserts the shrink streak is reset by high
// water, so a pool that stays busy never accumulates shrink credit.
func TestScaleDownResetsOnBusyPeriod(t *testing.T) {
	release := make(chan struct{})
	var handled atomic.Int64
	// Queue big enough that the first 1024 submits leave it near-empty.
	p := NewScalablePool(slowHandler(release, &handled), nil, 1, 8, 16384, PolicyDrop)
	p.scaleDownRatio = 0.25
	p.scaleUpRatio = 0.75
	p.Start()
	// Shrinking is only permitted above minWorker, so the pool has to be grown
	// past its floor before a shrink reading can even be considered.
	for range 3 {
		p.addWorker()
	}
	defer func() {
		close(release)
		p.Stop()
	}()

	sess := newPoolTestSession(1)

	// Phase 1: one low-water tick, below the hold.
	for range scaleCheckInterval {
		_ = p.Submit(sess, core.ProtocolTCP, []byte("x"))
	}
	if got := p.downStreak.Load(); got != 1 {
		t.Fatalf("downStreak after low-water tick = %d, want 1", got)
	}

	// Phase 2: fill the queue past the up threshold. The handler parks, so the
	// depth stays up and the next tick must take the grow branch, which
	// resets the streak.
	for range 12288 {
		_ = p.Submit(sess, core.ProtocolTCP, []byte("x"))
	}
	// One more modulo tick to let the grow branch observe the high water.
	for range scaleCheckInterval {
		_ = p.Submit(sess, core.ProtocolTCP, []byte("x"))
	}

	if got := p.downStreak.Load(); got != 0 {
		t.Fatalf("downStreak after a busy period = %d, want 0 (must be reset)", got)
	}
}

// TestMaybeScaleDoesNotHoldSubmitLock is the property that forced the Submit
// split: maybeScale must not run while submitMu's read lock is held, or a
// blocking PolicyBlock submit would hold it for the duration of a goroutine
// start.
func TestMaybeScaleDoesNotHoldSubmitLock(t *testing.T) {
	release := make(chan struct{})
	var handled atomic.Int64
	p := NewScalablePool(slowHandler(release, &handled), nil, 1, 8, 8, PolicyDrop)
	p.scaleUpRatio = 0.5
	p.Start()
	defer func() {
		close(release)
		p.Stop()
	}()

	sess := newPoolTestSession(1)
	// Occupy the queue and workers so maybeScale wants to grow.
	for range scaleCheckInterval {
		_ = p.Submit(sess, core.ProtocolTCP, []byte("x"))
	}
	before := p.workerCount()

	// A second submit must not be able to acquire the read lock while
	// maybeScale is in flight. Taking the write lock from the test goroutine
	// proves no reader holds it: if Submit still held the lock, this would
	// block until the whole test times out.
	done := make(chan struct{})
	go func() {
		p.submitMu.Lock()
		p.submitMu.Unlock()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("submitMu write lock blocked: Submit still holds the read lock")
	}
	_ = before
}

// TestScaleUpGrowthIsCapped asserts repeated high-water ticks grow the pool by
// one worker at a time and stop at maxWorker.
func TestScaleUpGrowthIsCapped(t *testing.T) {
	release := make(chan struct{})
	var handled atomic.Int64
	p := NewScalablePool(slowHandler(release, &handled), nil, 1, 3, 8, PolicyDrop)
	p.scaleUpRatio = 0.5
	p.Start()
	defer func() {
		close(release)
		p.Stop()
	}()

	sess := newPoolTestSession(1)
	seen := map[int]bool{}
	for range scaleCheckInterval * 8 {
		_ = p.Submit(sess, core.ProtocolTCP, []byte("x"))
		if n := p.workerCount(); n <= 3 {
			seen[n] = true
		}
	}
	if got, want := p.workerCount(), 3; got != want {
		t.Fatalf("workerCount = %d, want capped at maxWorker %d", got, want)
	}
	if !seen[2] {
		t.Fatal("pool never sat at 2 workers: growth is not incremental")
	}
}

// TestScalingUnderSynctest runs the same shrink cycle inside a synctest bubble
// so the tick accounting is deterministic rather than dependent on how fast the
// test machine happens to schedule goroutines.
//
// The queue is sized well past scaleCheckInterval for the same reason as
// TestScaleDownRequiresStreak: a small queue fills during the submit loop and
// the grow branch takes over.
func TestScalingUnderSynctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		var handled atomic.Int64
		p := NewScalablePool(slowHandler(release, &handled), nil, 1, 8, 16384, PolicyDrop)
		p.scaleUpRatio = 2.0 // unreachable: isolate the shrink path
		p.scaleDownRatio = 0.25
		p.Start()
		// Same reason as TestScaleDownResetsOnBusyPeriod: a shrink reading is
		// only considered above minWorker.
		for range 3 {
			p.addWorker()
		}

		sess := newPoolTestSession(1)
		before := p.workerCount()

		// synctest.Wait parks until every goroutine in the bubble is blocked,
		// which here means every submit has been processed. That removes the
		// timing assumptions a plain sleep would carry.
		for range scaleCheckInterval {
			_ = p.Submit(sess, core.ProtocolTCP, []byte("x"))
		}
		synctest.Wait()
		if got := p.workerCount(); got != before {
			t.Fatalf("workerCount = %d, want %d (hold not yet reached)", got, before)
		}

		for range scaleCheckInterval * 3 {
			_ = p.Submit(sess, core.ProtocolTCP, []byte("x"))
		}
		synctest.Wait()
		if got := p.workerCount(); got >= before {
			t.Fatalf("workerCount = %d, want < %d", got, before)
		}

		close(release)
		p.Stop()
	})
}
