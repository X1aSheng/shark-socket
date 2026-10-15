package grpcweb

import (
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestWebSocketSessionSendHonoursWriteTimeout verifies that a WebSocket
// session bounds each write with WriteTimeout. A peer that stops reading must
// not be able to block Send forever: Send holds writeMu, and Close needs the
// same mutex, so an unbounded write would wedge CloseSessions and the whole
// gateway shutdown.
func TestWebSocketSessionSendHonoursWriteTimeout(t *testing.T) {
	server, _ := startTestServer(t, nil,
		WithWebSocketMode("/grpc/ws"),
		WithCheckOrigin(func(*http.Request) bool { return true }),
		WithWriteTimeout(200*time.Millisecond),
	)

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+server.Addr().String()+"/grpc/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Deliberately never read from conn: the server's send buffer fills up and
	// the write deadline must eventually fire.

	var sess *webSocketSession
	deadline := time.Now().Add(2 * time.Second)
	for sess == nil {
		server.sessions.Range(func(_, v any) bool {
			sess = v.(*webSocketSession)
			return false
		})
		if sess == nil {
			if time.Now().After(deadline) {
				t.Fatal("websocket session was never registered")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	if sess.writeTimeout != 200*time.Millisecond {
		t.Fatalf("session writeTimeout = %v, want 200ms", sess.writeTimeout)
	}

	big := make([]byte, 256*1024)
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 2000; i++ {
			if err := sess.Send(big); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Send never hit the write deadline against a non-reading peer")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Send blocked past the configured write timeout")
	}

	// Close must complete promptly even though a write just timed out.
	closed := make(chan struct{})
	go func() {
		_ = sess.Close(t.Context())
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked after a timed-out write")
	}
}
