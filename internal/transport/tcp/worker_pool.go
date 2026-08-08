package tcp

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type FullPolicy int

const (
	PolicyBlock FullPolicy = iota
	PolicyDrop
	PolicyClose
)

type task struct {
	sess *session
	data []byte
}

type workerPool struct {
	handler core.Handler
	queue   chan task
	policy  FullPolicy
	wg      sync.WaitGroup
	closed  atomic.Bool
	done    chan struct{}
	// submitMu serializes submit() against stop(): submit holds the read lock
	// for the whole enqueue (including a blocking PolicyBlock send) so stop()
	// cannot drain the queue and return while a task is still in flight.
	submitMu sync.RWMutex
}

func newWorkerPool(handler core.Handler, workers int, queueSize int, policy FullPolicy) *workerPool {
	if workers <= 0 {
		workers = 1
	}
	if queueSize <= 0 {
		queueSize = workers * 128
	}
	return &workerPool{
		handler: handler,
		queue:   make(chan task, queueSize),
		policy:  policy,
		done:    make(chan struct{}),
	}
}

func (p *workerPool) start(workers int) {
	if workers <= 0 {
		workers = 1
	}
	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		go p.run()
	}
}

func (p *workerPool) submit(sess *session, data []byte) error {
	// Hold the read lock through the enqueue so a task cannot land in the
	// queue after stop() has drained it and returned (a submit that passed the
	// closed check just before stop() closed done).
	p.submitMu.RLock()
	defer p.submitMu.RUnlock()
	if p.closed.Load() {
		return core.ErrClosed
	}
	t := task{sess: sess, data: data}
	// Watch the session context too: if the peer disconnects while the queue is
	// full, the submitting goroutine would otherwise block until the whole pool
	// stops, leaking a goroutine per wedged connection. sess may be nil in
	// tests/regression callers, in which case only pool stop unblocks.
	var sessDone <-chan struct{}
	if sess != nil && sess.ctx != nil {
		sessDone = sess.ctx.Done()
	}
	switch p.policy {
	case PolicyBlock:
		// Never close p.queue; use done for termination so a blocking
		// send cannot race with stop() and panic on a closed channel.
		select {
		case p.queue <- t:
			return nil
		case <-p.done:
			return core.ErrClosed
		case <-sessDone:
			return core.ErrSessionClosed
		}
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

func (p *workerPool) stop() {
	if p.closed.CompareAndSwap(false, true) {
		close(p.done)
	}
	// Wait for every in-flight submit to finish enqueueing (blocked PolicyBlock
	// senders unblock on the closed done channel), then let the workers drain.
	p.submitMu.Lock()
	p.submitMu.Unlock()
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

func (p *workerPool) handle(t task) {
	if p.handler == nil {
		return
	}
	msg := core.Message{SessionID: t.sess.ID(), Protocol: core.ProtocolTCP, Payload: t.data}
	if err := p.callHandler(t.sess, msg); err != nil {
		_ = t.sess.Close(context.Background())
	}
}

// callHandler invokes the user handler with panic recovery so a panicking
// handler closes the session instead of crashing the worker goroutine (and
// with it the whole process).
func (p *workerPool) callHandler(sess *session, msg core.Message) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = core.ErrHandlerPanic
		}
	}()
	return p.handler(sess, msg)
}

func (p *workerPool) run() {
	defer p.wg.Done()
	for {
		select {
		case t := <-p.queue:
			p.handle(t)
		case <-p.done:
			// Drain any remaining queued tasks before exiting so that
			// tasks submitted before stop() still complete.
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
