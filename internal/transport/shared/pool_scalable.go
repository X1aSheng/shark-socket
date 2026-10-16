package shared

import (
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// DefaultMaxWorkerMultiplier bounds maxWorker in units of GOMAXPROCS.
//
// Measured basis (design doc section 14.2): on an 8 vCPU / 4-physical-core host
// with a 1ms handler, throughput scaled linearly up to 8 workers (0.91x of
// ideal) and fell off to 0.75x at 16. Two per logical CPU therefore allows
// some oversubscription for burst absorption without inviting the context
// switch cost that showed up past the core count.
const DefaultMaxWorkerMultiplier = 2

// DefaultScaleUpRatio is the queue occupancy at which the pool adds a worker.
const DefaultScaleUpRatio = 0.75

// DefaultScaleDownRatio is the queue occupancy below which the pool may
// retire a worker. It is deliberately far below the up threshold so the pool
// does not oscillate around one boundary.
const DefaultScaleDownRatio = 0.25

// DefaultScaleDownHold is how many consecutive low-water observations are
// required before a worker is retired.
//
// Shrinking is asymmetric with growing on purpose: adding a worker that turns
// out to be unnecessary costs one goroutine, while removing one that was
// needed pushes work onto the remaining workers and raises the drop rate. The
// cost of a wrong call is therefore asymmetric and the policy is too.
const DefaultScaleDownHold = 3

// workerRef is one worker's identity.
//
// Scaling means "stop this particular worker", so a worker has to be
// individually addressable. A bare `go p.run()` gives no handle on the
// goroutine it started and cannot be asked to retire.
type workerRef struct {
	id     int64
	retire chan struct{}
}

// ScalablePool is a WorkerPool whose worker count adjusts at run time.
//
// It exists because measurement (design doc section 14.2) shows throughput
// tracking worker count nearly linearly until the core count is exceeded:
// on an 8 vCPU host with a 1ms handler, raising the worker count from 1 to 16
// moved throughput by 11.96x. A fixed pool cannot follow a workload whose
// handler latency or arrival rate varies.
//
// What this does NOT solve: a single noisy session can still exhaust the
// queue and starve unrelated sessions, because the queue is global. That is an
// isolation problem, not a capacity problem; fixing it needs per-session
// queues, which is a different design (sharding was rejected in the v2 design).
//
// Not embedded in WorkerPool: inheriting would force Submit and handle to be
// overridden, which is a fragile base class. Both compose poolCore instead.
type ScalablePool struct {
	poolCore

	mu      sync.Mutex
	workers map[int64]*workerRef
	nextID  atomic.Int64

	minWorker atomic.Int64
	maxWorker atomic.Int64

	scaleUpRatio   float64
	scaleDownRatio float64
	scaleDownHold  int32
	downStreak     atomic.Int32

	// submitTick counts submits so scaling can be sampled on a modulo instead
	// of on every call. It is a plain counter: the modulo is the only read.
	submitTick atomic.Int64

	// wg tracks every worker ever started, retired ones included. Stop waits
	// on it, so a retired worker must have already called Done by the time
	// Stop returns. wg.Go owns both the increment and the goroutine start,
	// which is what makes concurrent addWorker safe against Stop's Wait.
	wg sync.WaitGroup
}

// NewScalablePool creates an elastic pool. min/max bound the worker count at
// run time; non-positive values fall back to GOMAXPROCS and
// DefaultMaxWorkerMultiplier x GOMAXPROCS respectively.
func NewScalablePool(handler core.Handler, logger core.Logger, min, max, queueSize int, policy FullPolicy) *ScalablePool {
	if min <= 0 {
		min = runtime.GOMAXPROCS(0)
	}
	if max <= 0 {
		max = runtime.GOMAXPROCS(0) * DefaultMaxWorkerMultiplier
	}
	if max < min {
		max = min
	}
	p := &ScalablePool{
		poolCore:       newPoolCore(handler, logger, min, queueSize, policy),
		workers:        make(map[int64]*workerRef),
		scaleUpRatio:   DefaultScaleUpRatio,
		scaleDownRatio: DefaultScaleDownRatio,
		scaleDownHold:  DefaultScaleDownHold,
	}
	p.minWorker.Store(int64(min))
	p.maxWorker.Store(int64(max))
	return p
}

// Start launches minWorker workers.
func (p *ScalablePool) Start() {
	target := int(p.minWorker.Load())
	for range target {
		p.addWorker()
	}
}

// SetScaleRatio overrides the queue-occupancy thresholds.
//
// A high up threshold (0.8) is conservative: the queue has to fill well before
// the pool grows, which is stable but slow to react. A low one (0.2) reacts to
// a light backlog, at the cost of overshooting. Only the pair matters -- down
// must sit below up or the pool oscillates around a single boundary -- so an
// invalid pair is rejected outright rather than silently reordered, which would
// turn a caller's mistake into a policy they did not ask for.
//
// Values outside (0, 1) are ignored: a threshold at or beyond the ends of the
// occupancy range would make one branch unreachable.
func (p *ScalablePool) SetScaleRatio(up, down float64) {
	if up <= 0 || up > 1 || down <= 0 || down >= up {
		return
	}
	p.scaleUpRatio = up
	p.scaleDownRatio = down
}

// WorkerCount reports the number of workers currently running.
//
// Exposed because an elastic pool's whole point is a worker count that moves:
// without this there is no way to observe the pool doing what it was added for,
// neither from a test nor from an operator watching a live server.
func (p *ScalablePool) WorkerCount() int { return p.workerCount() }

// WorkerBounds returns the configured min and max worker counts.
func (p *ScalablePool) WorkerBounds() (min, max int) {
	return int(p.minWorker.Load()), int(p.maxWorker.Load())
}

// Submit enqueues one message for the user handler, then evaluates scaling.
//
// The scaling check runs AFTER poolCore.submit has returned, which is the whole
// point of the split: submitMu's read lock is held across the entire enqueue
// (including a blocking PolicyBlock send), so calling addWorker inside it would
// extend the hold time of every concurrent Submit and amplify exactly the
// back-pressure that lock exists to bound. By the time this line runs the lock
// is already released.
//
// Every return path ticks, including ErrWriteQueueFull: a rejected submit is
// the single strongest signal that the pool is short of workers, so it must
// not be skipped.
func (p *ScalablePool) Submit(sess core.Session, protocol core.Protocol, payload []byte) error {
	err := p.submit(sess, protocol, payload)
	if p.submitTick.Add(1)%scaleCheckInterval == 0 {
		p.maybeScale()
	}
	return err
}

// scaleCheckInterval is how many submits pass between scaling evaluations.
//
// The check reads len/cap of the queue, which is cheap but not free, so it
// runs on an atomic modulo rather than on every call. 1024 keeps the sampling
// at roughly one evaluation per 10ms at 100k msg/s, which is fine enough to
// follow a burst, while leaving the hot path with a single atomic add.
const scaleCheckInterval = 1024

// maybeScale decides whether to grow or shrink the worker set, and acts on it.
//
// The judgement uses queue occupancy alone. Two alternatives were rejected:
// handler latency needs a per-task timer on the hot path, and error rate is a
// consequence rather than a cause -- by the time it climbs, the drop has
// already happened. Occupancy is the leading indicator: a backlog means
// workers are consuming slower than producers are submitting.
//
// Growing and shrinking are deliberately asymmetric. Adding a worker that turns
// out to be unnecessary costs one goroutine; removing one that was needed pushes
// its work onto the survivors and raises the drop rate. So growth acts on a
// single observation, while shrink requires scaleDownHold consecutive low-water
// readings and steps down by exactly one worker.
func (p *ScalablePool) maybeScale() {
	if p.closed.Load() {
		return
	}
	q, capacity := len(p.queue), cap(p.queue)
	if capacity == 0 {
		return
	}
	occupancy := float64(q) / float64(capacity)
	n := p.workerCount()

	if occupancy >= p.scaleUpRatio && n < int(p.maxWorker.Load()) {
		// High water resets the shrink streak: a pool that is busy must not
		// carry over credit from an earlier quiet period.
		p.downStreak.Store(0)
		p.addWorker()
		return
	}

	if occupancy <= p.scaleDownRatio && n > int(p.minWorker.Load()) {
		if p.downStreak.Add(1) >= p.scaleDownHold {
			p.downStreak.Store(0)
			p.retireOneWorker()
		}
		return
	}

	// Between the two thresholds the pool is neither short nor idle. Reset the
	// streak so "shrink" really does mean consecutive low-water observations.
	p.downStreak.Store(0)
}

// retireOneWorker asks one worker to exit.
//
// The worker is taken out of the active set BEFORE its retire channel is
// closed, and both happen outside the pool mutex: run() calls removeWorker on
// the same mutex when it observes retire, so closing inside the lock would
// deadlock against it.
func (p *ScalablePool) retireOneWorker() {
	p.mu.Lock()
	var victim *workerRef
	for _, w := range p.workers {
		victim = w
		break
	}
	if victim != nil {
		delete(p.workers, victim.id)
	}
	p.mu.Unlock()

	if victim != nil {
		close(victim.retire)
	}
}

// Stop terminates every worker after draining everything already accepted.
func (p *ScalablePool) Stop() {
	p.beginClose()
	// Retire the workers explicitly. A worker parked in select would only
	// notice done via its done branch, which drains the queue first; letting
	// it exit on done keeps that single drain path and avoids two kinds of
	// worker consuming the same task.
	p.mu.Lock()
	for id, w := range p.workers {
		close(w.retire)
		delete(p.workers, id)
	}
	p.mu.Unlock()

	// Same ordering contract as WorkerPool.Stop: the write lock cannot be
	// granted until every in-flight Submit has finished, and it is held while
	// the workers drain so the final sweep cannot race a new enqueue.
	p.submitMu.Lock()
	defer p.submitMu.Unlock()
	p.wg.Wait()
	p.drainQueue()
}

// addWorker starts one more worker, unless the pool is already at maxWorker or
// already closed.
func (p *ScalablePool) addWorker() {
	if p.closed.Load() {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed.Load() {
		// Closed between the two checks: Stop may already have drained the
		// worker map, and starting now would leave a goroutine nobody waits
		// for.
		return
	}
	if int64(len(p.workers)) >= p.maxWorker.Load() {
		return
	}
	w := &workerRef{id: p.nextID.Add(1), retire: make(chan struct{})}
	p.workers[w.id] = w
	// wg.Go rather than Add+go: it makes the Add and the goroutine start one
	// atomic step. With a plain Add(1) a concurrent Stop reaching wg.Wait
	// between the two would panic with "Add called concurrently with Wait".
	// WorkerPool gets away with the three-line form only because Add happens
	// exclusively in Start; here workers start at run time.
	p.wg.Go(func() { p.run(w) })
}

// workerCount reports how many workers are currently active.
func (p *ScalablePool) workerCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.workers)
}

// run is a worker's main loop.
//
// A worker is a single goroutine, so when retire is observed the task in hand
// is necessarily already finished: that is what makes this the safe point to
// shrink, with no need to interrupt a handler or migrate a task.
func (p *ScalablePool) run(w *workerRef) {
	for {
		select {
		case t := <-p.queue:
			p.handle(t)
		case <-w.retire:
			// Retire without touching the queue. A retired worker that kept
			// consuming would race the remaining workers for tasks and could
			// strip the queue of work the survivors still need to drain.
			p.removeWorker(w.id)
			return
		case <-p.done:
			p.drainQueue()
			p.removeWorker(w.id)
			return
		}
	}
}

// removeWorker drops a worker from the active set.
func (p *ScalablePool) removeWorker(id int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.workers, id)
}
