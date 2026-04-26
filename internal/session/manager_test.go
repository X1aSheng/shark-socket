package session

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/errs"
	"github.com/X1aSheng/shark-socket/internal/types"
)

// testSession is a minimal RawSession implementation for testing.
type testSession struct {
	*BaseSession
	sendCount  atomic.Int64
	closeCount atomic.Int64
	closed     atomic.Bool
}

func newTestSession(id uint64) *testSession {
	return &testSession{
		BaseSession: NewBase(id, types.TCP,
			&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345},
			&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080},
		),
	}
}

func (s *testSession) Send(data []byte) error {
	s.sendCount.Add(1)
	return nil
}

func (s *testSession) SendTyped(msg []byte) error {
	return s.Send(msg)
}

func (s *testSession) Close() error {
	if s.closed.CompareAndSwap(false, true) {
		s.closeCount.Add(1)
		s.DoClose()
	}
	return nil
}

// --- NextID is monotonically increasing ---

func TestManager_NextID_MonotonicallyIncreasing(t *testing.T) {
	m := NewManager()
	defer m.Close()

	var prev uint64
	for i := 0; i < 1000; i++ {
		id := m.NextID()
		if id <= prev {
			t.Errorf("NextID() = %d, not greater than prev %d at iteration %d", id, prev, i)
		}
		prev = id
	}
}

// --- Register/Unregister/Get/Count CRUD ---

func TestManager_RegisterAndGet(t *testing.T) {
	m := NewManager()
	defer m.Close()

	sess := newTestSession(m.NextID())

	if err := m.Register(sess); err != nil {
		t.Fatalf("Register() error: %v", err)
	}

	got, ok := m.Get(sess.ID())
	if !ok {
		t.Fatal("Get() returned false for registered session")
	}
	if got.ID() != sess.ID() {
		t.Errorf("Get() returned session with ID %d, want %d", got.ID(), sess.ID())
	}
}

func TestManager_Unregister(t *testing.T) {
	m := NewManager()
	defer m.Close()

	sess := newTestSession(m.NextID())
	m.Register(sess)
	m.Unregister(sess.ID())

	_, ok := m.Get(sess.ID())
	if ok {
		t.Error("Get() returned true after Unregister")
	}

	if m.Count() != 0 {
		t.Errorf("Count() = %d after Unregister, want 0", m.Count())
	}
}

func TestManager_Count(t *testing.T) {
	m := NewManager()
	defer m.Close()

	const n = 50
	ids := make([]uint64, n)
	for i := 0; i < n; i++ {
		sess := newTestSession(m.NextID())
		ids[i] = sess.ID()
		if err := m.Register(sess); err != nil {
			t.Fatalf("Register(%d) error: %v", i, err)
		}
	}

	if m.Count() != n {
		t.Errorf("Count() = %d, want %d", m.Count(), n)
	}

	// Remove half
	for i := 0; i < n/2; i++ {
		m.Unregister(ids[i])
	}

	if m.Count() != n/2 {
		t.Errorf("Count() after half unregister = %d, want %d", m.Count(), n/2)
	}
}

// --- Get nonexistent returns false ---

func TestManager_Get_Nonexistent(t *testing.T) {
	m := NewManager()
	defer m.Close()

	_, ok := m.Get(99999)
	if ok {
		t.Error("Get(nonexistent) returned true")
	}
}

// --- All() iter.Seq yields all sessions ---

func TestManager_All(t *testing.T) {
	m := NewManager()
	defer m.Close()

	const n = 20
	for i := 0; i < n; i++ {
		sess := newTestSession(m.NextID())
		m.Register(sess)
	}

	collected := make(map[uint64]bool)
	for sess := range m.All() {
		collected[sess.ID()] = true
	}

	if len(collected) != n {
		t.Errorf("All() yielded %d sessions, want %d", len(collected), n)
	}
}

// --- Range iterates all ---

