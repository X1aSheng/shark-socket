package udp

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/errs"
	"github.com/X1aSheng/shark-socket/internal/types"
)

// setupUDPConn creates a pair of connected UDP sockets on loopback.
func setupUDPConn(t *testing.T) (*net.UDPConn, *net.UDPConn, *net.UDPAddr) {
	t.Helper()

	// Server side
	serverAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve server addr: %v", err)
	}
	serverConn, err := net.ListenUDP("udp", serverAddr)
	if err != nil {
		t.Fatalf("listen server: %v", err)
	}

	// Client side
	clientAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		serverConn.Close()
		t.Fatalf("resolve client addr: %v", err)
	}
	clientConn, err := net.ListenUDP("udp", clientAddr)
	if err != nil {
		serverConn.Close()
		t.Fatalf("listen client: %v", err)
	}

	t.Cleanup(func() {
		serverConn.Close()
		clientConn.Close()
	})

	return serverConn, clientConn, clientConn.LocalAddr().(*net.UDPAddr)
}

func TestNewUDPSession(t *testing.T) {
	serverConn, _, clientAddr := setupUDPConn(t)

	sess := NewUDPSession(1, serverConn, clientAddr)

	if sess.ID() != 1 {
		t.Errorf("ID() = %d, want 1", sess.ID())
	}
	if sess.Protocol() != types.UDP {
		t.Errorf("Protocol() = %v, want UDP", sess.Protocol())
	}
	if !sess.IsAlive() {
		t.Error("new session should be alive (Active)")
	}
	if sess.State() != types.Active {
		t.Errorf("State() = %v, want Active", sess.State())
	}
	if sess.Addr().String() != clientAddr.String() {
		t.Errorf("Addr() = %v, want %v", sess.Addr(), clientAddr)
	}
}

func TestUDPSession_Send(t *testing.T) {
	serverConn, clientConn, clientAddr := setupUDPConn(t)

	sess := NewUDPSession(42, serverConn, clientAddr)

	payload := []byte("hello shark-socket")
	if err := sess.Send(payload); err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	buf := make([]byte, 1024)
	n, _, err := clientConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if string(buf[:n]) != string(payload) {
		t.Errorf("received %q, want %q", buf[:n], payload)
	}
}

func TestUDPSession_SendAfterClose(t *testing.T) {
	serverConn, _, clientAddr := setupUDPConn(t)

	sess := NewUDPSession(1, serverConn, clientAddr)

	if err := sess.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	if sess.IsAlive() {
		t.Error("session should not be alive after Close()")
	}

	err := sess.Send([]byte("should fail"))
	if err == nil {
		t.Error("Send() after Close() should return error, got nil")
	}
	if err != errs.ErrSessionClosed {
		t.Errorf("Send() error = %v, want ErrSessionClosed", err)
	}
}

func TestUDPSession_CloseTransitionsState(t *testing.T) {
	serverConn, _, clientAddr := setupUDPConn(t)

	sess := NewUDPSession(1, serverConn, clientAddr)

	if sess.State() != types.Active {
		t.Fatalf("initial state = %v, want Active", sess.State())
	}

	if err := sess.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	if sess.State() != types.Closed {
		t.Errorf("state after Close() = %v, want Closed", sess.State())
	}
}

func TestUDPSession_CloseIdempotent(t *testing.T) {
	serverConn, _, clientAddr := setupUDPConn(t)

	sess := NewUDPSession(1, serverConn, clientAddr)

	for i := 0; i < 5; i++ {
		if err := sess.Close(); err != nil {
			t.Errorf("Close() call %d error: %v", i, err)
		}
	}
	if sess.State() != types.Closed {
		t.Errorf("state = %v, want Closed after multiple Close() calls", sess.State())
	}
}

func TestUDPSession_SendConcurrent(t *testing.T) {
	serverConn, clientConn, clientAddr := setupUDPConn(t)

	sess := NewUDPSession(1, serverConn, clientAddr)

	var wg sync.WaitGroup
	const numSenders = 10
	wg.Add(numSenders)

	for i := 0; i < numSenders; i++ {
		go func() {
			defer wg.Done()
			_ = sess.Send([]byte("concurrent"))
		}()
	}
	wg.Wait()

	// Read all messages from client
	received := 0
	buf := make([]byte, 1024)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		n, _, err := clientConn.ReadFromUDP(buf)
		if err != nil {
			break
		}
		if n > 0 {
			received++
		}
	}

	if received != numSenders {
		t.Errorf("received %d messages, want %d", received, numSenders)
	}
}

func TestUDPSession_SendTyped(t *testing.T) {
	serverConn, clientConn, clientAddr := setupUDPConn(t)

	sess := NewUDPSession(1, serverConn, clientAddr)

	payload := []byte("typed message")
	if err := sess.SendTyped(payload); err != nil {
		t.Fatalf("SendTyped() error: %v", err)
	}

	buf := make([]byte, 1024)
	n, _, err := clientConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if string(buf[:n]) != string(payload) {
		t.Errorf("received %q, want %q", buf[:n], payload)
	}
}

func TestUDPSession_Metadata(t *testing.T) {
	serverConn, _, clientAddr := setupUDPConn(t)

	sess := NewUDPSession(1, serverConn, clientAddr)

	sess.SetMeta("key", "value")
	val, ok := sess.GetMeta("key")
	if !ok {
		t.Error("GetMeta() key not found")
	}
	if val != "value" {
		t.Errorf("GetMeta() = %v, want %q", val, "value")
	}

	sess.DelMeta("key")
	_, ok = sess.GetMeta("key")
	if ok {
		t.Error("GetMeta() should return false after DelMeta()")
	}
}
