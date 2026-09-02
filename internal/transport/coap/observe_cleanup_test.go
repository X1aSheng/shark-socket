package coap

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/runtime"
)

// TestCoAPRSTCancelsObserver verifies RFC 7641 §4.4: a peer that rejects a
// CON notification with RST cancels the observe relation identified by the
// notification token.
func TestCoAPRSTCancelsObserver(t *testing.T) {
	server := NewServer(WithAddr("127.0.0.1:0"))
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
	remote := conn.LocalAddr().String()

	token := []byte{0xAA}
	server.observers.Register("/sensor/humidity", remote, token)

	// Reject the CON notification with an RST carrying the same token.
	rst := Message{Type: TypeRST, Code: CodeEmpty, MessageID: 42, Token: token}
	data, err := rst.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(data); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if len(server.observers.Notify("/sensor/humidity")) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("observer still registered after RST")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestCoAPSweepKeepsObserverSessions verifies that the idle sweep does not
// drop sessions that still hold observe relations (a client may legitimately
// be silent between notifications for longer than SessionTTL).
func TestCoAPSweepKeepsObserverSessions(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithSessionTTL(50*time.Millisecond),
		WithSweepInterval(20*time.Millisecond),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

	// A plain request creates the session for this remote.
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		var found *session
		server.sessions.Range(func(_, v any) bool {
			found = v.(*session)
			return false
		})
		if found != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("session was never created")
		}
		time.Sleep(10 * time.Millisecond)
	}

	remote := conn.LocalAddr().String()
	server.observers.Register("/sensor/temp", remote, []byte{1})

	// Let several sweep cycles run: the session (and its observer) must
	// survive despite no inbound traffic for longer than SessionTTL.
	time.Sleep(150 * time.Millisecond)
	if count := server.SessionCount(); count != 1 {
		t.Fatalf("session with observer was swept: count = %d, want 1", count)
	}
	if len(server.observers.Notify("/sensor/temp")) != 1 {
		t.Fatal("observer was removed with its session")
	}

	// Once the relation ends, the idle session is reclaimed as before.
	server.observers.RemoveBySession(remote)
	deadline = time.Now().Add(2 * time.Second)
	for server.SessionCount() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("session without observers was not swept")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestCoAPRetransmitExhaustionRemovesObserver verifies that a CON
// notification that is never acknowledged (attempts exhausted) cancels the
// observe relation instead of leaving a dead subscription behind.
func TestCoAPRetransmitExhaustionRemovesObserver(t *testing.T) {
	server := NewServer(WithAddr("127.0.0.1:0"))
	remote := "127.0.0.1:19999"
	token := []byte{0x42}
	server.observers.Register("/sensor/temp", remote, token)

	// Inject a pending notification that has already exhausted its
	// retransmission attempts (as the retransmit loop would leave it for a
	// dead peer).
	key := notifyKey{remote: remote, msgID: 7}
	server.retransmitMu.Lock()
	server.pendingNotifies[key] = pendingNotify{data: []byte("x"), token: append([]byte(nil), token...), attempts: maxRetransmit}
	server.retransmitMu.Unlock()

	server.retransmitDue()

	if len(server.observers.Notify("/sensor/temp")) != 0 {
		t.Fatal("observer still registered after retransmission exhaustion")
	}
	server.retransmitMu.Lock()
	_, stillPending := server.pendingNotifies[key]
	server.retransmitMu.Unlock()
	if stillPending {
		t.Fatal("pending notification entry still present after exhaustion")
	}
}
