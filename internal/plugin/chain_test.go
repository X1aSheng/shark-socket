package plugin

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yourname/shark-socket/internal/errs"
	"github.com/yourname/shark-socket/internal/types"
)

// ---------------------------------------------------------------------------
// mockRawSession implements types.RawSession for tests.
// ---------------------------------------------------------------------------

type mockRawSession struct {
	id         uint64
	protocol   types.ProtocolType
	remoteAddr net.Addr
	localAddr  net.Addr
	createdAt  time.Time
	state      types.SessionState
	alive      bool
	lastActive time.Time
	closed     atomic.Bool
	meta       map[string]any
}

func newMockSession(id uint64, remoteIP string) *mockRawSession {
	ip := net.ParseIP(remoteIP)
	if ip == nil {
		ip = net.ParseIP("127.0.0.1")
	}
	return &mockRawSession{
		id:         id,
		protocol:   types.TCP,
		remoteAddr: &net.TCPAddr{IP: ip, Port: 12345},
		localAddr:  &net.TCPAddr{IP: net.ParseIP("0.0.0.0"), Port: 8080},
		createdAt:  time.Now(),
		state:      types.Active,
		alive:      true,
		lastActive: time.Now(),
		meta:       make(map[string]any),
	}
}

func (m *mockRawSession) ID() uint64                    { return m.id }
func (m *mockRawSession) Protocol() types.ProtocolType  { return m.protocol }
func (m *mockRawSession) RemoteAddr() net.Addr          { return m.remoteAddr }
func (m *mockRawSession) LocalAddr() net.Addr           { return m.localAddr }
func (m *mockRawSession) CreatedAt() time.Time          { return m.createdAt }
func (m *mockRawSession) State() types.SessionState     { return m.state }
func (m *mockRawSession) IsAlive() bool                 { return m.alive }
func (m *mockRawSession) LastActiveAt() time.Time       { return m.lastActive }
func (m *mockRawSession) Send(data []byte) error        { return nil }
func (m *mockRawSession) SendTyped(msg []byte) error    { return nil }
func (m *mockRawSession) Context() context.Context      { return context.Background() }
func (m *mockRawSession) SetMeta(key string, val any)   { m.meta[key] = val }
func (m *mockRawSession) GetMeta(key string) (any, bool) { v, ok := m.meta[key]; return v, ok }
func (m *mockRawSession) DelMeta(key string)            { delete(m.meta, key) }
func (m *mockRawSession) Close() error {
	m.closed.Store(true)
	m.alive = false
	m.state = types.Closed
	return nil
}

// ---------------------------------------------------------------------------
// stubPlugin is a configurable test plugin.
// ---------------------------------------------------------------------------

type stubPlugin struct {
	name      string
	priority  int
	onAccept  func(types.RawSession) error
	onMessage func(types.RawSession, []byte) ([]byte, error)
	onClose   func(types.RawSession)
	closeCalled atomic.Int32
}

func (s *stubPlugin) Name() string  { return s.name }
func (s *stubPlugin) Priority() int { return s.priority }

func (s *stubPlugin) OnAccept(sess types.RawSession) error {
	if s.onAccept != nil {
		return s.onAccept(sess)
	}
	return nil
}

func (s *stubPlugin) OnMessage(sess types.RawSession, data []byte) ([]byte, error) {
	if s.onMessage != nil {
		return s.onMessage(sess, data)
	}
	return data, nil
}

