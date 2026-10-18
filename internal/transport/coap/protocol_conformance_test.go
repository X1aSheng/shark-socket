package coap

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/runtime"
)

// startTestCoAPServer starts a plain-UDP CoAP server behind a gateway and
// returns it together with a connected client socket.
func startTestCoAPServer(t *testing.T, opts ...Option) (*Server, *runtime.Gateway, net.Conn, context.Context) {
	t.Helper()
	server := NewServer(append([]Option{WithAddr("127.0.0.1:0")}, opts...)...)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := gateway.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = gateway.Stop(ctx)
		cancel()
	})
	conn, err := net.Dial("udp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return server, gateway, conn, ctx
}

func readCoAPMessage(t *testing.T, conn net.Conn, timeout time.Duration) (Message, bool) {
	t.Helper()
	buf := make([]byte, 2048)
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatal(err)
	}
	n, err := conn.Read(buf)
	if err != nil {
		return Message{}, false
	}
	msg, perr := Parse(buf[:n])
	if perr != nil {
		t.Fatalf("server sent unparsable datagram: %v (% x)", perr, buf[:n])
	}
	return msg, true
}

// TestCoAPEmptyCONPingGetsRST verifies RFC 7252 section 4.3: a CON message
// with code 0.00 (CoAP ping) must be answered with an RST, not a 2.05 ACK.
func TestCoAPEmptyCONPingGetsRST(t *testing.T) {
	_, _, conn, _ := startTestCoAPServer(t)

	ping := Message{Type: TypeCON, Code: CodeEmpty, MessageID: 0x1234}
	data, err := ping.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(data); err != nil {
		t.Fatal(err)
	}

	reply, ok := readCoAPMessage(t, conn, 2*time.Second)
	if !ok {
		t.Fatal("no reply to CoAP ping")
	}
	if reply.Type != TypeRST {
		t.Fatalf("ping reply type = %d, want RST (%d); full reply = %+v", reply.Type, TypeRST, reply)
	}
	if reply.MessageID != ping.MessageID {
		t.Fatalf("ping reply MID = %d, want %d", reply.MessageID, ping.MessageID)
	}
	if reply.Code != CodeEmpty {
		t.Fatalf("ping reply code = %d, want empty 0.00", reply.Code)
	}
}

// TestCoAPSpecCompliantRSTCancelsObserver verifies RFC 7641 section 4.4 with a
// protocol-conformant RST: RFC 7252 section 4.2 mandates that an RST is an
// Empty message with an empty token (TKL=0). The relation must be cancelled by
// correlating the RST Message ID with the pending CON notification, since the
// RST itself carries no token.
func TestCoAPSpecCompliantRSTCancelsObserver(t *testing.T) {
	server, _, conn, _ := startTestCoAPServer(t)
	remote := conn.LocalAddr().String()
	token := []byte{0xAA}

	server.observers.Register("/sensor/humidity", remote, token)
	if len(server.observers.Notify("/sensor/humidity")) != 1 {
		t.Fatal("precondition: observer not registered")
	}

	// Track a pending CON notification carrying the relation token, as the
	// notify path would. The peer rejects that notification by Message ID.
	const notifyMID = 0x4242
	server.retransmitMu.Lock()
	server.pendingNotifies[notifyKey{remote: remote, msgID: notifyMID}] = pendingNotify{
		data:     []byte("notify"),
		token:    append([]byte(nil), token...),
		attempts: 0,
		timeout:  ackTimeout,
		deadline: time.Now().Add(ackTimeout),
	}
	server.retransmitMu.Unlock()

	// RFC-conformant RST: Empty message, no token.
	rst := Message{Type: TypeRST, Code: CodeEmpty, MessageID: notifyMID}
	data, err := rst.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(data); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for len(server.observers.Notify("/sensor/humidity")) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("observer still registered after a conformant RST")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestCoAPZeroLengthObserveRegisters verifies that an Observe option encoded
// as a zero-length value (the canonical shortest encoding of integer 0, per
// RFC 7252 section 3.2) registers an observe relation.
func TestCoAPZeroLengthObserveRegisters(t *testing.T) {
	server, _, conn, _ := startTestCoAPServer(t)
	remote := conn.LocalAddr().String()

	get := Message{
		Type:      TypeCON,
		Code:      CodeGet,
		MessageID: 0x0101,
		Token:     []byte{0x01},
		Options:   map[uint16][]byte{ObserveOption: {}},
		Payload:   []byte("/sensor/temp"),
	}
	data, err := get.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(data); err != nil {
		t.Fatal(err)
	}
	if _, ok := readCoAPMessage(t, conn, 2*time.Second); !ok {
		t.Fatal("no ACK for observe registration")
	}

	deadline := time.Now().Add(2 * time.Second)
	for len(server.observers.Notify("/sensor/temp")) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("zero-length Observe option did not register a relation for %s", remote)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
