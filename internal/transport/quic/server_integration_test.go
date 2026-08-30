package quic

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	quicgo "github.com/quic-go/quic-go"
)

type prefixPlugin struct {
	core.BasePlugin
	prefix []byte
}

func (p prefixPlugin) Name() string { return "quic-prefix" }

func (p prefixPlugin) OnMessage(_ core.Session, data []byte) ([]byte, error) {
	out := append([]byte(nil), p.prefix...)
	out = append(out, data...)
	return out, nil
}

func TestQUICRequiresTLS(t *testing.T) {
	server := NewServer(WithAddr("127.0.0.1:0"))
	if err := server.Start(context.Background()); err == nil {
		t.Fatal("Start succeeded without TLS config")
	}
}

func TestGatewayQUICEchoAndShutdown(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithTLS(testTLSConfig(t)),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gateway := runtime.NewGateway(runtime.WithPlugins(prefixPlugin{prefix: []byte("global:")}))
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	conn, err := quicgo.DialAddr(context.Background(), server.Addr().String(), ClientTLSConfig(true), nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := conn.OpenStreamSync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	resp, err := conn.AcceptStream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resp)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "global:hello" {
		t.Fatalf("response = %q, want global:hello", got)
	}
	_ = conn.CloseWithError(0, "done")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := gateway.Stop(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	if count := gateway.Runtime().Sessions().Count(); count != 0 {
		t.Fatalf("session count = %d, want 0", count)
	}
}

func TestQUICOversizedStreamDoesNotInvokeHandler(t *testing.T) {
	called := make(chan struct{}, 1)
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithTLS(testTLSConfig(t)),
		WithHandler(func(core.Session, core.Message) error {
			called <- struct{}{}
			return nil
		}),
	)
	server.opts.MaxMessageSize = 3
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = gateway.Stop(shutdownCtx)
	}()

	conn, err := quicgo.DialAddr(context.Background(), server.Addr().String(), ClientTLSConfig(true), nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := conn.OpenStreamSync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
		t.Fatal("handler called for oversized stream")
	case <-time.After(100 * time.Millisecond):
	}
	_ = conn.CloseWithError(0, "done")
}

func testTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER := x509.MarshalPKCS1PrivateKey(key)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"shark-socket-quic"}}
}

func TestGatewayQUICPluginDropSuppressesResponse(t *testing.T) {
	dropAll := dropPlugin{name: "drop"}
	tlsCfg := testTLSConfig(t)
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithTLS(tlsCfg),
		WithHandler(func(sess core.Session, msg core.Message) error {
			t.Error("handler should not be called when plugin drops")
			return nil
		}),
	)
	gateway := runtime.NewGateway(runtime.WithPlugins(dropAll))
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer stopGateway(t, gateway)

	conn, err := quicgo.DialAddr(context.Background(), server.Addr().String(), ClientTLSConfig(true), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseWithError(0, "")

	stream, err := conn.OpenStreamSync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = stream.Write([]byte("should-be-dropped"))
	_ = stream.Close()

	// The plugin drops the message; the server should keep the session alive.
	time.Sleep(200 * time.Millisecond)
	if count := gateway.Runtime().Sessions().Count(); count != 1 {
		t.Errorf("session count = %d, want 1 (session kept alive)", count)
	}
}

type dropPlugin struct {
	core.BasePlugin
	name string
}

func (p dropPlugin) Name() string { return p.name }

func (p dropPlugin) OnMessage(_ core.Session, _ []byte) ([]byte, error) {
	return nil, core.ErrPluginDrop
}

// TestQUICMaxConnectionsRejectsExcess verifies the accept-cap wiring end to
// end: with a cap of 1, a second connection is rejected (handshake fails or
// the connection is closed with an application error before any stream can
// be used).
func TestQUICMaxConnectionsRejectsExcess(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithTLS(testTLSConfig(t)),
		WithMaxConnections(1),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
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

	// First connection stays open, exhausting the cap.
	conn1, err := quicgo.DialAddr(context.Background(), server.Addr().String(), ClientTLSConfig(true), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn1.CloseWithError(0, "")

	// The second connection must be rejected: the server closes it with an
	// application error right after the handshake, which cancels the client
	// connection context. (Stream writes are buffered by quic-go and would
	// report success even on a closed connection, so the context is the
	// reliable signal.)
	conn2, err := quicgo.DialAddr(context.Background(), server.Addr().String(), ClientTLSConfig(true), nil)
	if err != nil {
		return // handshake rejected: pass
	}
	defer conn2.CloseWithError(0, "")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	select {
	case <-conn2.Context().Done():
		return // connection closed by the server: pass
	case <-ctx.Done():
		t.Fatal("excess QUIC connection stayed usable")
	}
}

// TestQUICServerDirectStop covers the non-staged Stop path (previously 0%):
// a server started without a gateway serves traffic and Stop shuts the
// listener down.
func TestQUICServerDirectStop(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithTLS(testTLSConfig(t)),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Start(ctx); err != nil {
		t.Fatal(err)
	}

	conn, err := quicgo.DialAddr(ctx, server.Addr().String(), ClientTLSConfig(true), nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	resp, err := conn.AcceptStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 16)
	if err := resp.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	n, err := io.ReadFull(resp, buf[:4])
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "ping" {
		t.Fatalf("echo = %q, want ping", buf[:n])
	}
	_ = conn.CloseWithError(0, "")

	if err := server.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	// quic-go retries the handshake for several seconds against a dead
	// listener; bound the probe so the test stays fast.
	probeCtx, probeCancel := context.WithTimeout(context.Background(), time.Second)
	defer probeCancel()
	if _, err := quicgo.DialAddr(probeCtx, server.Addr().String(), ClientTLSConfig(true), nil); err == nil {
		t.Fatal("server still accepting after Stop")
	}
}

func stopGateway(tb testing.TB, gateway *runtime.Gateway) {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = gateway.Stop(ctx)
}
