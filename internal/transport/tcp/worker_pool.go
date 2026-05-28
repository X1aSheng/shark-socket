package tcp

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/X1aSheng/shark-socket-new/internal/core"
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
		p.queue <- t
		return nil
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
		p.queue <- t
		return nil
	}
}

func (p *workerPool) stop() {
	if p.closed.CompareAndSwap(false, true) {
		close(p.queue)
	}
	p.wg.Wait()
}

func (p *workerPool) run() {
	defer p.wg.Done()
	for t := range p.queue {
		if p.handler == nil {
			continue
		}
		msg := core.Message{SessionID: t.sess.ID(), Protocol: core.ProtocolTCP, Payload: t.data}
		if err := p.handler(t.sess, msg); err != nil {
			_ = t.sess.Close(context.Background())
		}
	}
}
