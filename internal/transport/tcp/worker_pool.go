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
	if p.closed.Load() {
		return core.ErrClosed
	}
	t := task{sess: sess, data: data}
	switch p.policy {
	case PolicyBlock:
		// Never close p.queue; use done for termination so a blocking
		// send cannot race with stop() and panic on a closed channel.
		select {
		case p.queue <- t:
			return nil
		case <-p.done:
			return core.ErrClosed
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
			_ = sess.Close(context.Background())
			return core.ErrWriteQueueFull
		}
	default:
		select {
		case p.queue <- t:
			return nil
		case <-p.done:
			return core.ErrClosed
		}
	}
}

func (p *workerPool) stop() {
	if p.closed.CompareAndSwap(false, true) {
		close(p.done)
	}
	p.wg.Wait()
}

func (p *workerPool) handle(t task) {
	if p.handler == nil {
		return
	}
	msg := core.Message{SessionID: t.sess.ID(), Protocol: core.ProtocolTCP, Payload: t.data}
	if err := p.handler(t.sess, msg); err != nil {
		_ = t.sess.Close(context.Background())
	}
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
