package runtime

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket-new/internal/core"
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
