package quic

import (
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/session"
	"github.com/X1aSheng/shark-socket/internal/types"
)

// mockAddr implements net.Addr for testing
type mockAddr struct {
	network string
	str     string
}

func (m *mockAddr) Network() string { return m.network }
func (m *mockAddr) String() string  { return m.str }

var _ net.Addr = (*mockAddr)(nil)

func TestNewSession(t *testing.T) {
	mockRemote := &mockAddr{network: "udp", str: "192.168.1.1:54321"}
	mockLocal := &mockAddr{network: "udp", str: "127.0.0.1:18600"}
	sess := &Session{
		BaseSession: session.NewBase(123, types.QUIC, mockRemote, mockLocal),
		writeQueue:  make(chan []byte, 16),
		draining:    make(chan struct{}),
		drained:     make(chan struct{}),
	}
	sess.SetState(types.Active)

	if sess.ID() != 123 {
		t.Errorf("expected session ID 123, got %d", sess.ID())
	}
	if sess.Protocol() != types.QUIC {
		t.Errorf("expected QUIC protocol, got %v", sess.Protocol())
	}
	if sess.RemoteAddr().String() != "192.168.1.1:54321" {
		t.Errorf("expected remote addr 192.168.1.1:54321, got %s", sess.RemoteAddr().String())
	}
}

func TestSession_Send(t *testing.T) {
	mockRemote := &mockAddr{network: "udp", str: "10.0.0.1:1234"}
	mockLocal := &mockAddr{network: "udp", str: "127.0.0.1:18600"}
	sess := &Session{
		BaseSession: session.NewBase(1, types.QUIC, mockRemote, mockLocal),
		writeQueue:  make(chan []byte, 2),
		draining:    make(chan struct{}),
		drained:     make(chan struct{}),
	}
	sess.SetState(types.Active)

	data := []byte("hello")
	if err := sess.Send(data); err != nil {
		t.Errorf("unexpected send error: %v", err)
	}

	select {
	case msg := <-sess.writeQueue:
		if string(msg) != "hello" {
			t.Errorf("expected 'hello', got '%s'", string(msg))
		}
	default:
		t.Error("expected message in queue")
	}

	// Fill the queue
	sess.writeQueue <- []byte("msg1")
	sess.writeQueue <- []byte("msg2")

	// Test queue full
	err := sess.Send([]byte("overflow"))
	if err == nil {
		t.Error("expected error when queue is full")
	}
}

func TestSession_SendTyped(t *testing.T) {
	mockRemote := &mockAddr{network: "udp", str: "10.0.0.1:1234"}
	mockLocal := &mockAddr{network: "udp", str: "127.0.0.1:18600"}
	sess := &Session{
		BaseSession: session.NewBase(1, types.QUIC, mockRemote, mockLocal),
		writeQueue:  make(chan []byte, 1),
		draining:    make(chan struct{}),
		drained:     make(chan struct{}),
	}
	sess.SetState(types.Active)

	data := []byte("typed data")
	if err := sess.SendTyped(data); err != nil {
		t.Errorf("unexpected SendTyped error: %v", err)
	}
}

func TestSession_Send_Closed(t *testing.T) {
	mockRemote := &mockAddr{network: "udp", str: "10.0.0.1:1234"}
	mockLocal := &mockAddr{network: "udp", str: "127.0.0.1:18600"}
	sess := &Session{
		BaseSession: session.NewBase(1, types.QUIC, mockRemote, mockLocal),
		writeQueue:  make(chan []byte, 1),
		draining:    make(chan struct{}),
		drained:     make(chan struct{}),
	}
	sess.SetState(types.Closed)

	err := sess.Send([]byte("data"))
	if err == nil {
		t.Error("expected error when sending to closed session")
	}
}

func TestSession_Protocol(t *testing.T) {
	mockRemote := &mockAddr{network: "udp", str: "10.0.0.1:1234"}
	mockLocal := &mockAddr{network: "udp", str: "127.0.0.1:18600"}
	sess := &Session{
		BaseSession: session.NewBase(1, types.QUIC, mockRemote, mockLocal),
	}

	if sess.Protocol() != types.QUIC {
		t.Errorf("expected QUIC protocol, got %v", sess.Protocol())
	}
}

func TestSession_IsAlive(t *testing.T) {
	mockRemote := &mockAddr{network: "udp", str: "10.0.0.1:1234"}
	mockLocal := &mockAddr{network: "udp", str: "127.0.0.1:18600"}
	sess := &Session{
		BaseSession: session.NewBase(1, types.QUIC, mockRemote, mockLocal),
	}

	sess.SetState(types.Active)
	if !sess.IsAlive() {
		t.Error("expected session to be alive in Active state")
	}

	sess.SetState(types.Closed)
	if sess.IsAlive() {
		t.Error("expected session to not be alive in Closed state")
	}
}

func TestSession_TouchActive(t *testing.T) {
	mockRemote := &mockAddr{network: "udp", str: "10.0.0.1:1234"}
	mockLocal := &mockAddr{network: "udp", str: "127.0.0.1:18600"}
	sess := &Session{
		BaseSession: session.NewBase(1, types.QUIC, mockRemote, mockLocal),
	}
	sess.SetState(types.Active)

	before := sess.LastActiveAt()
	time.Sleep(10 * time.Millisecond)
	sess.TouchActive()
	after := sess.LastActiveAt()

	if !after.After(before) {
		t.Error("expected LastActiveAt to be updated")
	}
}

