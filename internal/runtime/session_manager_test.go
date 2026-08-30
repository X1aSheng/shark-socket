package runtime

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type fakeSession struct {
	id     uint64
	closed bool
}

func (s *fakeSession) ID() uint64                  { return s.id }
func (s *fakeSession) Protocol() core.Protocol     { return core.ProtocolCustom }
func (s *fakeSession) RemoteAddr() net.Addr        { return nil }
func (s *fakeSession) LocalAddr() net.Addr         { return nil }
func (s *fakeSession) State() core.SessionState    { return core.StateActive }
func (s *fakeSession) CreatedAt() time.Time        { return time.Now() }
func (s *fakeSession) LastActiveAt() time.Time     { return time.Now() }
func (s *fakeSession) Context() context.Context    { return context.Background() }
func (s *fakeSession) Send([]byte) error           { return nil }
func (s *fakeSession) Close(context.Context) error { s.closed = true; return nil }
func (s *fakeSession) SetMeta(string, any)         {}
func (s *fakeSession) GetMeta(string) (any, bool)  { return nil, false }
func (s *fakeSession) DelMeta(string)              {}

func TestSessionManagerCloseAll(t *testing.T) {
	m := NewSessionManager()
	sess := &fakeSession{id: m.NextID()}
	if err := m.Register(sess); err != nil {
		t.Fatal(err)
	}
	if err := m.CloseAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !sess.closed {
		t.Fatal("session was not closed")
	}
	if m.Count() != 0 {
		t.Fatalf("count = %d, want 0", m.Count())
	}
}

func TestSessionManagerCapacityAndBroadcast(t *testing.T) {
	m := NewSessionManager(WithMaxSessions(1))
	first := &fakeSession{id: m.NextID()}
	if err := m.Register(first); err != nil {
		t.Fatal(err)
	}
	if err := m.Register(&fakeSession{id: m.NextID()}); !errors.Is(err, core.ErrSessionCapacity) {
		t.Fatalf("register over capacity error = %v, want %v", err, core.ErrSessionCapacity)
	}
	if got := len(m.Snapshot()); got != 1 {
		t.Fatalf("snapshot length = %d, want 1", got)
	}
	if err := m.Broadcast([]byte("hello")); err != nil {
		t.Fatal(err)
	}
}

// selfRegisteringSession registers one successor session when closed,
// simulating a transport registering sessions while shutdown is in progress
// (the V8 P2-1 scenario).
type selfRegisteringSession struct {
	fakeSession
	manager    *SessionManager
	registered atomic.Bool
}

func (s *selfRegisteringSession) Close(context.Context) error {
	s.closed = true
	if s.registered.CompareAndSwap(false, true) {
		next := &fakeSession{id: s.manager.NextID()}
		_ = s.manager.Register(next)
	}
	return nil
}

// TestSessionManagerCloseAllDrainsMidShutdownRegistrations is the regression
// test for the V8 P2-1 fix: CloseAll must loop until the manager is empty, so
// sessions registered while the drain is running are closed too instead of
// being leaked (the pre-fix implementation closed a single snapshot).
func TestSessionManagerCloseAllDrainsMidShutdownRegistrations(t *testing.T) {
	m := NewSessionManager()
	sess := &selfRegisteringSession{
		fakeSession: fakeSession{id: m.NextID()},
		manager:     m,
	}
	if err := m.Register(sess); err != nil {
		t.Fatal(err)
	}
	if err := m.CloseAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if count := m.Count(); count != 0 {
		t.Fatalf("count = %d, want 0 (mid-shutdown registrations leaked)", count)
	}
}

// TestSessionManagerCloseAllAbortsOnCancelledContext verifies that a
// cancelled context aborts the drain immediately instead of closing (or
// hanging on) sessions.
func TestSessionManagerCloseAllAbortsOnCancelledContext(t *testing.T) {
	m := NewSessionManager()
	sess := &fakeSession{id: m.NextID()}
	if err := m.Register(sess); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// The exact error is nil on a clean abort (nothing failed yet); the
	// assertion that matters is that CloseAll returns promptly and does not
	// close the session, which would otherwise be the drain's job.
	if err := m.CloseAll(ctx); err != nil {
		t.Fatalf("CloseAll error = %v, want nil (clean abort)", err)
	}
	if sess.closed {
		t.Fatal("session closed despite cancelled context")
	}
	if m.Count() != 1 {
		t.Fatalf("count = %d, want 1 (abort must not remove sessions)", m.Count())
	}
}
