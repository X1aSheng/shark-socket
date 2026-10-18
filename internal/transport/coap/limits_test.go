package coap

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/runtime"
)

func startLimitedCoAPServer(t *testing.T, opts ...Option) *Server {
	t.Helper()
	server := NewServer(opts...)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopGateway(t, gateway) })
	return server
}

// sendCoAPFrom emits a NON POST from a chosen loopback address, so a test can
// act as several distinct peers.
func sendCoAPFrom(t *testing.T, ip, serverAddr string, messageID uint16) {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(ip)})
	if err != nil {
		t.Fatalf("bind %s: %v", ip, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	raddr, err := net.ResolveUDPAddr("udp", serverAddr)
	if err != nil {
		t.Fatal(err)
	}
	// A bare 4-byte CoAP header is enough to make the server resolve a session
	// for the peer: NON, POST, empty token, empty payload.
	msg := []byte{0x50, byte(CodePost), 0, 0}
	binary.BigEndian.PutUint16(msg[2:], messageID)
	if _, err := conn.WriteToUDP(msg, raddr); err != nil {
		t.Fatal(err)
	}
}

func waitForCoAP(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestCoAPMaxSessionsPerPeer is A1 on the CoAP transport, which created
// sessions from any source address with no ceiling at all.
func TestCoAPMaxSessionsPerPeer(t *testing.T) {
	server := startLimitedCoAPServer(t, WithAddr("127.0.0.1:0"),
		WithMaxSessions(10), WithMaxSessionsPerPeer(1))
	addr := server.Addr().String()

	sendCoAPFrom(t, "127.0.0.2", addr, 1)
	waitForCoAP(t, "the first peer's session", func() bool { return server.SessionCount() == 1 })

	// A second socket from the same address is the same peer.
	sendCoAPFrom(t, "127.0.0.2", addr, 2)
	time.Sleep(50 * time.Millisecond)
	if got := server.SessionCount(); got != 1 {
		t.Fatalf("SessionCount = %d after a second socket from one peer, want 1", got)
	}

	// A distinct address is still admitted.
	sendCoAPFrom(t, "127.0.0.3", addr, 3)
	waitForCoAP(t, "the second peer's session", func() bool { return server.SessionCount() == 2 })
}

// TestCoAPMaxSessionsDrains proves the quota is returned when a session is
// reclaimed, so a flood cannot permanently shrink the table.
func TestCoAPMaxSessionsDrains(t *testing.T) {
	server := startLimitedCoAPServer(t, WithAddr("127.0.0.1:0"),
		WithMaxSessions(1),
		WithSessionTTL(80*time.Millisecond),
		WithSweepInterval(10*time.Millisecond))
	addr := server.Addr().String()

	sendCoAPFrom(t, "127.0.0.2", addr, 1)
	waitForCoAP(t, "the first session", func() bool { return server.SessionCount() == 1 })

	// Table full: this peer is dropped and never gets a session.
	sendCoAPFrom(t, "127.0.0.3", addr, 2)
	time.Sleep(50 * time.Millisecond)
	if got := server.SessionCount(); got != 1 {
		t.Fatalf("SessionCount = %d while the table should be full, want 1", got)
	}

	waitForCoAP(t, "the table to drain", func() bool { return server.SessionCount() == 0 })
	sendCoAPFrom(t, "127.0.0.3", addr, 3)
	waitForCoAP(t, "the previously refused peer to be admitted", func() bool { return server.SessionCount() == 1 })
}
