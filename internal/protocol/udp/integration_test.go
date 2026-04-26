package udp

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/types"
)

// TestIntegration_UDP_Echo starts a UDP server on :0, sends a packet,
// receives the echoed response, and verifies the payload.
func TestIntegration_UDP_Echo(t *testing.T) {
	// Echo handler: bounce back whatever arrives.
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return sess.Send(msg.Payload)
	}

	srv := NewServer(handler, WithAddr("127.0.0.1", 0))

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		srv.Stop(stopCtx)
	}()

	// Resolve the server address. Since we used port 0, the OS assigns a port.
	// The UDP server stores the connection, so we read the actual address from it.
	srvAddr := srv.conn.LocalAddr().(*net.UDPAddr)
	t.Logf("UDP server listening on %s", srvAddr)

	// Create a UDP client.
	clientConn, err := net.DialUDP("udp", nil, srvAddr)
	if err != nil {
		t.Fatalf("dial udp: %v", err)
	}
	defer clientConn.Close()

	// Send a packet.
	payload := []byte("hello udp shark-socket")
	if _, err := clientConn.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read the echoed response with a timeout.
	clientConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 4096)
	n, err := clientConn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	got := buf[:n]
	if string(got) != string(payload) {
		t.Fatalf("echo mismatch: got %q, want %q", got, payload)
	}
	t.Logf("UDP echo verified: %q", got)

	// Verify protocol type.
	if proto := srv.Protocol(); proto != types.UDP {
		t.Fatalf("Protocol() = %v, want %v", proto, types.UDP)
	}
}
