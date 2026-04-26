package websocket

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/yourname/shark-socket/internal/errs"
	"github.com/yourname/shark-socket/internal/types"
)

// setupWSPair creates a full-duplex WebSocket pair using httptest.
// Returns the server-side WSSession, the client-side *websocket.Conn, and cleanup.
func setupWSPair(t *testing.T) (*WSSession, *websocket.Conn) {
	t.Helper()

	var serverSess *WSSession

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("upgrade error: %v", err)
			return
		}
		serverSess = NewWSSession(1, conn)
	})

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/"
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("client dial: %v", err)
	}

	// Give server a moment to complete upgrade
	time.Sleep(50 * time.Millisecond)

	t.Cleanup(func() {
		if serverSess != nil {
			serverSess.Close()
		}
		clientConn.Close()
	})

	return serverSess, clientConn
}

func TestNewWSSession(t *testing.T) {
	sess, _ := setupWSPair(t)

	if sess.ID() != 1 {
		t.Errorf("ID() = %d, want 1", sess.ID())
	}
	if sess.Protocol() != types.WebSocket {
		t.Errorf("Protocol() = %v, want WebSocket", sess.Protocol())
	}
	if !sess.IsAlive() {
		t.Error("new session should be alive")
	}
	if sess.State() != types.Active {
		t.Errorf("State() = %v, want Active", sess.State())
	}
}

func TestWSSession_SendBinary(t *testing.T) {
	sess, clientConn := setupWSPair(t)

	payload := []byte("binary data")
	if err := sess.Send(payload); err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	msgType, data, err := clientConn.ReadMessage()
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if msgType != websocket.BinaryMessage {
		t.Errorf("message type = %d, want BinaryMessage", msgType)
	}
	if string(data) != string(payload) {
		t.Errorf("received %q, want %q", data, payload)
	}
}

func TestWSSession_SendText(t *testing.T) {
	sess, clientConn := setupWSPair(t)

	text := []byte("hello text")
	if err := sess.SendText(text); err != nil {
		t.Fatalf("SendText() error: %v", err)
	}

	msgType, data, err := clientConn.ReadMessage()
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if msgType != websocket.TextMessage {
		t.Errorf("message type = %d, want TextMessage", msgType)
	}
	if string(data) != string(text) {
		t.Errorf("received %q, want %q", data, text)
	}
}

func TestWSSession_SendPing(t *testing.T) {
	sess, clientConn := setupWSPair(t)

	if err := sess.sendPing(); err != nil {
		t.Fatalf("sendPing() error: %v", err)
	}

	// gorilla/websocket auto-responds to pings with pongs internally.
	// We verify the sendPing succeeded by checking the session is still alive.
	// A write error would have been returned above.
	if !sess.IsAlive() {
		t.Error("session should still be alive after sendPing()")
	}

	// Verify the session's Conn still works for data after a ping
	if err := sess.Send([]byte("after-ping")); err != nil {
		t.Fatalf("Send() after sendPing() error: %v", err)
	}
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := clientConn.ReadMessage()
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if string(data) != "after-ping" {
		t.Errorf("received %q, want %q", data, "after-ping")
	}
}

func TestWSSession_Close(t *testing.T) {
	sess, clientConn := setupWSPair(t)

	if err := sess.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	if sess.IsAlive() {
		t.Error("session should not be alive after Close()")
	}
	if sess.State() != types.Closed {
		t.Errorf("State() = %v, want Closed", sess.State())
	}

	// Client should receive a close frame
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err := clientConn.ReadMessage()
	if err == nil {
		t.Error("expected error after server closed connection")
	}
}

func TestWSSession_CloseIdempotent(t *testing.T) {
	sess, _ := setupWSPair(t)

	// Call Close multiple times — only the first should perform cleanup
	for i := 0; i < 5; i++ {
		if err := sess.Close(); err != nil {
			t.Logf("Close() call %d: %v", i, err)
		}
	}
	if sess.State() != types.Closed {
		t.Errorf("State() = %v, want Closed after multiple Close() calls", sess.State())
	}
}

func TestWSSession_SendAfterClose(t *testing.T) {
	sess, _ := setupWSPair(t)

	sess.Close()

	err := sess.Send([]byte("should fail"))
	if err == nil {
		t.Error("Send() after Close() should return error")
	}
	if err != errs.ErrSessionClosed {
		t.Errorf("Send() error = %v, want ErrSessionClosed", err)
	}
}

func TestWSSession_SendTyped(t *testing.T) {
	sess, clientConn := setupWSPair(t)

	payload := []byte("typed ws message")
	if err := sess.SendTyped(payload); err != nil {
		t.Fatalf("SendTyped() error: %v", err)
	}

	_, data, err := clientConn.ReadMessage()
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if string(data) != string(payload) {
		t.Errorf("received %q, want %q", data, payload)
	}
}

func TestWSSession_Metadata(t *testing.T) {
	sess, _ := setupWSPair(t)

	sess.SetMeta("user", "alice")
	val, ok := sess.GetMeta("user")
	if !ok {
		t.Error("GetMeta() key not found")
	}
	if val != "alice" {
		t.Errorf("GetMeta() = %v, want %q", val, "alice")
	}

	sess.DelMeta("user")
	_, ok = sess.GetMeta("user")
	if ok {
		t.Error("GetMeta() should return false after DelMeta()")
	}
}

func TestWSSession_AddrInfo(t *testing.T) {
	sess, _ := setupWSPair(t)

	if sess.RemoteAddr() == nil {
		t.Error("RemoteAddr() should not be nil")
	}
	if sess.LocalAddr() == nil {
		t.Error("LocalAddr() should not be nil")
	}
}

func TestWSSession_Conn(t *testing.T) {
	sess, _ := setupWSPair(t)

	conn := sess.Conn()
	if conn == nil {
		t.Error("Conn() should not be nil")
	}

	// Verify Conn returns the real address
	addr := conn.RemoteAddr()
	if addr == nil {
		t.Error("underlying conn RemoteAddr() should not be nil")
	}

	// RemoteAddr of session should match conn's RemoteAddr
	if sess.RemoteAddr().String() != addr.String() {
		t.Errorf("session RemoteAddr() = %q, conn RemoteAddr() = %q", sess.RemoteAddr(), addr)
	}
}

// TestWSSession_RemoteAddrUsesLoopback verifies the remote address is on loopback.
func TestWSSession_RemoteAddrUsesLoopback(t *testing.T) {
	sess, _ := setupWSPair(t)

	host, _, err := net.SplitHostPort(sess.RemoteAddr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	ip := net.ParseIP(host)
	if !ip.IsLoopback() {
		t.Errorf("remote host %q is not loopback", host)
	}
}
