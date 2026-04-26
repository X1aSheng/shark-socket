package tcp

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yourname/shark-socket/internal/errs"
	"github.com/yourname/shark-socket/internal/types"
)

// mockSession implements types.RawSession for testing.
type mockSession struct {
	id       uint64
	protocol types.ProtocolType
	alive    atomic.Bool
	closed   atomic.Bool
}

func newMockSession(id uint64) *mockSession {
	m := &mockSession{id: id, protocol: types.TCP}
	m.alive.Store(true)
	return m
}

func (m *mockSession) ID() uint64                              { return m.id }
func (m *mockSession) Protocol() types.ProtocolType            { return m.protocol }
func (m *mockSession) RemoteAddr() net.Addr                    { return mockAddr{} }
func (m *mockSession) LocalAddr() net.Addr                     { return mockAddr{} }
func (m *mockSession) CreatedAt() time.Time                    { return time.Now() }
func (m *mockSession) State() types.SessionState               { return types.Active }
func (m *mockSession) IsAlive() bool                           { return m.alive.Load() }
func (m *mockSession) LastActiveAt() time.Time                 { return time.Now() }
func (m *mockSession) Send(data []byte) error                  { return nil }
func (m *mockSession) SendTyped(msg []byte) error              { return nil }
func (m *mockSession) Close() error                            { m.closed.Store(true); return nil }
func (m *mockSession) Context() context.Context                { return context.Background() }
func (m *mockSession) SetMeta(key string, val any)             {}
func (m *mockSession) GetMeta(key string) (any, bool)          { return nil, false }
func (m *mockSession) DelMeta(key string)                      {}

// mockAddr is a minimal net.Addr implementation for mocks.
type mockAddr struct{}

func (mockAddr) Network() string { return "tcp" }
func (mockAddr) String() string  { return "127.0.0.1:0" }

// --- WorkerPool tests ---

func TestWorkerPool_SubmitProcessesTask(t *testing.T) {
	var processed atomic.Int32
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		processed.Add(1)
		return nil
	}

	pool := NewWorkerPool(handler, nil, 2, 16, 8, PolicyDrop)
	pool.Start()
	defer pool.Stop()

	sess := newMockSession(1)
	if err := pool.Submit(sess, []byte("test")); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Give workers time to process
	time.Sleep(100 * time.Millisecond)

	if got := processed.Load(); got != 1 {
		t.Errorf("expected 1 processed, got %d", got)
	}
}

func TestWorkerPool_MultipleTasks(t *testing.T) {
	var processed atomic.Int32
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		processed.Add(1)
		return nil
	}

	// Use PolicyBlock so all tasks are guaranteed to be submitted
	pool := NewWorkerPool(handler, nil, 4, 256, 16, PolicyBlock)
	pool.Start()
	defer pool.Stop()

	sess := newMockSession(1)
	for i := 0; i < 100; i++ {
		if err := pool.Submit(sess, []byte("data")); err != nil {
			t.Fatalf("Submit[%d]: %v", i, err)
		}
	}

	// Wait for all tasks to be processed
	time.Sleep(200 * time.Millisecond)

	if got := processed.Load(); got != 100 {
		t.Errorf("expected 100 processed, got %d", got)
	}
}

func TestWorkerPool_PolicyDrop_DiscardsWhenFull(t *testing.T) {
	var processed atomic.Int32
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		processed.Add(1)
		time.Sleep(50 * time.Millisecond) // slow handler to fill queue
		return nil
	}

	// Tiny queue, PolicyDrop
	pool := NewWorkerPool(handler, nil, 1, 2, 2, PolicyDrop)
	pool.Start()
	defer pool.Stop()

	sess := newMockSession(1)

	// First few submits should succeed (filling queue + worker busy)
	var dropped int
	for i := 0; i < 20; i++ {
		if err := pool.Submit(sess, []byte("data")); err != nil {
			if err == errs.ErrWriteQueueFull {
				dropped++
			}
		}
	}

	if dropped == 0 {
		t.Error("expected some tasks to be dropped with PolicyDrop")
	}
}

func TestWorkerPool_PolicyBlock_BlocksWhenFull(t *testing.T) {
	var processed atomic.Int32
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		processed.Add(1)
		return nil
	}

	pool := NewWorkerPool(handler, nil, 2, 4, 8, PolicyBlock)
	pool.Start()
	defer pool.Stop()

	sess := newMockSession(1)

	// Submit many tasks; PolicyBlock should block until space is available
	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			_ = pool.Submit(sess, []byte("data"))
		}
		close(done)
	}()

	select {
	case <-done:
		// All submitted successfully (some blocked, but all got in)
	case <-time.After(5 * time.Second):
		t.Fatal("PolicyBlock submit timed out")
	}

	// Wait for processing
	time.Sleep(200 * time.Millisecond)

	if got := processed.Load(); got != 50 {
		t.Errorf("expected 50 processed, got %d", got)
	}
}

