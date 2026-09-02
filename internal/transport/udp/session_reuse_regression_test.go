package udp

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
)

// TestUDPClosedSessionIsRecreatedForSamePeer is a regression test for the
// "closed session reuse" defect: after a plugin or error path Closed a plain
// UDP session (session.Close is a state transition only, the shared socket is
// not closed), a datagram from the same peer must reap the dead session and
// create a fresh, active one instead of silently reusing the closed session.
func TestUDPClosedSessionIsRecreatedForSamePeer(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gateway.Stop(ctx) }()

	conn, err := net.Dial("udp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// First datagram creates the session and is echoed.
	if _, err := conn.Write([]byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read first echo: %v", err)
	}
	if string(buf[:n]) != "one" {
		t.Fatalf("first echo = %q, want %q", buf[:n], "one")
	}

	var old *session
	server.sessions.Range(func(_, v any) bool {
		old = v.(*session)
		return false
	})
	if old == nil {
		t.Fatal("no session found after first datagram")
	}
	if old.State() != core.StateActive {
		t.Fatalf("session state = %v, want active", old.State())
	}
	oldID := old.ID()
	if err := old.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Second datagram from the same peer: the closed session must be reaped
	// and a brand-new active session created and used for the echo.
	if _, err := conn.Write([]byte("two")); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	n, err = conn.Read(buf)
	if err != nil {
		t.Fatalf("read second echo: %v", err)
	}
	if string(buf[:n]) != "two" {
		t.Fatalf("second echo = %q, want %q", buf[:n], "two")
	}

	if count := server.SessionCount(); count != 1 {
		t.Fatalf("SessionCount = %d, want 1", count)
	}
	var fresh *session
	server.sessions.Range(func(_, v any) bool {
		fresh = v.(*session)
		return false
	})
	if fresh == nil {
		t.Fatal("no session found after second datagram")
	}
	if fresh.ID() == oldID {
		t.Fatalf("second datagram reused closed session id %d", oldID)
	}
	if fresh.State() != core.StateActive {
		t.Fatalf("fresh session state = %v, want active", fresh.State())
	}
}
