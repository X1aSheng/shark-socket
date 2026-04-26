package tcp

import (
	"sync"
	"sync/atomic"

	"github.com/X1aSheng/shark-socket/internal/errs"
	"github.com/X1aSheng/shark-socket/internal/plugin"
	"github.com/X1aSheng/shark-socket/internal/types"
)

// FullPolicy defines behavior when the task queue is full.
type FullPolicy int

const (
	PolicyBlock     FullPolicy = 0
	PolicyDrop      FullPolicy = 1
	PolicySpawnTemp FullPolicy = 2
	PolicyClose     FullPolicy = 3
)

type task struct {
	sess types.RawSession
	data []byte
}

// WorkerPool manages a pool of worker goroutines for message processing.
type WorkerPool struct {
	taskQueue  chan task
	handler    types.RawHandler
	chain      *plugin.Chain
	wg         sync.WaitGroup
	maxWorkers int
	coreCount  int
	tempCount  atomic.Int32
	closed     atomic.Bool
	policy     FullPolicy
}

// NewWorkerPool creates a new worker pool.
func NewWorkerPool(handler types.RawHandler, chain *plugin.Chain, coreWorkers, queueSize, maxWorkers int, policy FullPolicy) *WorkerPool {
	if coreWorkers <= 0 {
		coreWorkers = 1
	}
	if queueSize <= 0 {
		queueSize = coreWorkers * 128
	}
	if maxWorkers < coreWorkers {
		maxWorkers = coreWorkers * 4
	}
	return &WorkerPool{
		taskQueue:  make(chan task, queueSize),
		handler:    handler,
		chain:      chain,
		coreCount:  coreWorkers,
		maxWorkers: maxWorkers,
		policy:     policy,
	}
}

// Start launches core worker goroutines.
func (wp *WorkerPool) Start() {
	for i := 0; i < wp.coreCount; i++ {
		wp.wg.Add(1)
		go wp.runWorker()
	}
}

// Submit sends a task to the worker pool.
func (wp *WorkerPool) Submit(sess types.RawSession, data []byte) error {
	t := task{sess: sess, data: data}
	switch wp.policy {
	case PolicyBlock:
		wp.taskQueue <- t
		return nil
	case PolicyDrop:
		select {
		case wp.taskQueue <- t:
			return nil
		default:
			return errs.ErrWriteQueueFull
		}
	case PolicySpawnTemp:
		select {
		case wp.taskQueue <- t:
			return nil
		default:
			if int(wp.tempCount.Load()) < wp.maxWorkers-wp.coreCount {
				wp.tempCount.Add(1)
				wp.wg.Add(1)
				go wp.runTempWorker(t)
				return nil
			}
			return errs.ErrWriteQueueFull
		}
	case PolicyClose:
		select {
		case wp.taskQueue <- t:
			return nil
		default:
			return errs.ErrWriteQueueFull
		}
	default:
		select {
		case wp.taskQueue <- t:
			return nil
		default:
			return errs.ErrWriteQueueFull
		}
	}
}

// Stop closes the task queue and waits for workers to finish.
func (wp *WorkerPool) Stop() {
	if wp.closed.CompareAndSwap(false, true) {
		close(wp.taskQueue)
	}
	wp.wg.Wait()
}

func (wp *WorkerPool) runWorker() {
	defer wp.wg.Done()
	for t := range wp.taskQueue {
		wp.processTask(t)
	}
}

func (wp *WorkerPool) runTempWorker(first task) {
	defer func() {
		wp.tempCount.Add(-1)
		wp.wg.Done()
	}()
	wp.processTask(first)
	// Drain any remaining tasks, then exit
	for {
		select {
		case t, ok := <-wp.taskQueue:
			if !ok {
				return
			}
			wp.processTask(t)
		default:
			return
		}
	}
}

func (wp *WorkerPool) processTask(t task) {
	defer func() {
		recover()
	}()

	var err error
	data := t.data

	if wp.chain != nil && wp.chain.Len() > 0 {
		data, err = wp.chain.OnMessage(t.sess, t.data)
		if err != nil {
			if err == errs.ErrDrop {
				return
			}
			return
		}
	}

	if wp.handler != nil {
		msg := types.NewRawMessage(t.sess.ID(), t.sess.Protocol(), data)
		_ = wp.handler(t.sess, msg)
	}
}