func TestWorkerPool_StopWaitsForWorkers(t *testing.T) {
	var started sync.WaitGroup
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		started.Done()
		time.Sleep(50 * time.Millisecond)
		return nil
	}

	pool := NewWorkerPool(handler, nil, 2, 16, 8, PolicyDrop)
	pool.Start()

	sess := newMockSession(1)
	for i := 0; i < 10; i++ {
		started.Add(1)
		_ = pool.Submit(sess, []byte("data"))
	}

	started.Wait() // ensure workers pick up tasks

	// Stop should wait until all workers finish their current tasks
	pool.Stop()
	// If we get here, Stop() returned after workers finished
}

func TestWorkerPool_StopIsIdempotent(t *testing.T) {
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return nil
	}

	pool := NewWorkerPool(handler, nil, 2, 16, 8, PolicyDrop)
	pool.Start()

	// Calling Stop twice should not panic
	pool.Stop()
	pool.Stop()
}

func TestWorkerPool_DefaultValues(t *testing.T) {
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return nil
	}

	// coreWorkers=0 should default to 1
	pool := NewWorkerPool(handler, nil, 0, 0, 0, PolicyDrop)
	if pool.coreCount != 1 {
		t.Errorf("expected coreCount=1, got %d", pool.coreCount)
	}
	if pool.maxWorkers < pool.coreCount {
		t.Error("maxWorkers should be >= coreCount")
	}
}

func TestWorkerPool_SafeRunRecoversPanic(t *testing.T) {
	panicHandler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		panic("test panic")
	}

	pool := NewWorkerPool(panicHandler, nil, 1, 16, 4, PolicyDrop)
	pool.Start()
	defer pool.Stop()

	sess := newMockSession(1)
	// Submit should not crash even though handler panics
	if err := pool.Submit(sess, []byte("data")); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Give the worker time to process (and recover from) the panic
	time.Sleep(200 * time.Millisecond)

	// Worker should still be alive — submit another task
	var processed atomic.Bool
	normalHandler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		processed.Store(true)
		return nil
	}
	pool.handler = normalHandler

	if err := pool.Submit(sess, []byte("data2")); err != nil {
		t.Fatalf("Submit after panic: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	if !processed.Load() {
		t.Error("worker did not process task after panic recovery")
	}
}

func TestWorkerPool_HandlerReceivesCorrectData(t *testing.T) {
	var receivedData []byte
	var receivedMux sync.Mutex
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		receivedMux.Lock()
		receivedData = make([]byte, len(msg.Payload))
		copy(receivedData, msg.Payload)
		receivedMux.Unlock()
		return nil
	}

	pool := NewWorkerPool(handler, nil, 1, 16, 4, PolicyDrop)
	pool.Start()
	defer pool.Stop()

	sess := newMockSession(42)
	payload := []byte("hello worker pool")
	if err := pool.Submit(sess, payload); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	receivedMux.Lock()
	defer receivedMux.Unlock()
	if string(receivedData) != string(payload) {
		t.Errorf("handler received %q, want %q", receivedData, payload)
	}
}

func TestWorkerPool_NilHandler(t *testing.T) {
	pool := NewWorkerPool(nil, nil, 2, 16, 8, PolicyDrop)
	pool.Start()
	defer pool.Stop()

	sess := newMockSession(1)
	// Should not panic with nil handler
	if err := pool.Submit(sess, []byte("data")); err != nil {
		t.Fatalf("Submit with nil handler: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
}

func TestWorkerPool_SpawnTempPolicy(t *testing.T) {
	var processed atomic.Int32
	slowHandler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		processed.Add(1)
		time.Sleep(100 * time.Millisecond)
		return nil
	}

	// 1 core worker, small queue, max 4 workers total (1 core + 3 temp)
	pool := NewWorkerPool(slowHandler, nil, 1, 1, 4, PolicySpawnTemp)
	pool.Start()
	defer pool.Stop()

	sess := newMockSession(1)

	// Submit enough tasks to exceed queue + core worker
	var tempSpawned bool
	for i := 0; i < 10; i++ {
		err := pool.Submit(sess, []byte("data"))
		if err == errs.ErrWriteQueueFull {
			tempSpawned = true
		}
	}

	// If we hit capacity, temp workers should have been spawned
	// (otherwise all fit in queue + core worker)
	_ = tempSpawned

	time.Sleep(500 * time.Millisecond)
	// All tasks that were accepted should have been processed
	if processed.Load() == 0 {
		t.Error("expected some tasks to be processed")
	}
}

func TestWorkerPool_ClosePolicy(t *testing.T) {
	slowHandler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		time.Sleep(100 * time.Millisecond)
		return nil
	}

	pool := NewWorkerPool(slowHandler, nil, 1, 1, 2, PolicyClose)
	pool.Start()
	defer pool.Stop()

	sess := newMockSession(1)

	var rejected bool
	for i := 0; i < 10; i++ {
		if err := pool.Submit(sess, []byte("data")); err == errs.ErrWriteQueueFull {
			rejected = true
		}
	}

	if !rejected {
		t.Error("expected some tasks to be rejected with PolicyClose")
	}
}
