package coap

import (
	"context"
	"crypto/tls"
	"net"
	"testing"
	"time"

	"github.com/pion/dtls/v3"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
)

// dtlsRequest exchanges one CON POST over the given (DTLS) conn and returns
// the ACK payload.
func dtlsRequest(t *testing.T, conn net.Conn, msgID uint16, payload []byte) []byte {
	t.Helper()
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

// TestCoAPDTLSGetCertificateBridge is a regression test for the DTLS
// certificate-mapping gap: the application configuration path serves
// certificates through tls.Config.GetCertificate (Certificates empty, certs
// hot-reloaded via tlsutil.CertCache), but the DTLS mapping previously did
// not carry GetCertificate over, so the DTLS server had no certificate at all
// and no handshake could complete.
func TestCoAPDTLSGetCertificateBridge(t *testing.T) {
	cert, _ := generateTestCert(t)
	// Mimic loadServerTLSConfig: Certificates empty, GetCertificate only.
	serverTLSCfg := &tls.Config{
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return &cert, nil
		},
	}
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithDTLS(serverTLSCfg),
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

	addr, err := net.ResolveUDPAddr("udp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dtls.Dial("udp", addr, &dtls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("DTLS dial failed (GetCertificate was not bridged?): %v", err)
	}
	defer conn.Close()

	if got := dtlsRequest(t, conn, 1, []byte("one")); string(got) != "one" {
		t.Fatalf("first echo = %q, want %q", got, "one")
	}
	if got := dtlsRequest(t, conn, 2, []byte("two")); string(got) != "two" {
		t.Fatalf("second echo = %q, want %q", got, "two")
	}
}
