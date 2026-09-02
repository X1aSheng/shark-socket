package coap

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
)

// TestCoAPClosedSessionRecreatedForSamePeer is a regression test for the
// "closed session reuse" defect: after a plugin or error path Closed a plain
// CoAP UDP session (session.Close is a state transition only, the shared
// socket is not closed), the next request from the same peer must reap the
// dead session (including its observers) and be served by a fresh, active
// session instead of silently reusing the closed one.
func TestCoAPClosedSessionRecreatedForSamePeer(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		// A responder echo: the CON ACK carries the payload, so the client
		// receives exactly one well-formed CoAP response per request.
		WithResponder(func(_ core.Session, msg core.Message) ([]byte, error) {
			return msg.Payload, nil
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

	request := func(msgID uint16, payload []byte) []byte {
		req := Message{Type: TypeCON, Code: CodePost, MessageID: msgID, Token: []byte{0x01}, Payload: payload}
		data, err := req.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Write(data); err != nil {
			t.Fatal(err)
		}
		if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 1500)
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatalf("read response for msg %d: %v", msgID, err)
		}
		ack, err := Parse(buf[:n])
		if err != nil {
			t.Fatalf("parse response for msg %d: %v", msgID, err)
		}
		if ack.Type != TypeACK || ack.Code != CodeCreated {
			t.Fatalf("response type=%d code=%d, want ACK/Created", ack.Type, ack.Code)
		}
		return ack.Payload
	}

	if got := request(1, []byte("one")); string(got) != "one" {
		t.Fatalf("first echo = %q, want %q", got, "one")
	}

	var old *session
	server.sessions.Range(func(_, v any) bool {
		old = v.(*session)
		return false
	})
	if old == nil {
		t.Fatal("no session found after first request")
	}
	oldID := old.ID()
	if err := old.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := request(2, []byte("two")); string(got) != "two" {
		t.Fatalf("second echo = %q, want %q", got, "two")
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
		t.Fatal("no session found after second request")
	}
	if fresh.ID() == oldID {
		t.Fatalf("second request reused closed session id %d", oldID)
	}
	if fresh.State() != core.StateActive {
		t.Fatalf("fresh session state = %v, want active", fresh.State())
	}
}
