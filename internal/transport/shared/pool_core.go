package shared

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// poolCore holds the state and behaviour that every task-dispatch pool needs,
// regardless of how it decides how many workers to run.
//
// Why this exists: the L2 elastic pool (ScalablePool) must reuse the queue,
// the back-pressure policy, the Submit/Stop mutual exclusion and the panic
// isolation without depending on WorkerPool's worker-management policy.
// Embedding WorkerPool would force a fragile override of Submit and handle
// (a "fragile base class"), so both pools compose this type instead.
//
// Every field here is owned by the embedding pool; poolCore never starts or
// stops workers of its own accord.
type poolCore struct {
	handler core.Handler
	logger  core.Logger
	queue   chan PoolTask
	policy  FullPolicy
	closed  atomic.Bool
	done    chan struct{}
	// submitMu serializes Submit against Stop: Submit holds the read lock for
	// the whole enqueue (including a blocking PolicyBlock send) so Stop cannot
	// drain the queue and return while a task is still in flight.
	submitMu sync.RWMutex
}

// newPoolCore builds the shared state. A non-positive queue size defaults to
// workers*128 so callers keep the historical sizing behaviour.
func newPoolCore(handler core.Handler, logger core.Logger, workers, queueSize int, policy FullPolicy) poolCore {
	if workers <= 0 {
		workers = 1
	}
	if queueSize <= 0 {
		queueSize = workers * 128
	}
	return poolCore{
		handler: handler,
		logger:  logger,
		queue:   make(chan PoolTask, queueSize),
		policy:  policy,
		done:    make(chan struct{}),
	}
}

// Handler returns the configured handler.
func (c *poolCore) Handler() core.Handler { return c.handler }

// Enabled reports whether a handler is installed. Callers should skip Submit
// when it returns false so a pool without a handler does not enqueue tasks
// only to discard them.
func (c *poolCore) Enabled() bool { return c.handler != nil }

// submit enqueues one message for the user handler.
//
// The locking contract matters and is unchanged from the original inline
// implementation: the read lock is held across the entire enqueue, including
// a blocking PolicyBlock send, so Stop cannot drain the queue and return while
// a task is still in flight. Scaling decisions must NOT be taken while this
// lock is held -- see ScalablePool.maybeScale.
func (c *poolCore) submit(sess core.Session, protocol core.Protocol, payload []byte) error {
	c.submitMu.RLock()
	defer c.submitMu.RUnlock()
	if c.closed.Load() {
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
	switch c.policy {
	case PolicyDrop:
		select {
		case c.queue <- t:
			return nil
		default:
			return core.ErrWriteQueueFull
		}
	case PolicyClose:
		select {
		case c.queue <- t:
			return nil
		default:
			if sess != nil {
				_ = sess.Close(context.Background())
			}
			return core.ErrWriteQueueFull
		}
	default:
		// Never close c.queue; use done for termination so a blocking send
		// cannot race Stop and panic on a closed channel.
		select {
		case c.queue <- t:
			return nil
		case <-c.done:
			return core.ErrClosed
		case <-sessDone:
			return core.ErrSessionClosed
		}
	}
}

// handle invokes the user handler for one task. It carries the two early-exit
// branches that keep a pool without a handler, or a task without a session,
// from reaching user code.
func (c *poolCore) handle(t PoolTask) {
	if c.handler == nil || t.Session == nil {
		return
	}
	msg := core.Message{SessionID: t.Session.ID(), Protocol: t.Protocol, Payload: t.Payload}
	if err := c.callHandler(t.Session, msg); err != nil {
		_ = t.Session.Close(context.Background())
	}
}

// callHandler invokes the user handler with panic recovery and logging, so a
// panicking handler closes the session instead of crashing the worker (and with
// it the whole process) and the operator gets a diagnostic rather than a
// silently vanished session.
func (c *poolCore) callHandler(sess core.Session, msg core.Message) error {
	return CallHandler(func() error { return c.handler(sess, msg) }, c.logger)
}

// beginClose marks the pool closed and closes the done channel exactly once.
// The returned value reports whether this call performed the transition, so
// callers can distinguish "I closed it" from "already closed".
func (c *poolCore) beginClose() bool {
	if !c.closed.CompareAndSwap(false, true) {
		return false
	}
	close(c.done)
	return true
}

// drainQueue handles everything still queued and is used as the final safety
// net after all workers have exited.
func (c *poolCore) drainQueue() {
	for {
		select {
		case t := <-c.queue:
			c.handle(t)
		default:
			return
		}
	}
}
