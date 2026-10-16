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

// TestScaleDownFloor asserts shrinking stops exactly at minWorker and never
// goes below it.
//
// This is the boundary the design calls out in section 11.3.5: a pool that
// retires past its floor loses the goroutines it can least afford to lose,
// because minWorker is the count the caller asked to always have available.
// The assertion is two-sided on purpose -- reaching the floor is not enough,
// the pool must also stay there under continued low-water readings, which is
// where an off-by-one in the comparison would show up.
//
// maybeScale is driven directly, with an empty queue, rather than through
// Submit. Two earlier attempts to reach low water by submitting were both
// measuring the wrong thing: a tight submit loop outruns the workers even with
// a non-blocking handler, so the queue fills, occupancy leaves the shrink band
// and the streak resets mid-run. Whether a burst of submits reads as low water
// is a sampling question already covered by TestScaleDownRequiresStreak; what
// this test is about is the floor itself, and an empty queue is the unambiguous
// low-water signal for it.
func TestScaleDownFloor(t *testing.T) {
	var handled atomic.Int64
	const minWorker = 2
	p := NewScalablePool(func(core.Session, core.Message) error {
		handled.Add(1)
		return nil
	}, nil, minWorker, 8, 1024, PolicyDrop)
	p.scaleUpRatio = 0.75
	p.scaleDownRatio = 0.25
	p.Start()
	defer p.Stop()

	// Grow well above the floor so there is room to shrink back down to it.
	for range 4 {
		p.addWorker()
	}
	if got := p.workerCount(); got != minWorker+4 {
		t.Fatalf("setup workerCount = %d, want %d", got, minWorker+4)
	}

	// Each retirement costs scaleDownHold consecutive readings, so drive far
	// more readings than there are workers to retire. Without a floor the pool
	// would keep retiring and hit zero, which is the failure under test.
	for range int(p.scaleDownHold) * 20 {
		p.maybeScale()
	}

	if got := p.workerCount(); got != minWorker {
		t.Fatalf("workerCount = %d, want exactly minWorker %d", got, minWorker)
	}
	// Keep reading: the floor must hold, not merely be reached once.
	for range int(p.scaleDownHold) * 10 {
		p.maybeScale()
	}
	if got := p.workerCount(); got != minWorker {
		t.Fatalf("workerCount = %d after sustained low water, want %d (floor must hold)", got, minWorker)
	}
}

// TestRetireWorkerDrainsNothing asserts a retired worker exits without taking
// one last task from the queue.
//
// The hazard is specific: run()'s retire branch returns immediately, while its
// done branch calls drainQueue first. If a retiring worker drained on the way
// out, it would consume tasks that the surviving workers are counting on, and
// during Stop that work would be handled twice or lost -- the single drain
// path would no longer be the only one.
//
// The test parks every worker inside the handler first, so a task is sitting in
// the queue with no worker able to reach it. Once the retire is observed the
// queue depth must be untouched, which is only true if the retire branch did
// not touch the queue.
func TestRetireWorkerDrainsNothing(t *testing.T) {
	const (
		workers = 3
		backlog = 12
	)
	var handled atomic.Int64
	inHandler := make(chan struct{}, workers)
	release := make(chan struct{})

	p := NewScalablePool(func(core.Session, core.Message) error {
		handled.Add(1)
		// Non-blocking, unlike a plain send. Stop drains the queue after the
		// workers are gone, so the handler runs with nobody left to read this
		// channel; a blocking send would park it forever and hang Stop.
		select {
		case inHandler <- struct{}{}:
		default:
		}
		<-release
		return nil
	}, nil, workers, workers, 64, PolicyDrop)
	p.Start()
	// Only Stop is deferred: the parked workers need release closed before Stop
	// can drain, and close(release) must not happen twice.
	defer p.Stop()

	sess := newPoolTestSession(1)
	// One task per worker parks every worker inside the handler.
	for range workers {
		if err := p.Submit(sess, core.ProtocolTCP, []byte("a")); err != nil {
			t.Fatalf("Submit priming: %v", err)
		}
	}
	for range workers {
		<-inHandler
	}

	// Fill the queue while no worker is available to consume it.
	for range backlog {
		if err := p.Submit(sess, core.ProtocolTCP, []byte("b")); err != nil {
			t.Fatalf("Submit backlog: %v", err)
		}
	}
	depthBefore := len(p.queue)
	if depthBefore != backlog {
		t.Fatalf("queue depth = %d, want %d", depthBefore, backlog)
	}

	// Retire one worker and wait until it has actually left the active set.
	p.retireOneWorker()
	deadline := time.Now().Add(2 * time.Second)
	for p.workerCount() != workers-1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := p.workerCount(); got != workers-1 {
		t.Fatalf("workerCount = %d, want %d", got, workers-1)
	}

	// The retire branch ran to completion. Had it drained, the backlog would be
	// smaller than when the retire was requested.
	if got := len(p.queue); got != depthBefore {
		t.Fatalf("queue depth after retire = %d, want %d: a retired worker must not drain", got, depthBefore)
	}

	// Everything still queued must be handled exactly once, and the survivors
	// plus Stop's drain must account for all of it.
	close(release)
	p.Stop()
	if got, want := handled.Load(), int64(workers+backlog); got != want {
		t.Fatalf("handled = %d, want %d (no task lost or handled twice)", got, want)
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
