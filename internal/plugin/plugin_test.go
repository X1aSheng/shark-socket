package plugin

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket-new/internal/core"
	"github.com/X1aSheng/shark-socket-new/internal/infra/store"
	"github.com/X1aSheng/shark-socket-new/internal/runtime"
)

type fakeSession struct {
	addr net.Addr
}

func (s fakeSession) ID() uint64                  { return 1 }
func (s fakeSession) Protocol() core.Protocol     { return core.ProtocolCustom }
func (s fakeSession) RemoteAddr() net.Addr        { return s.addr }
func (s fakeSession) LocalAddr() net.Addr         { return nil }
func (s fakeSession) State() core.SessionState    { return core.StateActive }
func (s fakeSession) CreatedAt() time.Time        { return time.Now() }
func (s fakeSession) LastActiveAt() time.Time     { return time.Now() }
func (s fakeSession) Context() context.Context    { return context.Background() }
func (s fakeSession) Send([]byte) error           { return nil }
func (s fakeSession) Close(context.Context) error { return nil }
func (s fakeSession) SetMeta(string, any)         {}
func (s fakeSession) GetMeta(string) (any, bool)  { return nil, false }
func (s fakeSession) DelMeta(string)              {}

func TestBlacklistBlocksExactIP(t *testing.T) {
	p := NewBlacklist("127.0.0.1")
	sess := fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}}
	if err := p.OnAccept(sess); err != core.ErrPluginBlock {
		t.Fatalf("OnAccept error = %v, want %v", err, core.ErrPluginBlock)
	}
}

func TestRateLimitDropsOverLimit(t *testing.T) {
	p := NewRateLimit(1, time.Second)
	sess := fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}}
	if _, err := p.OnMessage(sess, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.OnMessage(sess, []byte("two")); err != core.ErrPluginDrop {
		t.Fatalf("OnMessage error = %v, want %v", err, core.ErrPluginDrop)
	}
}

func TestAutoBanBlocksAfterThreshold(t *testing.T) {
	p := NewAutoBan(2)
	sess := fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}}
	if p.Record(sess) {
		t.Fatal("first record should not ban")
	}
	if !p.Record(sess) {
		t.Fatal("second record should ban")
	}
	if err := p.OnAccept(sess); err != core.ErrPluginBlock {
		t.Fatalf("OnAccept error = %v, want %v", err, core.ErrPluginBlock)
	}
}

func TestPersistenceWritesLifecycleEvents(t *testing.T) {
	s := store.NewMemory()
	p := NewPersistence(s, "sessions")
	sess := fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}}
	if err := p.OnAccept(sess); err != nil {
		t.Fatal(err)
	}
	p.OnClose(sess)
	value, ok := s.Load("sessions", "custom/1")
	if !ok {
		t.Fatal("persistence event missing")
	}
	if string(value) == "" {
		t.Fatal("persistence event empty")
	}
}

func TestHeartbeatSweepsIdleSessions(t *testing.T) {
	manager := runtime.NewSessionManager()
	sess := &heartbeatSession{fakeSession: fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}}, lastActive: time.Now().Add(-time.Minute)}
	if err := manager.Register(sess); err != nil {
		t.Fatal(err)
	}
	p := NewHeartbeat(manager, time.Second)
	if closed := p.Sweep(time.Now()); closed != 1 {
		t.Fatalf("closed = %d, want 1", closed)
	}
	if manager.Count() != 0 {
		t.Fatalf("manager count = %d, want 0", manager.Count())
	}
	if !sess.closed {
		t.Fatal("session was not closed")
	}
}

type heartbeatSession struct {
	fakeSession
	lastActive time.Time
	closed     bool
}

func (s *heartbeatSession) LastActiveAt() time.Time { return s.lastActive }
func (s *heartbeatSession) Close(context.Context) error {
	s.closed = true
	return nil
}
