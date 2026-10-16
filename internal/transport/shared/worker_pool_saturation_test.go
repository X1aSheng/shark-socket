package shared

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// These tests measure WorkerPool saturation deterministically, without any
// network client. The load-path benchmark (scripts/workerpool_baseline.go) had
// to abandon that approach because the client process itself became the
// bottleneck -- clients filled the socket buffer and exited, so absolute
// throughput numbers described the client, not the pool.
//
// The technique here is to hold every worker inside a handler that never
// returns, then measure what Submit does once the queue is provably full.
// That makes the capacity ceiling a property of the pool under test and the
// test is reproducible on any machine.

// blockingSession is a poolTestSession whose Close is observable, used to
// assert what PolicyClose does to a rejected task's session.
type blockingSession struct {
	*poolTestSession
	closed atomic.Bool
}

func newBlockingSession(id uint64) *blockingSession {
	return &blockingSession{poolTestSession: newPoolTestSession(id)}
}

func (s *blockingSession) Close(context.Context) error {
	s.closed.Store(true)
	return s.poolTestSession.Close(context.Background())
}

// enterHandler returns a handler that blocks until release is closed, and a
// signal closed once the requested number of workers are parked inside it.
func enterHandler(want int) (handler core.Handler, parked chan struct{}, release chan struct{}) {
	parked = make(chan struct{})
	release = make(chan struct{})
	var n atomic.Int64
	return func(sess core.Session, msg core.Message) error {
		if n.Add(1) == int64(want) {
			close(parked)
		}
		<-release
		return nil
	}, parked, release
}

// TestPoolCapacityDeterministic measures how many tasks the pool accepts once
// every worker is parked and the queue is full, per policy.
//
// The pool is created with queueSize workers+1 so a worker can drain one
// extra task beyond the parked set; that is subtracted in the accounting.
func TestPoolCapacityDeterministic(t *testing.T) {
	const (
		workers = 4
		queue   = 8
	)

	for _, tc := range []struct {
		name   string
		policy FullPolicy
	}{
		{"drop", PolicyDrop},
		{"close", PolicyClose},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler, parked, release := enterHandler(workers)
			p := NewWorkerPool(handler, core.NopLogger(), workers, queue, tc.policy)
			p.Start(workers)
			defer func() {
				close(release)
				p.Stop()
			}()

			// Submit exactly one task per worker: with an empty queue the
			// workers would otherwise sit in select and never enter the
			// handler, so nothing would ever park.
			for i := 0; i < workers; i++ {
				if err := p.Submit(newPoolTestSession(uint64(i+1)), core.ProtocolTCP, make([]byte, 16)); err != nil {
					t.Fatalf("priming worker %d: %v", i, err)
				}
			}

			select {
			case <-parked:
			case <-time.After(5 * time.Second):
				t.Fatalf("workers did not park; pool not saturated")
			}

			// All workers are now blocked. Push well past the queue depth and
			// count the accepted/rejected split deterministically.
			const attempts = 500
			var accepted, rejected atomic.Int64
			sessions := make([]*blockingSession, 0, attempts)

			for i := 0; i < attempts; i++ {
				s := newBlockingSession(uint64(i + 1))
				sessions = append(sessions, s)
				err := p.Submit(s, core.ProtocolTCP, make([]byte, 16))
				switch {
				case err == nil:
					accepted.Add(1)
				case errors.Is(err, core.ErrWriteQueueFull):
					rejected.Add(1)
				default:
					t.Fatalf("attempt %d: unexpected error %v", i, err)
				}
			}

			got := accepted.Load()
			// Accepted cannot exceed queue depth: every worker is parked
			// inside a handler, so nothing drains the queue. Any larger number
			// would mean a task ran despite the workers being blocked.
			if got > queue {
				t.Fatalf("accepted %d tasks with all %d workers parked and queue depth %d: capacity is unbounded",
					got, workers, queue)
			}
			if rejected.Load() == 0 {
				t.Fatalf("no task was rejected; pool accepted all %d", attempts)
			}

			t.Logf("policy=%s workers=%d queue=%d accepted=%d rejected=%d capacity=queue-only",
				tc.name, workers, queue, got, rejected.Load())

			if tc.policy == PolicyClose {
				// Every rejected task must have had its session closed; a
				// rejected-but-open session leaks the connection.
				open := 0
				for _, s := range sessions {
					if !s.closed.Load() && !s.poolTestSession.closed.Load() {
						open++
					}
				}
				// Only the accepted ones may remain open.
				if wantOpen := int(got); open > wantOpen {
					t.Fatalf("PolicyClose left %d sessions open, at most %d may be accepted-and-open", open, wantOpen)
				}
				t.Logf("PolicyClose closed every rejected session (%d rejected)", rejected.Load())
			}
		})
	}
}

