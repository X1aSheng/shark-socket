package shared

import (
	"context"
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
// the transports reentrant. It is shared by the TCP, UDP and CoAP servers so
// they all get identical backpressure and panic-isolation semantics.
type WorkerPool struct {
	handler core.Handler
	logger  core.Logger
	queue   chan PoolTask
	policy  FullPolicy
	wg      sync.WaitGroup
	closed  atomic.Bool
	done    chan struct{}
	// submitMu serializes Submit against Stop: Submit holds the read lock for
	// the whole enqueue (including a blocking PolicyBlock send) so Stop cannot
	// drain the queue and return while a task is still in flight.
	submitMu sync.RWMutex
}

// NewWorkerPool creates a pool. A non-positive worker count is treated as 1 and
// a non-positive queue size defaults to workers*128. handler may be nil, in
// which case Enabled reports false and callers skip Submit entirely.
func NewWorkerPool(handler core.Handler, logger core.Logger, workers, queueSize int, policy FullPolicy) *WorkerPool {
	if workers <= 0 {
		workers = 1
	}
	if queueSize <= 0 {
		queueSize = workers * 128
	}
	return &WorkerPool{
		handler: handler,
		logger:  logger,
		queue:   make(chan PoolTask, queueSize),
		policy:  policy,
		done:    make(chan struct{}),
	}
}

// Handler returns the configured handler.
func (p *WorkerPool) Handler() core.Handler { return p.handler }

// Enabled reports whether a handler is installed. Callers should skip Submit
// when it returns false so a pool without a handler does not enqueue tasks
// only to discard them.
func (p *WorkerPool) Enabled() bool { return p.handler != nil }

// Start launches the worker goroutines.
func (p *WorkerPool) Start(workers int) {
	if workers <= 0 {
		workers = 1
	}
	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		go p.run()
	}
}

// Submit enqueues one message for the user handler.
func (p *WorkerPool) Submit(sess core.Session, protocol core.Protocol, payload []byte) error {
	// Hold the read lock through the enqueue so a task cannot land in the
	// queue after Stop has drained it and returned.
	p.submitMu.RLock()
	defer p.submitMu.RUnlock()
	if p.closed.Load() {
		return core.ErrClosed
	}
	t := PoolTask{Session: sess, Protocol: protocol, Payload: payload}
	// Watch the session context too: if the peer disconnects while the queue is
	// full, the submitting goroutine would otherwise block until the whole pool
	// stops, leaking a goroutine per wedged connection. sess may be nil in
	// tests/regression callers, in which case only pool stop unblocks.
	var sessDone <-chan struct{}
	if sess != nil {
		if ctx := sess.Context(); ctx != nil {
			sessDone = ctx.Done()
		}
	}
	switch p.policy {
	case PolicyDrop:
		select {
		case p.queue <- t:
			return nil
		default:
			return core.ErrWriteQueueFull
		}
	case PolicyClose:
		select {
		case p.queue <- t:
			return nil
		default:
			if sess != nil {
				_ = sess.Close(context.Background())
			}
			return core.ErrWriteQueueFull
		}
	default:
		// Never close p.queue; use done for termination so a blocking send
		// cannot race Stop and panic on a closed channel.
		select {
		case p.queue <- t:
			return nil
		case <-p.done:
			return core.ErrClosed
		case <-sessDone:
			return core.ErrSessionClosed
		}
	}
}

// Stop terminates the workers after draining everything already accepted.
func (p *WorkerPool) Stop() {
	if p.closed.CompareAndSwap(false, true) {
		close(p.done)
	}
	// Acquire the write lock: it cannot be granted until every in-flight Submit
	// (which holds the read lock across its enqueue) has finished. It is held
	// while the workers drain and through the final sweep, so the sweep cannot
	// race a new enqueue.
	p.submitMu.Lock()
	defer p.submitMu.Unlock()
	p.wg.Wait()
	// Safety net: handle anything still queued.
	for {
		select {
		case t := <-p.queue:
			p.handle(t)
		default:
			return
		}
	}
}

func (p *WorkerPool) handle(t PoolTask) {
	if p.handler == nil || t.Session == nil {
		return
	}
	msg := core.Message{SessionID: t.Session.ID(), Protocol: t.Protocol, Payload: t.Payload}
	if err := p.callHandler(t.Session, msg); err != nil {
		_ = t.Session.Close(context.Background())
	}
}

// callHandler invokes the user handler with panic recovery and logging, so a
// panicking handler closes the session instead of crashing the worker (and with
// it the whole process) and the operator gets a diagnostic rather than a
// silently vanished session.
func (p *WorkerPool) callHandler(sess core.Session, msg core.Message) error {
	return CallHandler(func() error { return p.handler(sess, msg) }, p.logger)
}

func (p *WorkerPool) run() {
	defer p.wg.Done()
	for {
		select {
		case t := <-p.queue:
			p.handle(t)
		case <-p.done:
			// Drain any remaining queued tasks before exiting so that tasks
			// submitted before Stop still complete.
			for {
				select {
				case t := <-p.queue:
					p.handle(t)
				default:
					return
				}
			}
		}
	}
}
