package plugin

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/infra/observability"
	"github.com/X1aSheng/shark-socket/internal/infra/store"
	"github.com/X1aSheng/shark-socket/internal/runtime"
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

func TestHeartbeatStartStopIsIdempotent(t *testing.T) {
	manager := runtime.NewSessionManager()
	sess := &heartbeatSession{fakeSession: fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}}, lastActive: time.Now().Add(-time.Minute)}
	if err := manager.Register(sess); err != nil {
		t.Fatal(err)
	}
	p := NewHeartbeat(manager, time.Millisecond)
	p.Start(time.Millisecond)
	p.Start(time.Millisecond)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if manager.Count() == 0 {
			p.Stop()
			p.Stop()
			return
		}
		time.Sleep(time.Millisecond)
	}
	p.Stop()
	t.Fatal("heartbeat loop did not sweep idle session")
}

func TestSlowHandlerLogsDurationAndReturnsError(t *testing.T) {
	logger := observability.NewMemoryLogger()
	now := time.Unix(0, 0)
	handlerErr := errors.New("boom")
	wrapped := NewSlowHandler(
		logger,
		time.Millisecond,
		func(core.Session, core.Message) error {
			now = now.Add(2 * time.Millisecond)
			return handlerErr
		},
		WithSlowHandlerClock(func() time.Time { return now }),
	)
	msg := core.Message{SessionID: 1, Protocol: core.ProtocolTCP, Payload: []byte("hello")}
	if err := wrapped(fakeSession{}, msg); !errors.Is(err, handlerErr) {
		t.Fatalf("handler error = %v, want %v", err, handlerErr)
	}
	entries := logger.Entries()
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Level != "warn" || entries[0].Msg != "slow handler" {
		t.Fatalf("entry = %#v", entries[0])
	}
	if !attrsContain(entries[0].Attrs, "error", "boom") {
		t.Fatalf("attrs missing error: %#v", entries[0].Attrs)
	}
}

func TestPersistenceV2WritesLifecycleEvents(t *testing.T) {
	s := store.NewMemory()
	p := NewPersistenceV2(s, "sessions")
	sess := fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}}
	if err := p.OnAccept(sess); err != nil {
		t.Fatal(err)
	}
	p.OnClose(sess)
	_, ok, err := s.LoadV2("sessions", "custom/1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("persistence-v2 event missing")
	}
}

func TestPersistenceV2OnMessageAppendsToLog(t *testing.T) {
	s := store.NewMemory()
	p := NewPersistenceV2(s, "sessions")
	sess := fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}}
	data, err := p.OnMessage(sess, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("data = %q, want hello", string(data))
	}
	log := p.MessageLog()
	if log == nil {
		t.Fatal("message log is nil")
	}
	n, err := log.Len()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("log len = %d, want 1", n)
	}
}

func TestSlowHandlerSkipsFastHandler(t *testing.T) {
	logger := observability.NewMemoryLogger()
	now := time.Unix(0, 0)
	wrapped := NewSlowHandler(
		logger,
		time.Second,
		func(core.Session, core.Message) error {
			now = now.Add(time.Millisecond)
			return nil
		},
		WithSlowHandlerClock(func() time.Time { return now }),
	)
	if err := wrapped(fakeSession{}, core.Message{SessionID: 1, Protocol: core.ProtocolTCP}); err != nil {
		t.Fatal(err)
	}
	if entries := logger.Entries(); len(entries) != 0 {
		t.Fatalf("entries = %#v, want none", entries)
	}
}

func attrsContain(attrs []any, key string, value any) bool {
	for i := 0; i+1 < len(attrs); i += 2 {
		if attrs[i] == key && attrs[i+1] == value {
			return true
		}
	}
	return false
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