func TestManager_Range(t *testing.T) {
	m := NewManager()
	defer m.Close()

	const n = 15
	for i := 0; i < n; i++ {
		sess := newTestSession(m.NextID())
		m.Register(sess)
	}

	count := 0
	m.Range(func(sess types.RawSession) bool {
		count++
		return true
	})

	if count != n {
		t.Errorf("Range visited %d sessions, want %d", count, n)
	}
}

func TestManager_Range_EarlyStop(t *testing.T) {
	m := NewManager()
	defer m.Close()

	const n = 100
	for i := 0; i < n; i++ {
		sess := newTestSession(m.NextID())
		m.Register(sess)
	}

	count := 0
	m.Range(func(sess types.RawSession) bool {
		count++
		return count < 5 // stop after 5
	})

	if count != 5 {
		t.Errorf("Range with early stop visited %d sessions, want 5", count)
	}
}

// --- Broadcast sends to all alive sessions ---

func TestManager_Broadcast(t *testing.T) {
	m := NewManager()
	defer m.Close()

	const n = 10
	sessions := make([]*testSession, n)
	for i := 0; i < n; i++ {
		sess := newTestSession(m.NextID())
		sess.SetState(types.Active) // make them alive
		sessions[i] = sess
		m.Register(sess)
	}

	// Mark one as not alive
	sessions[5].SetState(types.Closed)

	m.Broadcast([]byte("hello"))

	for i, s := range sessions {
		got := s.sendCount.Load()
		if i == 5 {
			if got != 0 {
				t.Errorf("session[%d] (not alive) got %d sends, want 0", i, got)
			}
		} else {
			if got != 1 {
				t.Errorf("session[%d] got %d sends, want 1", i, got)
			}
		}
	}
}

// --- Close closes all sessions ---

func TestManager_Close(t *testing.T) {
	m := NewManager()

	const n = 10
	for i := 0; i < n; i++ {
		sess := newTestSession(m.NextID())
		m.Register(sess)
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	if m.Count() != 0 {
		t.Errorf("Count() after Close = %d, want 0", m.Count())
	}
}

// --- Register after Close returns error ---

func TestManager_Register_AfterClose(t *testing.T) {
	m := NewManager()
	m.Close()

	sess := newTestSession(m.NextID())
	err := m.Register(sess)
	if err == nil {
		t.Fatal("Register() after Close returned nil error")
	}
	if !errors.Is(err, errs.ErrSessionClosed) {
		t.Errorf("Register() after Close error = %v, want ErrSessionClosed", err)
	}
}

// --- Close is idempotent ---

func TestManager_Close_Idempotent(t *testing.T) {
	m := NewManager()
	sess := newTestSession(m.NextID())
	m.Register(sess)

	if err := m.Close(); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("second Close() error: %v", err)
	}

	// Session should only be closed once
	if sess.closeCount.Load() != 1 {
		t.Errorf("session closed %d times, want 1", sess.closeCount.Load())
	}
}

// --- Concurrent stress tests ---

func TestManager_ConcurrentRegisterUnregister(t *testing.T) {
	m := NewManager(WithMaxSessions(100000))
	defer m.Close()

	const goroutines = 64
	const perGoroutine = 500
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			ids := make([]uint64, 0, perGoroutine)
			for i := 0; i < perGoroutine; i++ {
				sess := newTestSession(m.NextID())
				if err := m.Register(sess); err == nil {
					ids = append(ids, sess.ID())
				}
			}
			for _, id := range ids {
				m.Unregister(id)
			}
		}()
	}
	wg.Wait()

	if m.Count() != 0 {
		t.Errorf("Count() after concurrent register/unregister = %d, want 0", m.Count())
	}
}

func TestManager_ConcurrentRegisterWithCapacityLimit(t *testing.T) {
	const maxSess = 100
	m := NewManager(WithMaxSessions(maxSess))
	defer m.Close()

	const goroutines = 32
	const perGoroutine = 50
	var wg sync.WaitGroup
	var registered atomic.Int64
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				sess := newTestSession(m.NextID())
				if err := m.Register(sess); err == nil {
					registered.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	if m.Count() > maxSess {
		t.Errorf("Count() = %d exceeds maxSess = %d", m.Count(), maxSess)
	}
}