func TestSession_SetGetMeta(t *testing.T) {
	mockRemote := &mockAddr{network: "udp", str: "10.0.0.1:1234"}
	mockLocal := &mockAddr{network: "udp", str: "127.0.0.1:18600"}
	sess := &Session{
		BaseSession: session.NewBase(1, types.QUIC, mockRemote, mockLocal),
	}

	sess.SetMeta("key", "value")
	val, ok := sess.GetMeta("key")
	if !ok {
		t.Error("expected to find key")
	}
	if val != "value" {
		t.Errorf("expected 'value', got %v", val)
	}

	_, ok = sess.GetMeta("nonexistent")
	if ok {
		t.Error("expected not to find nonexistent key")
	}

	sess.DelMeta("key")
	_, ok = sess.GetMeta("key")
	if ok {
		t.Error("expected key to be deleted")
	}
}

func TestSession_ConcurrentAccess(t *testing.T) {
	mockRemote := &mockAddr{network: "udp", str: "10.0.0.1:1234"}
	mockLocal := &mockAddr{network: "udp", str: "127.0.0.1:18600"}
	sess := &Session{
		BaseSession: session.NewBase(1, types.QUIC, mockRemote, mockLocal),
		writeQueue:  make(chan []byte, 100),
		draining:    make(chan struct{}),
		drained:     make(chan struct{}),
	}
	sess.SetState(types.Active)

	done := make(chan bool)

	// Concurrent sends
	for i := 0; i < 10; i++ {
		go func(n int) {
			_ = sess.Send([]byte{byte(n)})
			done <- true
		}(i)
	}

	// Concurrent metadata operations
	for i := 0; i < 10; i++ {
		go func(n int) {
			sess.SetMeta(string(rune('a'+n)), n)
			_, _ = sess.GetMeta(string(rune('a' + n)))
			done <- true
		}(i)
	}

	// Drain queue to unblock sends
	go func() {
		for i := 0; i < 10; i++ {
			<-sess.writeQueue
		}
	}()

	for i := 0; i < 20; i++ {
		<-done
	}
}

func TestSession_MemoryLimits(t *testing.T) {
	mockRemote := &mockAddr{network: "udp", str: "10.0.0.1:1234"}
	mockLocal := &mockAddr{network: "udp", str: "127.0.0.1:18600"}
	sess := &Session{
		BaseSession: session.NewBase(1, types.QUIC, mockRemote, mockLocal),
	}

	if sess.MaxMessageSize() != session.DefaultMaxMessageSize {
		t.Errorf("expected default max message size %d, got %d",
			session.DefaultMaxMessageSize, sess.MaxMessageSize())
	}

	sess.SetMaxMessageSize(1024)
	if sess.MaxMessageSize() != 1024 {
		t.Errorf("expected max message size 1024, got %d", sess.MaxMessageSize())
	}

	if !sess.CheckMessageSize(512) {
		t.Error("expected 512 bytes to be within limit")
	}
	if sess.CheckMessageSize(2048) {
		t.Error("expected 2048 bytes to exceed limit")
	}

	sess.SetMaxMessageSize(0)
	if !sess.CheckMessageSize(1000000) {
		t.Error("expected large message to be within limit when unlimited")
	}

	sess.AddMemUsage(100)
	if sess.MemUsed() != 100 {
		t.Errorf("expected mem used 100, got %d", sess.MemUsed())
	}
	sess.ReleaseMemUsage(50)
	if sess.MemUsed() != 50 {
		t.Errorf("expected mem used 50 after release, got %d", sess.MemUsed())
	}
}

func TestSession_StateTransitions(t *testing.T) {
	mockRemote := &mockAddr{network: "udp", str: "10.0.0.1:1234"}
	mockLocal := &mockAddr{network: "udp", str: "127.0.0.1:18600"}
	sess := &Session{
		BaseSession: session.NewBase(1, types.QUIC, mockRemote, mockLocal),
	}

	if sess.State() != types.Connecting {
		t.Errorf("expected initial state Connecting, got %v", sess.State())
	}

	if !sess.SetState(types.Active) {
		t.Error("expected successful transition to Active")
	}
	if sess.State() != types.Active {
		t.Errorf("expected state Active, got %v", sess.State())
	}

	if sess.SetState(types.Active) {
		t.Error("expected no transition when already in Active")
	}

	if !sess.SetState(types.Closing) {
		t.Error("expected successful transition to Closing")
	}

	if !sess.SetState(types.Closed) {
		t.Error("expected successful transition to Closed")
	}
}

func TestSession_Context(t *testing.T) {
	mockRemote := &mockAddr{network: "udp", str: "10.0.0.1:1234"}
	mockLocal := &mockAddr{network: "udp", str: "127.0.0.1:18600"}
	sess := &Session{
		BaseSession: session.NewBase(1, types.QUIC, mockRemote, mockLocal),
	}

	ctx := sess.Context()
	if ctx == nil {
		t.Error("expected non-nil context")
	}

	// Context should be cancelled after DoClose
	sess.DoClose()
	select {
	case <-ctx.Done():
		// expected
	default:
		t.Error("expected context to be cancelled after DoClose")
	}
}

func TestSession_CreatedAt(t *testing.T) {
	mockRemote := &mockAddr{network: "udp", str: "10.0.0.1:1234"}
	mockLocal := &mockAddr{network: "udp", str: "127.0.0.1:18600"}
	before := time.Now()
	sess := &Session{
		BaseSession: session.NewBase(1, types.QUIC, mockRemote, mockLocal),
	}
	after := time.Now()

	created := sess.CreatedAt()
	if created.Before(before) || created.After(after) {
		t.Error("CreatedAt time out of expected range")
	}
}

var _ types.RawSession = (*Session)(nil)
var _ net.Addr = (*mockAddr)(nil)