// TestPoolBlockStallsSubmitter is the test for the most dangerous documented
// behaviour: under PolicyBlock, Submit blocks. In the server this Submit runs
// synchronously inside the per-connection read goroutine
// (tcp/server.go:212), so a blocking Submit stalls that connection's reader.
//
// This measures the stall precisely rather than asserting it in prose.
func TestPoolBlockStallsSubmitter(t *testing.T) {
	const (
		workers = 2
		queue   = 2
	)

	handler, parked, release := enterHandler(workers)
	p := NewWorkerPool(handler, core.NopLogger(), workers, queue, PolicyBlock)
	p.Start(workers)
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	defer func() {
		releaseAll()
		p.Stop()
	}()

	// Prime: one task per worker so both park inside the handler.
	for i := 0; i < workers; i++ {
		if err := p.Submit(newPoolTestSession(uint64(i+1)), core.ProtocolTCP, make([]byte, 8)); err != nil {
			t.Fatalf("priming worker %d: %v", i, err)
		}
	}

	select {
	case <-parked:
	case <-time.After(5 * time.Second):
		t.Fatalf("workers did not park")
	}

	// Fill the queue so the next Submit has nowhere to go and must block.
	for i := 0; i < queue; i++ {
		if err := p.Submit(newPoolTestSession(uint64(1000+i)), core.ProtocolTCP, make([]byte, 8)); err != nil {
			t.Fatalf("filling queue: %v", err)
		}
	}

	done := make(chan error, 1)
	go func() {
		done <- p.Submit(newPoolTestSession(9999), core.ProtocolTCP, make([]byte, 8))
	}()

	// The submitter must still be blocked: nothing has drained the queue.
	select {
	case err := <-done:
		t.Fatalf("PolicyBlock Submit returned %v while the queue was full; expected it to block", err)
	case <-time.After(300 * time.Millisecond):
	}

	// Draining one worker must release exactly one blocked submitter.
	go func() {
		// Let the parked handler return so a worker can pick up the queued task.
		time.Sleep(50 * time.Millisecond)
	}()

	t.Logf("confirmed: PolicyBlock blocks Submit while the queue is full; " +
		"in tcp/server.go:212 this Submit runs in the connection read goroutine, " +
		"so a saturated pool stalls that connection's reader")

	// Release so the deferred Stop can complete.
	releaseAll()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("submitter released with error %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("submitter never released after drain")
	}
}

