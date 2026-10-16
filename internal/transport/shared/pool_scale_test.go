package shared

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// TestScalablePoolStartMinWorkers verifies the pool comes up at minWorker and
// processes work.
func TestScalablePoolStartMinWorkers(t *testing.T) {
	var handled atomic.Int64
	handler := func(core.Session, core.Message) error {
		handled.Add(1)
		return nil
	}
	p := NewScalablePool(handler, nil, 2, 4, 16, PolicyDrop)
	p.Start()
	defer p.Stop()

	if got := p.workerCount(); got != 2 {
		t.Fatalf("workerCount = %d, want 2", got)
	}

	sess := newPoolTestSession(1)
	for range 8 {
		if err := p.Submit(sess, core.ProtocolTCP, []byte("x")); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	p.Stop() // drain everything

	if handled.Load() != 8 {
		t.Fatalf("handled = %d, want 8", handled.Load())
	}
}

// TestScalablePoolNeverExceedsMax is the bound that keeps scaling from turning
// into unbounded goroutine growth.
func TestScalablePoolNeverExceedsMax(t *testing.T) {
	p := NewScalablePool(func(core.Session, core.Message) error { return nil }, nil, 1, 3, 8, PolicyDrop)
	defer p.Stop()

	p.Start()
	for range 50 {
		p.addWorker()
	}
	if got := p.workerCount(); got > 3 {
		t.Fatalf("workerCount = %d, must not exceed max 3", got)
	}
}

// TestScalablePoolNoAddAfterStop covers the race the design calls out: a
// concurrent addWorker must not resurrect the pool after Stop has begun
// draining, which would leak a goroutine nobody waits for.
func TestScalablePoolNoAddAfterStop(t *testing.T) {
	p := NewScalablePool(func(core.Session, core.Message) error { return nil }, nil, 2, 8, 8, PolicyDrop)
	p.Start()
	p.Stop()

	// After Stop the active set is empty and no new worker may appear.
	p.addWorker()
	if got := p.workerCount(); got != 0 {
		t.Fatalf("workerCount after Stop = %d, want 0", got)
	}
}

// TestScalablePoolStopConcurrentAdd hammers the Start/Stop/addWorker boundary.
// This is the exact window that would panic under a plain wg.Add(1)+go.
func TestScalablePoolStopConcurrentAdd(t *testing.T) {
	for range 30 {
		p := NewScalablePool(func(core.Session, core.Message) error { return nil }, nil, 2, 8, 8, PolicyDrop)
		p.Start()

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 20 {
				p.addWorker()
			}
		}()
		go func() {
			defer wg.Done()
			p.Stop()
		}()
		wg.Wait()

		// A second Stop must be safe and must not hang.
		done := make(chan struct{})
		go func() { p.Stop(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("second Stop hung")
		}
		if got := p.workerCount(); got != 0 {
			t.Fatalf("workerCount after Stop = %d, want 0", got)
		}
	}
}

// TestScalablePoolRetireDoesNotConsumeQueue asserts a retiring worker stops
// taking work. If it kept consuming, the surviving workers would find the
// queue stripped and tasks could be handled twice or lost during Stop's
// drain.
func TestScalablePoolRetireDoesNotConsumeQueue(t *testing.T) {
	var handled atomic.Int64
	handler := func(core.Session, core.Message) error {
		handled.Add(1)
		return nil
	}
	// One worker: park it inside the handler so we control exactly when it
	// observes retire.
	p := NewScalablePool(handler, nil, 1, 1, 64, PolicyDrop)
	p.Start()

	blocked := make(chan struct{})
	release := make(chan struct{})
	p.handler = func(sess core.Session, msg core.Message) error {
		if handled.Add(1) == 1 {
			close(blocked)
		}
		<-release
		return nil
	}

	sess := newPoolTestSession(1)
	if err := p.Submit(sess, core.ProtocolTCP, []byte("a")); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	<-blocked // the single worker is now inside the handler

	// Queue additional tasks while the worker is parked.
	for range 10 {
		if err := p.Submit(sess, core.ProtocolTCP, []byte("b")); err != nil {
			t.Fatalf("Submit backlog: %v", err)
		}
	}

	p.mu.Lock()
	for id, w := range p.workers {
		close(w.retire)
		delete(p.workers, id)
		_ = id
	}
	p.mu.Unlock()
	close(release)

	// Let the retiring worker finish and exit.
	deadline := time.Now().Add(2 * time.Second)
	for p.workerCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := p.workerCount(); got != 0 {
		t.Fatalf("retired worker still active: %d", got)
	}

	// Drain whatever is left; every accepted task must be handled exactly once.
	p.Stop()
	if got := handled.Load(); got != 11 {
		t.Fatalf("handled = %d, want 11 (one in flight + ten queued)", got)
	}
}

// TestScalablePoolStopDrainsQueue verifies Stop is loss-free: every task that
// Submit accepted is eventually handled.
func TestScalablePoolStopDrainsQueue(t *testing.T) {
	var handled atomic.Int64
	p := NewScalablePool(func(core.Session, core.Message) error {
		handled.Add(1)
		return nil
	}, nil, 3, 8, 256, PolicyDrop)
	p.Start()

	sess := newPoolTestSession(1)
	const n = 200
	for range n {
		if err := p.Submit(sess, core.ProtocolTCP, []byte("x")); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	p.Stop()

	if got := handled.Load(); got != n {
		t.Fatalf("handled = %d, want %d", got, n)
	}
}

// TestScalablePoolSubmitAfterStopRejected confirms the closed latch is honoured
// on the elastic path too.
func TestScalablePoolSubmitAfterStopRejected(t *testing.T) {
	p := NewScalablePool(func(core.Session, core.Message) error { return nil }, nil, 1, 2, 8, PolicyDrop)
	p.Start()
	p.Stop()

	sess := newPoolTestSession(1)
	if err := p.Submit(sess, core.ProtocolTCP, []byte("x")); err != core.ErrClosed {
		t.Fatalf("Submit after Stop = %v, want ErrClosed", err)
	}
}

// TestScalablePoolDisabledWithoutHandler mirrors WorkerPool.Enabled: a pool
// with no handler must report false so callers skip Submit.
func TestScalablePoolDisabledWithoutHandler(t *testing.T) {
	p := NewScalablePool(nil, nil, 1, 2, 8, PolicyDrop)
	if p.Enabled() {
		t.Fatal("Enabled() = true, want false without a handler")
	}
	if p.Handler() != nil {
		t.Fatal("Handler() should be nil")
	}
}

// TestScalablePoolPolicyCloseClosesSessionOnFull checks the shared back-pressure
// policy still reaches the session through the elastic path.
func TestScalablePoolPolicyCloseClosesSessionOnFull(t *testing.T) {
	handler, parked, releaseCh := enterHandler(1)

	p := NewScalablePool(handler, nil, 1, 1, 1, PolicyClose)
	p.Start()

	sess := newBlockingSession(1)
	// Occupy the single worker and the single queue slot.
	if err := p.Submit(sess, core.ProtocolTCP, []byte("a")); err != nil {
		t.Fatalf("Submit first: %v", err)
	}
	<-parked
	if err := p.Submit(sess, core.ProtocolTCP, []byte("b")); err != nil {
		t.Fatalf("Submit second (fills queue): %v", err)
	}
	// Now full: this one must be rejected and the session closed.
	err := p.Submit(sess, core.ProtocolTCP, []byte("c"))
	if err != core.ErrWriteQueueFull {
		t.Fatalf("Submit on full = %v, want ErrWriteQueueFull", err)
	}
	if !sess.closed.Load() {
		t.Fatal("session not closed under PolicyClose")
	}
	// Release the parked handler before Stop: Stop drains the queue, and a
	// handler still blocked on releaseCh would deadlock the drain.
	close(releaseCh)
	p.Stop()
}

// TestScalablePoolMinMaxClamping covers the constructor's normalisation rules.
func TestScalablePoolMinMaxClamping(t *testing.T) {
	p := NewScalablePool(func(core.Session, core.Message) error { return nil }, nil, 0, 0, 8, PolicyDrop)
	defer p.Stop()
	if got := p.minWorker.Load(); got != int64(runtime.GOMAXPROCS(0)) {
		t.Fatalf("minWorker = %d, want GOMAXPROCS %d", got, runtime.GOMAXPROCS(0))
	}
	if got, want := p.maxWorker.Load(), int64(runtime.GOMAXPROCS(0)*DefaultMaxWorkerMultiplier); got != want {
		t.Fatalf("maxWorker = %d, want %d", got, want)
	}

	// max below min must be raised to min, not left inverted.
	q := NewScalablePool(func(core.Session, core.Message) error { return nil }, nil, 4, 2, 8, PolicyDrop)
	defer q.Stop()
	if q.maxWorker.Load() < q.minWorker.Load() {
		t.Fatalf("maxWorker %d < minWorker %d", q.maxWorker.Load(), q.minWorker.Load())
	}
}
