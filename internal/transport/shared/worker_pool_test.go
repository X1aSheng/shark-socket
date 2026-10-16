package shared

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// poolTestSession is the minimal core.Session a WorkerPool needs: an ID, a
// context that Done() closes, and a Close that cancels it.
type poolTestSession struct {
	id     uint64
	ctx    context.Context
	cancel context.CancelFunc
	closed atomic.Bool
}

func newPoolTestSession(id uint64) *poolTestSession {
	ctx, cancel := context.WithCancel(context.Background())
	return &poolTestSession{id: id, ctx: ctx, cancel: cancel}
}

func (s *poolTestSession) ID() uint64                 { return s.id }
func (s *poolTestSession) Protocol() core.Protocol    { return core.ProtocolTCP }
func (s *poolTestSession) RemoteAddr() net.Addr       { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }
func (s *poolTestSession) LocalAddr() net.Addr        { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }
func (s *poolTestSession) State() core.SessionState   { return core.StateActive }
func (s *poolTestSession) CreatedAt() time.Time       { return time.Now() }
func (s *poolTestSession) LastActiveAt() time.Time    { return time.Now() }
func (s *poolTestSession) Context() context.Context   { return s.ctx }
func (s *poolTestSession) Send([]byte) error          { return nil }
func (s *poolTestSession) SetMeta(string, any)        {}
func (s *poolTestSession) GetMeta(string) (any, bool) { return nil, false }
func (s *poolTestSession) DelMeta(string)             {}

func (s *poolTestSession) Close(context.Context) error {
	if s.closed.CompareAndSwap(false, true) {
		s.cancel()
	}
	return nil
}

// recordingLogger captures the log records a component emits.
type recordingLogger struct {
	mu     sync.Mutex
	errors []string
}

func (l *recordingLogger) Debug(string, ...any) {}
func (l *recordingLogger) Info(string, ...any)  {}
func (l *recordingLogger) Warn(string, ...any)  {}
func (l *recordingLogger) Error(msg string, _ ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errors = append(l.errors, msg)
}

func (l *recordingLogger) errorCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.errors)
}

func TestWorkerPoolDropPolicyRejectsWhenFull(t *testing.T) {
	// The handler signals when it has taken the first task, which is what
	// makes "the queue is full" a fact rather than a guess. Submitting twice
	// back to back used to be enough, but the single worker could dequeue the
	// first task before the second submit ran, and then the queue was empty
	// and the submit succeeded. Waiting for the handler to start instead pins
	// the queue at empty, so the next submit fills it and the one after that
	// must be rejected.
	inHandler := make(chan struct{}, 1)
	release := make(chan struct{})
	pool := NewWorkerPool(func(core.Session, core.Message) error {
		// Non-blocking: Stop drains the queue afterwards, so the handler runs
		// more than once and a plain close would panic on the second call.
		select {
		case inHandler <- struct{}{}:
		default:
		}
		<-release
		return nil
	}, nil, 1, 1, PolicyDrop)
	pool.Start(1)
	defer func() {
		close(release)
		pool.Stop()
	}()

	if err := pool.Submit(newPoolTestSession(1), core.ProtocolTCP, []byte("one")); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	select {
	case <-inHandler:
	case <-time.After(2 * time.Second):
		t.Fatal("worker never started handling the first task")
	}
	// The worker is parked inside the handler, so the one-slot queue is empty:
	// this submit fills it.
	if err := pool.Submit(newPoolTestSession(2), core.ProtocolTCP, []byte("two")); err != nil {
		t.Fatalf("second submit: %v", err)
	}
	err := pool.Submit(newPoolTestSession(3), core.ProtocolTCP, []byte("three"))
	if !errors.Is(err, core.ErrWriteQueueFull) {
		t.Fatalf("third submit error = %v, want %v", err, core.ErrWriteQueueFull)
	}
}

func TestWorkerPoolClosesSessionOnHandlerError(t *testing.T) {
	sess := newPoolTestSession(7)
	pool := NewWorkerPool(func(core.Session, core.Message) error {
		return errors.New("handler failed")
	}, nil, 1, 1, PolicyBlock)
	pool.Start(1)
	if err := pool.Submit(sess, core.ProtocolTCP, []byte("boom")); err != nil {
		t.Fatalf("submit: %v", err)
	}
	pool.Stop()

	select {
	case <-sess.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("session was not closed after handler error")
	}
}

// TestWorkerPoolLogsHandlerPanic pins the fix for a silent failure: the pool
// used to convert a handler panic into ErrHandlerPanic with no diagnostic, so
// an operator saw sessions vanish with nothing in the log. shared.CallHandler
// recovers, logs, and the pool then closes the session.
func TestWorkerPoolLogsHandlerPanic(t *testing.T) {
	logger := &recordingLogger{}
	sess := newPoolTestSession(11)
	pool := NewWorkerPool(func(core.Session, core.Message) error {
		panic("handler exploded")
	}, logger, 1, 1, PolicyBlock)
	pool.Start(1)
	if err := pool.Submit(sess, core.ProtocolTCP, []byte("boom")); err != nil {
		t.Fatalf("submit: %v", err)
	}
	pool.Stop()

	if logger.errorCount() == 0 {
		t.Fatal("handler panic was not logged; operators get no diagnostic")
	}
	select {
	case <-sess.Context().Done():
	default:
		t.Fatal("session was not closed after handler panic")
	}
}

func TestWorkerPoolDisabledWithoutHandler(t *testing.T) {
	pool := NewWorkerPool(nil, nil, 1, 1, PolicyBlock)
	if pool.Enabled() {
		t.Fatal("Enabled = true for a nil handler")
	}
	pool.Start(1)
	pool.Stop()
}

// TestWorkerPoolSubmitStopRace guards the submit/stop race that could panic
// with "send on closed channel" when Stop closed the task queue.
func TestWorkerPoolSubmitStopRace(t *testing.T) {
	for iter := 0; iter < 200; iter++ {
		p := NewWorkerPool(nil, nil, 1, 2, PolicyBlock)
		p.Start(1)
		stop := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = p.Submit(nil, core.ProtocolTCP, []byte("x"))
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(5 * time.Millisecond)
			p.Stop()
			close(stop)
		}()
		wg.Wait()
	}
}
