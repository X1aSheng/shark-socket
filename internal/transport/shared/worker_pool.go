package shared

import (
	"sync"
	"sync/atomic"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// FullPolicy decides how a WorkerPool reacts when its task queue is full.
type FullPolicy int

const (
	// PolicyBlock makes the submitting goroutine wait for room.
	PolicyBlock FullPolicy = iota
	// PolicyDrop rejects the task with core.ErrWriteQueueFull.
	PolicyDrop
	// PolicyClose rejects the task and closes the session.
	PolicyClose
)

// PoolTask is one message handed to the user handler by a WorkerPool.
type PoolTask struct {
	Session  core.Session
	Protocol core.Protocol
	Payload  []byte
}

// WorkerPool runs user handlers off the network read path. Without it a slow
// handler stalls the read loop and every other peer's traffic queues up behind
// it (head-of-line blocking), and a panicking handler takes the process down
// because Go has no per-goroutine recovery.
//
// The pool is safe for concurrent use and for repeated Start/Stop, which keeps
// the transports reentrant. It lives in the shared package so any transport can
// reuse it: the TCP server instantiates one today (the other transports call the
// handler inline through CallHandler), and all of them therefore share identical
// handler panic-isolation semantics.
//
// Worker management is deliberately fixed: workers start once in Start and stop
// in Stop. For a pool that adjusts its worker count at run time see
// ScalablePool.
type WorkerPool struct {
	poolCore
	wg sync.WaitGroup
	// workers counts the running workers so the count is observable the same way
	// it is on ScalablePool. The pool is configured with a fixed number, but
	// "configured" is not "running": a caller that can read one pool's count and
	// not the other's has no way to compare them.
	workers atomic.Int64
}

// NewWorkerPool creates a pool. A non-positive worker count is treated as 1 and
// a non-positive queue size defaults to workers*128. handler may be nil, in
// which case Enabled reports false and callers skip Submit entirely.
func NewWorkerPool(handler core.Handler, logger core.Logger, workers, queueSize int, policy FullPolicy, opts ...PoolOption) *WorkerPool {
	p := &WorkerPool{poolCore: newPoolCore(handler, logger, workers, queueSize, policy)}
	p.applyOptions(opts)
	return p
}

// Handler returns the configured handler.
func (p *WorkerPool) Handler() core.Handler { return p.poolCore.Handler() }

// Enabled reports whether a handler is installed. Callers should skip Submit
// when it returns false so a pool without a handler does not enqueue tasks
// only to discard them.
func (p *WorkerPool) Enabled() bool { return p.poolCore.Enabled() }

// Start launches the worker goroutines.
func (p *WorkerPool) Start(workers int) {
	if workers <= 0 {
		workers = 1
	}
	for i := 0; i < workers; i++ {
		p.workers.Add(1)
		p.wg.Go(func() { p.run() })
	}
}

// WorkerCount returns the number of running workers.
func (p *WorkerPool) WorkerCount() int { return int(p.workers.Load()) }

// WorkerBounds returns the configured size. A fixed pool has one value for
// both, so the pair exists to match ScalablePool's signature rather than to
// report a range.
func (p *WorkerPool) WorkerBounds() (min, max int) {
	n := p.WorkerCount()
	return n, n
}

// Submit enqueues one message for the user handler.
func (p *WorkerPool) Submit(sess core.Session, protocol core.Protocol, payload []byte) error {
	return p.submit(sess, protocol, payload)
}

// Stop terminates the workers after draining everything already accepted.
func (p *WorkerPool) Stop() {
	p.beginClose()
	// Acquire the write lock: it cannot be granted until every in-flight Submit
	// (which holds the read lock across its enqueue) has finished. It is held
	// while the workers drain and through the final sweep, so the sweep cannot
	// race a new enqueue.
	p.submitMu.Lock()
	defer p.submitMu.Unlock()
	p.wg.Wait()
	// Safety net: handle anything still queued.
	p.drainQueue()
}

func (p *WorkerPool) run() {
	for {
		select {
		case t := <-p.queue:
			p.handle(t)
		case <-p.done:
			// Drain any remaining queued tasks before exiting so that tasks
			// submitted before Stop still complete.
			p.drainQueue()
			return
		}
	}
}