func (s *stubPlugin) OnClose(sess types.RawSession) {
	s.closeCalled.Add(1)
	if s.onClose != nil {
		s.onClose(sess)
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestNewChain_SortsByPriority(t *testing.T) {
	p1 := &stubPlugin{name: "low", priority: 100}
	p2 := &stubPlugin{name: "high", priority: 1}
	p3 := &stubPlugin{name: "mid", priority: 50}

	chain := NewChain(p1, p2, p3)
	if chain.Len() != 3 {
		t.Fatalf("expected 3 plugins, got %d", chain.Len())
	}

	// Verify ordering: high(1) -> mid(50) -> low(100)
	order := []string{"high", "mid", "low"}
	for i, expected := range order {
		// Access plugins via reflection of the chain internal ordering
		// We verify by calling OnMessage and checking the transformation order
		_ = i
		_ = expected
	}

	// Functional verification: build a chain that appends to a slice on each call.
	var orderSeen []string
	recordName := func(n string) func(types.RawSession, []byte) ([]byte, error) {
		return func(_ types.RawSession, data []byte) ([]byte, error) {
			orderSeen = append(orderSeen, n)
			return data, nil
		}
	}

	p1.onMessage = recordName("low")
	p2.onMessage = recordName("high")
	p3.onMessage = recordName("mid")

	sess := newMockSession(1, "127.0.0.1")
	_, err := chain.OnMessage(sess, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"high", "mid", "low"}
	if len(orderSeen) != len(want) {
		t.Fatalf("expected %d calls, got %d", len(want), len(orderSeen))
	}
	for i, name := range want {
		if orderSeen[i] != name {
			t.Errorf("call %d: expected %q, got %q", i, name, orderSeen[i])
		}
	}
}

func TestChain_OnAccept_ErrBlock_Interrupts(t *testing.T) {
	called := atomic.Int32{}

	p1 := &stubPlugin{
		name:     "blocker",
		priority: 1,
		onAccept: func(sess types.RawSession) error {
			called.Add(1)
			return errs.ErrBlock
		},
	}
	p2 := &stubPlugin{
		name:     "after",
		priority: 2,
		onAccept: func(sess types.RawSession) error {
			called.Add(1)
			return nil
		},
	}

	chain := NewChain(p1, p2)
	sess := newMockSession(1, "127.0.0.1")

	err := chain.OnAccept(sess)
	if !errors.Is(err, errs.ErrBlock) {
		t.Fatalf("expected ErrBlock, got %v", err)
	}
	if called.Load() != 1 {
		t.Fatalf("expected only 1 plugin called, got %d", called.Load())
	}
	if !sess.closed.Load() {
		t.Fatal("session should be closed after ErrBlock")
	}
}

func TestChain_OnMessage_ErrSkip_ReturnsOriginalData(t *testing.T) {
	p := &stubPlugin{
		name:     "skipper",
		priority: 1,
		onMessage: func(_ types.RawSession, data []byte) ([]byte, error) {
			return nil, errs.ErrSkip
		},
	}

	chain := NewChain(p)
	sess := newMockSession(1, "127.0.0.1")
	original := []byte("original")

	out, err := chain.OnMessage(sess, original)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(original) {
		t.Fatalf("expected %q, got %q", original, out)
	}
}

func TestChain_OnMessage_ErrDrop_ReturnsNilData(t *testing.T) {
	p := &stubPlugin{
		name:     "dropper",
		priority: 1,
		onMessage: func(_ types.RawSession, data []byte) ([]byte, error) {
			return nil, errs.ErrDrop
		},
	}

	chain := NewChain(p)
	sess := newMockSession(1, "127.0.0.1")

	out, err := chain.OnMessage(sess, []byte("data"))
	if !errors.Is(err, errs.ErrDrop) {
		t.Fatalf("expected ErrDrop, got %v", err)
	}
	if out != nil {
		t.Fatalf("expected nil data, got %q", out)
	}
}

func TestChain_OnMessage_DataPassesThrough(t *testing.T) {
	p1 := &stubPlugin{
		name:     "upper",
		priority: 1,
		onMessage: func(_ types.RawSession, data []byte) ([]byte, error) {
			return []byte(string(data) + "-p1"), nil
		},
	}
	p2 := &stubPlugin{
		name:     "lower",
		priority: 2,
		onMessage: func(_ types.RawSession, data []byte) ([]byte, error) {
			return []byte(string(data) + "-p2"), nil
		},
	}

	chain := NewChain(p1, p2)
	sess := newMockSession(1, "127.0.0.1")

	out, err := chain.OnMessage(sess, []byte("start"))
	if err != nil {
		t.Fatal(err)
	}
	expected := "start-p1-p2"
	if string(out) != expected {
		t.Fatalf("expected %q, got %q", expected, out)
	}
}

func TestChain_OnClose_ReverseOrder(t *testing.T) {
	var orderSeen []string

	p1 := &stubPlugin{
		name:     "first",
		priority: 1,
		onClose: func(_ types.RawSession) { orderSeen = append(orderSeen, "first") },
	}
	p2 := &stubPlugin{
		name:     "second",
		priority: 2,
		onClose: func(_ types.RawSession) { orderSeen = append(orderSeen, "second") },
	}
	p3 := &stubPlugin{
		name:     "third",
		priority: 3,
		onClose: func(_ types.RawSession) { orderSeen = append(orderSeen, "third") },
	}

	chain := NewChain(p1, p2, p3)
	sess := newMockSession(1, "127.0.0.1")

	chain.OnClose(sess)

	want := []string{"third", "second", "first"}
	if len(orderSeen) != len(want) {
		t.Fatalf("expected %d calls, got %d", len(want), len(orderSeen))
	}
	for i, name := range want {
		if orderSeen[i] != name {
			t.Errorf("call %d: expected %q, got %q", i, name, orderSeen[i])
		}
	}
}

func TestChain_EmptyChain_PassesThrough(t *testing.T) {
	chain := NewChain()
	sess := newMockSession(1, "127.0.0.1")

	// OnAccept
	if err := chain.OnAccept(sess); err != nil {
		t.Fatalf("empty chain OnAccept: %v", err)
	}

	// OnMessage
	out, err := chain.OnMessage(sess, []byte("data"))
	if err != nil {
		t.Fatalf("empty chain OnMessage: %v", err)
	}
	if string(out) != "data" {
		t.Fatalf("expected data to pass through, got %q", out)
	}

	// OnClose (should not panic)
	chain.OnClose(sess)
}
