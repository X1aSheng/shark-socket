package websocket

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/types"
	"github.com/gorilla/websocket"
)

func waitForTCPServer(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 10*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("TCP server at %s not ready after %v", addr, timeout)
}

// TestIntegration_WS_Echo starts a WebSocket server on :0, connects with a
// gorilla/websocket client, sends a binary message, receives the echo, and verifies.
func TestIntegration_WS_Echo(t *testing.T) {
	// Echo handler: bounce back whatever arrives.
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return sess.Send(msg.Payload)
	}

	// Pick a free port by listening temporarily.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	srv := NewServer(handler,
		WithAddr("127.0.0.1", port),
		WithPath("/ws"),
		WithPingPong(60*time.Second, 30*time.Second),
		WithAllowedOrigins("*"),
	)

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		srv.Stop(stopCtx)
	}()

	// Allow server goroutine to start.
	waitForTCPServer(t, fmt.Sprintf("127.0.0.1:%d", port), 3*time.Second)

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)
	t.Logf("Connecting to %s", wsURL)

	// Connect with gorilla/websocket client.
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer wsConn.Close()

	// Send a binary message.
	payload := []byte("hello ws shark-socket")
	if err := wsConn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		t.Fatalf("write message: %v", err)
	}

	// Read the echoed response.
	wsConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	msgType, got, err := wsConn.ReadMessage()
	if err != nil {
		t.Fatalf("read message: %v", err)
	}

	if msgType != websocket.BinaryMessage {
		t.Fatalf("message type = %d, want BinaryMessage", msgType)
	}

	if string(got) != string(payload) {
		t.Fatalf("echo mismatch: got %q, want %q", got, payload)
	}
	t.Logf("WS echo verified: %q", got)

	// Verify protocol type.
	if proto := srv.Protocol(); proto != types.WebSocket {
		t.Fatalf("Protocol() = %v, want %v", proto, types.WebSocket)
	}

	// Verify manager exists.
	if mgr := srv.Manager(); mgr == nil {
		t.Fatal("Manager() returned nil")
	}
}

// TestIntegration_WS_TextEcho tests sending and receiving text messages.
func TestIntegration_WS_TextEcho(t *testing.T) {
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		// Send uses BinaryMessage; the handler just echoes bytes.
		return sess.Send(msg.Payload)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	srv := NewServer(handler,
		WithAddr("127.0.0.1", port),
		WithPath("/echo"),
		WithPingPong(60*time.Second, 30*time.Second),
		WithAllowedOrigins("*"),
	)

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		srv.Stop(stopCtx)
	}()

	waitForTCPServer(t, fmt.Sprintf("127.0.0.1:%d", port), 3*time.Second)

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/echo", port)
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer wsConn.Close()

	// Send a text message.
	text := []byte("text message test")
	if err := wsConn.WriteMessage(websocket.TextMessage, text); err != nil {
		t.Fatalf("write text: %v", err)
	}

	wsConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, got, err := wsConn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if string(got) != string(text) {
		t.Fatalf("echo mismatch: got %q, want %q", got, text)
	}
	t.Logf("WS text echo verified: %q", got)
}

// TestIntegration_WS_MultipleMessages sends multiple messages on a single
// WebSocket connection and verifies each echo sequentially.
func TestIntegration_WS_MultipleMessages(t *testing.T) {
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return sess.Send(msg.Payload)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	srv := NewServer(handler,
		WithAddr("127.0.0.1", port),
		WithPath("/ws"),
		WithPingPong(60*time.Second, 30*time.Second),
		WithAllowedOrigins("*"),
	)

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		srv.Stop(stopCtx)
	}()

	waitForTCPServer(t, fmt.Sprintf("127.0.0.1:%d", port), 3*time.Second)

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer wsConn.Close()
	wsConn.SetReadDeadline(time.Now().Add(10 * time.Second))

	messages := []string{"msg-one", "msg-two", "msg-three"}
	for _, msg := range messages {
		if err := wsConn.WriteMessage(websocket.BinaryMessage, []byte(msg)); err != nil {
			t.Fatalf("write %q: %v", msg, err)
		}
		_, got, err := wsConn.ReadMessage()
		if err != nil {
			t.Fatalf("read echo for %q: %v", msg, err)
		}
		if string(got) != msg {
			t.Fatalf("echo mismatch: got %q, want %q", got, msg)
		}
	}
	t.Log("WS multiple message echo verified")
}
