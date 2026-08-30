package plugin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
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

// TestRateLimitMemoryBounded verifies that a flooding key does not grow its
// timestamp slice without bound: only accepted requests are recorded, so the
// per-key slice stays at or below the configured rate.
func TestRateLimitMemoryBounded(t *testing.T) {
	p := NewRateLimit(3, time.Minute)
	sess := fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1}}
	for i := 0; i < 1000; i++ {
		_, _ = p.OnMessage(sess, []byte("x"))
	}
	sh := p.shardFor("10.0.0.1")
	sh.mu.Lock()
	stamps := sh.counters["10.0.0.1"]
	sh.mu.Unlock()
	if len(stamps) > 3 {
		t.Fatalf("counter slice length = %d, want <= 3 (bounded by rate)", len(stamps))
	}
}

// TestRateLimitShardsIsolatePeers verifies that two peers hashing to
// different shards never contend on the same lock and are counted
// independently.
func TestRateLimitShardsIsolatePeers(t *testing.T) {
	p := NewRateLimit(2, time.Minute)
	// Scan for two addresses that land on different shards so the test holds
	// regardless of the process-random maphash seed.
	ip := func(n int) string { return fmt.Sprintf("10.%d.%d.%d", n/65536, (n/256)%256, n%256) }
	var keyA, keyB string
	for n := 1; n < 4096 && keyB == ""; n++ {
		candidate := ip(n)
		if keyA == "" {
			keyA = candidate
			continue
		}
		if p.shardFor(candidate) != p.shardFor(keyA) {
			keyB = candidate
		}
	}
	if keyB == "" {
		t.Fatal("could not find two addresses on different shards")
	}
	a := fakeSession{addr: &net.TCPAddr{IP: net.ParseIP(keyA), Port: 1}}
	b := fakeSession{addr: &net.TCPAddr{IP: net.ParseIP(keyB), Port: 2}}
	// Each peer sends 2 accepted messages, then the 3rd is dropped.
	for i := 0; i < 2; i++ {
		if _, err := p.OnMessage(a, []byte("x")); err != nil {
			t.Fatalf("peer a message %d: %v", i, err)
		}
		if _, err := p.OnMessage(b, []byte("x")); err != nil {
			t.Fatalf("peer b message %d: %v", i, err)
		}
	}
	if _, err := p.OnMessage(a, []byte("x")); err != core.ErrPluginDrop {
		t.Fatalf("peer a over-limit error = %v, want %v", err, core.ErrPluginDrop)
	}
	if _, err := p.OnMessage(b, []byte("x")); err != core.ErrPluginDrop {
		t.Fatalf("peer b over-limit error = %v, want %v", err, core.ErrPluginDrop)
	}
}

// TestRateLimitSessionKeyCached verifies the remote-IP key is cached in
// session meta after the first message and stays stable.
func TestRateLimitSessionKeyCached(t *testing.T) {
	p := NewRateLimit(10, time.Minute)
	sess := &metaTrackingSession{fakeSession: fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1}}}
	first := p.sessionKey(sess)
	if first != "10.0.0.1" {
		t.Fatalf("session key = %q, want 10.0.0.1", first)
	}
	second := p.sessionKey(sess)
	if second != first {
		t.Fatalf("cached key = %q, want %q", second, first)
	}
	if sess.sets != 1 {
		t.Fatalf("SetMeta calls = %d, want 1 (key computed once)", sess.sets)
	}
}

// metaTrackingSession counts SetMeta calls to verify one-time key caching.
type metaTrackingSession struct {
	fakeSession
	meta map[string]any
	sets int
}

func (s *metaTrackingSession) SetMeta(k string, v any) {
	s.sets++
	if s.meta == nil {
		s.meta = make(map[string]any)
	}
	s.meta[k] = v
}

func (s *metaTrackingSession) GetMeta(k string) (any, bool) {
	if s.meta == nil {
		return nil, false
	}
	v, ok := s.meta[k]
	return v, ok
}

// closeTrackingSession records Close calls so tests can assert a session was
// terminated.
type closeTrackingSession struct {
	fakeSession
	closes atomic.Int32
}

func (s *closeTrackingSession) Close(context.Context) error {
	s.closes.Add(1)
	return nil
}

// TestAutoBanClosesEstablishedSession verifies that a live session which trips
// the ban threshold is closed, not just dropped (OnAccept only blocks new
// connections).
func TestAutoBanClosesEstablishedSession(t *testing.T) {
	p := NewAutoBan(1)
	sess := &closeTrackingSession{fakeSession: fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.2"), Port: 2}}}
	if _, err := p.OnMessage(sess, []byte("x")); err != core.ErrPluginDrop {
		t.Fatalf("OnMessage error = %v, want %v", err, core.ErrPluginDrop)
	}
	if sess.closes.Load() != 1 {
		t.Fatalf("session Close calls = %d, want 1", sess.closes.Load())
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
	_, ok, err := s.Load("sessions", "custom/1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("persistence event missing")
	}
}

func TestHeartbeatSweepsIdleSessions(t *testing.T) {
	manager := runtime.NewSessionManager()
	sess := &heartbeatSession{fakeSession: fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}}, lastActive: time.Now().Add(-time.Minute)}
	if err := manager.Register(sess); err != nil {
		t.Fatal(err)
	}
	p := NewHeartbeat(manager, time.Second, 0)
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
	p := NewHeartbeat(manager, time.Millisecond, time.Millisecond)
	_ = p.Start()
	_ = p.Start()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if manager.Count() == 0 {
			_ = p.Stop()
			_ = p.Stop()
			return
		}
		time.Sleep(time.Millisecond)
	}
	_ = p.Stop()
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

func TestPersistenceOnMessageAppendsToLog(t *testing.T) {
	s := store.NewMemory()
	p := NewPersistence(s, "sessions")
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
