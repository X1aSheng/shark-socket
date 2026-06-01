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
	handled := 0
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(core.Session, core.Message) error {
			handled++
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
	if handled != 1 {
		t.Fatalf("handler calls = %d, want 1", handled)
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
