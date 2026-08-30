package coap

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/pion/dtls/v3"
)

type prefixPlugin struct {
	core.BasePlugin
	prefix []byte
}

func (p prefixPlugin) Name() string { return "coap-prefix" }

func (p prefixPlugin) OnMessage(_ core.Session, data []byte) ([]byte, error) {
	out := append([]byte(nil), p.prefix...)
	out = append(out, data...)
	return out, nil
}

func TestGatewayCoAPCONAckAndPlugin(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(sess core.Session, msg core.Message) error {
			sess.SetMeta("payload", string(msg.Payload))
			return nil
		}),
	)
	gateway := runtime.NewGateway(runtime.WithPlugins(prefixPlugin{prefix: []byte("global:")}))
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer stopGateway(t, gateway)

	conn, err := net.Dial("udp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	req := Message{Type: TypeCON, Code: CodePost, MessageID: 7, Token: []byte{1}, Payload: []byte("hello")}
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
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	ack, err := Parse(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if ack.Type != TypeACK || ack.MessageID != req.MessageID || ack.Code != CodeCreated {
		t.Fatalf("ack = %#v", ack)
	}
	if server.SessionCount() != 1 {
		t.Fatalf("session count = %d, want 1", server.SessionCount())
	}
}

func TestCoAPSessionTTL(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithSessionTTL(20*time.Millisecond),
		WithSweepInterval(10*time.Millisecond),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer stopGateway(t, gateway)

	conn, err := net.Dial("udp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	req := Message{Type: TypeNON, Code: CodePost, MessageID: 9, Payload: []byte("touch")}
	data, err := req.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(data); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if server.SessionCount() == 0 && gateway.Runtime().Sessions().Count() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session did not expire: server=%d runtime=%d", server.SessionCount(), gateway.Runtime().Sessions().Count())
}

func TestCoAPDuplicateCONDoesNotRerunHandler(t *testing.T) {
	var handled atomic.Int32
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(core.Session, core.Message) error {
			handled.Add(1)
			return nil
		}),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer stopGateway(t, gateway)

	conn, err := net.Dial("udp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	req := Message{Type: TypeCON, Code: CodePost, MessageID: 11, Token: []byte{7}, Payload: []byte("hello")}
	first := exchangeMessage(t, conn, req)
	if first.Code != CodeCreated {
		t.Fatalf("first ack code = %d, want %d", first.Code, CodeCreated)
	}
	second := exchangeMessage(t, conn, req)
	if second.Code != CodeValid {
		t.Fatalf("second ack code = %d, want %d", second.Code, CodeValid)
	}
	if got := handled.Load(); got != 1 {
		t.Fatalf("handler calls = %d, want 1", got)
	}
}

// TestCoAPNonDoesNotPolluteDedup verifies that a NON message carrying msgID N
// is not recorded in the dedup map, so a later CON reusing msgID N still
// reaches the handler instead of being misclassified as a duplicate
// (RFC 7252 dedup applies to CON messages only).
func TestCoAPNonDoesNotPolluteDedup(t *testing.T) {
	var handled atomic.Int32
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(core.Session, core.Message) error {
			handled.Add(1)
			return nil
		}),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer stopGateway(t, gateway)

	conn, err := net.Dial("udp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// NON POST with msgID 9 reaches the handler but must not be deduped.
	non := Message{Type: TypeNON, Code: CodePost, MessageID: 9, Token: []byte{3}, Payload: []byte("non")}
	nonData, err := non.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(nonData); err != nil {
		t.Fatal(err)
	}

	// A CON reusing msgID 9 must still reach the handler (CodeCreated), not be
	// re-ACKed as a duplicate (CodeValid).
	con := Message{Type: TypeCON, Code: CodePost, MessageID: 9, Token: []byte{4}, Payload: []byte("con")}
	ack := exchangeMessage(t, conn, con)
	if ack.Code != CodeCreated {
		t.Fatalf("CON ack code = %d, want %d (NON polluted the dedup map)", ack.Code, CodeCreated)
	}
	if got := handled.Load(); got != 2 {
		t.Fatalf("handler calls = %d, want 2", got)
	}
}

// TestCoAPNONObserveInitialValue verifies that a NON GET registering an
// observer receives the current value as an initial NON notification (RFC 7641);
// previously only CON registrations got an initial response.
func TestCoAPNONObserveInitialValue(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return nil
		}),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer stopGateway(t, gateway)

	conn, err := net.Dial("udp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// NON GET with Observe register (option value 0) on /sensor/temp.
	req := Message{Type: TypeNON, Code: CodeGet, MessageID: 5, Token: []byte{9},
		Options: map[uint16][]byte{ObserveOption: {0}}, Payload: []byte("/sensor/temp")}
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
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("no initial NON notification: %v", err)
	}
	notify, err := Parse(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if notify.Type != TypeNON || notify.Code != CodeContent {
		t.Fatalf("initial notify = %#v, want NON Content", notify)
	}
	seqBytes, ok := notify.Options[ObserveOption]
	if !ok || len(seqBytes) == 0 {
		t.Fatal("initial notification missing observe option")
	}
	// Observe seq uses variable-length big-endian encoding (encodeObserveSeq).
	var got uint32
	for _, b := range seqBytes {
		got = got<<8 | uint32(b)
	}
	if got != 0 {
		t.Fatalf("initial seq = %d, want 0", got)
	}
}

// TestCoAPNotificationAckClearsPending verifies that an ACK clears the pending
// CON notification so it is not retransmitted.
func TestCoAPNotificationAckClearsPending(t *testing.T) {
	s := NewServer(WithAddr("127.0.0.1:0"))
	s.trackNotify("127.0.0.1:1", 42, []byte("data"))
	if got := len(s.pendingNotifies); got != 1 {
		t.Fatalf("pending = %d, want 1", got)
	}
	// An ACK from a different remote must not clear this entry (key includes
	// the remote, so a msgID reuse across observers cannot collide).
	s.clearPendingNotify("10.0.0.9", 42)
	if got := len(s.pendingNotifies); got != 1 {
		t.Fatalf("pending after foreign ack = %d, want 1", got)
	}
	s.clearPendingNotify("127.0.0.1:1", 42)
	if got := len(s.pendingNotifies); got != 0 {
		t.Fatalf("pending after ack = %d, want 0", got)
	}
}

func exchangeMessage(t *testing.T, conn net.Conn, req Message) Message {
	t.Helper()
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
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	ack, err := Parse(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	return ack
}

func TestCoAPDTLSOptions(t *testing.T) {
	opts := defaultOptions()
	if opts.MaxDatagram != 64*1024 {
		t.Fatalf("default MaxDatagram = %d, want 64 KiB (shared plain-UDP buffer)", opts.MaxDatagram)
	}
	if opts.DTLSReadBufferBytes != 16*1024 {
		t.Fatalf("default DTLSReadBufferBytes = %d, want 16 KiB (per-connection DTLS buffer)", opts.DTLSReadBufferBytes)
	}
	WithDTLSReadBufferBytes(8192)(&opts)
	if opts.DTLSReadBufferBytes != 8192 {
		t.Fatalf("DTLSReadBufferBytes = %d, want 8192", opts.DTLSReadBufferBytes)
	}
	WithDTLSReadBufferBytes(0)(&opts)
	if opts.DTLSReadBufferBytes != 8192 {
		t.Fatalf("DTLSReadBufferBytes = %d, want 8192 (non-positive values ignored)", opts.DTLSReadBufferBytes)
	}
}

func TestCoAPDTLSEcho(t *testing.T) {
	cert, pool := generateTestCert(t)
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
	}
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithDTLS(tlsCfg),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return nil
		}),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer stopGateway(t, gateway)

	dtlsCfg := &dtls.Config{
		InsecureSkipVerify: true,
	}
	addr, err := net.ResolveUDPAddr("udp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dtls.Dial("udp", addr, dtlsCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	req := Message{Type: TypeCON, Code: CodePost, MessageID: 1, Token: []byte{1}, Payload: []byte("dtls-echo")}
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
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	ack, err := Parse(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if ack.Type != TypeACK || ack.MessageID != 1 || ack.Code != CodeCreated {
		t.Fatalf("ack = %#v, payload = %s", ack, string(ack.Payload))
	}
}

func TestCoAPDTLSRejectsPlainUDP(t *testing.T) {
	cert, pool := generateTestCert(t)
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
	}
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithDTLS(tlsCfg),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer stopGateway(t, gateway)

	conn, err := net.Dial("udp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	req := Message{Type: TypeCON, Code: CodeGet, MessageID: 99, Payload: []byte("hi")}
	data, _ := req.Marshal()
	if _, err := conn.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, err = conn.Read(make([]byte, 1024))
	if err == nil {
		t.Fatal("expected timeout/error for plain UDP on DTLS endpoint")
	}
}

func generateTestCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	cert := tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  priv,
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pemCert(certDER))
	return cert, pool
}

func pemCert(der []byte) []byte {
	var buf bytes.Buffer
	pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	return buf.Bytes()
}

func stopGateway(t *testing.T, gateway *runtime.Gateway) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := gateway.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}