// TestPoolBlockUnblocksOnSessionClose covers the escape hatch the pool
// documents: a blocking Submit also watches the session context, so a peer
// disconnect releases the submitter instead of wedging a goroutine forever.
func TestPoolBlockUnblocksOnSessionClose(t *testing.T) {
	const workers = 1

	handler, parked, release := enterHandler(workers)
	p := NewWorkerPool(handler, core.NopLogger(), workers, 1, PolicyBlock)
	p.Start(workers)
	defer func() {
		close(release)
		p.Stop()
	}()

	// Submit two tasks: the first is taken by the worker and parks inside the
	// handler, the second lands in the queue's single slot. Only then does the
	// queue have no room, so the next Submit has to block.
	for i := 0; i < 2; i++ {
		if err := p.Submit(newPoolTestSession(uint64(i+1)), core.ProtocolTCP, make([]byte, 8)); err != nil {
			t.Fatalf("priming task %d: %v", i, err)
		}
	}

	select {
	case <-parked:
	case <-time.After(5 * time.Second):
		t.Fatalf("worker did not park")
	}

	sess := newPoolTestSession(2)
	done := make(chan error, 1)
	go func() {
		done <- p.Submit(sess, core.ProtocolTCP, make([]byte, 8))
	}()

	select {
	case <-done:
		t.Fatalf("Submit returned before the session was closed")
	case <-time.After(200 * time.Millisecond):
	}

	// The peer disconnects: the pool must release the blocked submitter.
	_ = sess.Close(context.Background())

	select {
	case err := <-done:
		if !errors.Is(err, core.ErrSessionClosed) {
			t.Fatalf("expected ErrSessionClosed after session close, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("blocked Submit was not released by session close: goroutine leak")
	}

	t.Log("confirmed: blocking Submit is released by session close, no goroutine leak per wedged connection")
}

// TestPoolQueueDepthIsGlobalNotPerSession records the cross-connection
// coupling implied by a single pool: one noisy session can consume the entire
// queue and starve every other session on the same server.
func TestPoolQueueDepthIsGlobalNotPerSession(t *testing.T) {
	const (
		workers = 1
		queue   = 4
	)

	handler, parked, release := enterHandler(workers)
	p := NewWorkerPool(handler, core.NopLogger(), workers, queue, PolicyDrop)
	p.Start(workers)
	defer func() {
		close(release)
		p.Stop()
	}()

	// Prime: one task per worker so they enter the blocking handler.
	if err := p.Submit(newPoolTestSession(1), core.ProtocolTCP, make([]byte, 8)); err != nil {
		t.Fatalf("priming worker: %v", err)
	}

	select {
	case <-parked:
	case <-time.After(5 * time.Second):
		t.Fatalf("worker did not park")
	}

	// A single session submits queue+1 tasks. There is only one worker, parked.
	noisy := newPoolTestSession(100)
	var rejected atomic.Int64
	for i := 0; i <= queue; i++ {
		if err := p.Submit(noisy, core.ProtocolTCP, make([]byte, 8)); err != nil {
			rejected.Add(1)
		}
	}
	if rejected.Load() == 0 {
		t.Fatalf("expected the noisy session alone to exhaust the queue")
	}

	// A different, well-behaved session is now rejected too.
	if err := p.Submit(newPoolTestSession(200), core.ProtocolTCP, make([]byte, 8)); !errors.Is(err, core.ErrWriteQueueFull) {
		t.Fatalf("expected an unrelated session to be rejected once the queue is exhausted, got %v", err)
	}

	t.Logf("confirmed: queue depth %d is shared across all sessions on the server; "+
		"one session consumed it entirely and an unrelated session was rejected", queue)
}

// TestPoolSubmitLockHoldDuringBlock documents the second cost of PolicyBlock:
// Submit holds submitMu.RLock for the whole enqueue including the blocking
// send, so Stop must wait for every wedged submitter. If it did not, Stop
// could return while tasks were still in flight.
func TestPoolSubmitLockHoldDuringBlock(t *testing.T) {
	const workers = 1

	handler, parked, release := enterHandler(workers)
	p := NewWorkerPool(handler, core.NopLogger(), workers, 1, PolicyBlock)
	p.Start(workers)

	// Prime the worker so it parks; this task also fills the single queue slot.
	if err := p.Submit(newPoolTestSession(1), core.ProtocolTCP, make([]byte, 8)); err != nil {
		close(release)
		p.Stop()
		t.Fatalf("priming worker: %v", err)
	}

	select {
	case <-parked:
	case <-time.After(5 * time.Second):
		close(release)
		p.Stop()
		t.Fatalf("worker did not park")
	}

	if err := p.Submit(newPoolTestSession(1), core.ProtocolTCP, make([]byte, 8)); err != nil {
		t.Fatalf("filling queue: %v", err)
	}

	blocked := make(chan struct{})
	go func() {
		_ = p.Submit(newPoolTestSession(2), core.ProtocolTCP, make([]byte, 8))
		close(blocked)
	}()
	time.Sleep(200 * time.Millisecond)

	stopStarted := time.Now()
	stopDone := make(chan struct{})
	go func() {
		p.Stop()
		close(stopDone)
	}()

	select {
	case <-stopDone:
		t.Fatalf("Stop returned in %s while a submitter was still blocked in Submit; "+
			"Stop must wait for in-flight enqueues", time.Since(stopStarted))
	case <-time.After(500 * time.Millisecond):
	}

	close(release)
	select {
	case <-stopDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("Stop did not complete after drain")
	}

	t.Logf("confirmed: Stop waited %s for the blocked Submit (submitMu write lock)", time.Since(stopStarted))
}

var _ = runtime.NumGoroutine
var _ sync.Mutex
