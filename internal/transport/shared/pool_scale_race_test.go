package shared

import (
	"sync"
	"testing"
	"time"
)

// TestAddGoWindowIsReal documents WHY the elastic pool must use wg.Go rather
// than the Add(1)+go form that WorkerPool uses.
//
// WorkerPool gets away with Add(1)+go because Add only ever runs inside Start,
// and Start is not called concurrently with Stop. ScalablePool breaks that
// precondition: workers start while the pool runs, so a plain Add(1) followed
// by a separate `go` leaves a window in which Stop can reach wg.Wait() with
// the counter already incremented but the goroutine not yet started. Go
// documents that as a misuse and it panics.
//
// This test does not provoke the panic -- provoking it would make the suite
// flaky by design. It asserts the two properties that make the wg.Go path
// safe, and both are exercised by TestScalablePoolStopConcurrentAdd:
//
//  1. addWorker never runs after Stop has closed the latch, so no counter
//     increment can race a Wait.
//  2. Start and the goroutine body are one indivisible step, so a Wait can
//     never observe a half-started worker.
func TestAddGoWindowIsReal(t *testing.T) {
	// Property 1: after beginClose, addWorker is inert.
	p := NewScalablePool(nil, nil, 1, 8, 8, PolicyDrop)
	p.Start()
	if p.workerCount() != 1 {
		t.Fatalf("workerCount = %d, want 1", p.workerCount())
	}
	p.Stop()

	for range 10 {
		p.addWorker()
	}
	if got := p.workerCount(); got != 0 {
		t.Fatalf("workerCount after Stop = %d, want 0 (addWorker must be inert)", got)
	}

	// Property 2: a Wait concurrent with addWorker terminates rather than
	// deadlocking, and every worker it waited for is accounted for.
	for range 20 {
		q := NewScalablePool(nil, nil, 2, 8, 8, PolicyDrop)
		q.Start()

		var wg sync.WaitGroup
		stopping := make(chan struct{})
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 30 {
				select {
				case <-stopping:
					return
				default:
					q.addWorker()
				}
			}
		}()

		time.Sleep(time.Millisecond)
		stoppingOnce(stopping)
		q.Stop()

		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("adder goroutine did not finish")
		}
		if got := q.workerCount(); got != 0 {
			t.Fatalf("workerCount after Stop = %d, want 0", got)
		}
	}
}

func stoppingOnce(ch chan struct{}) {
	select {
	case <-ch:
	default:
		close(ch)
	}
}
