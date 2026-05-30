package tcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket-new/internal/core"
	"github.com/X1aSheng/shark-socket-new/internal/runtime"
)

type prefixPlugin struct {
	core.BasePlugin
	prefix []byte
}

func (p prefixPlugin) Name() string { return "prefix" }

func (p prefixPlugin) OnMessage(_ core.Session, data []byte) ([]byte, error) {
	out := append([]byte(nil), p.prefix...)
	out = append(out, data...)
	return out, nil
}

type conditionalDropPlugin struct {
	core.BasePlugin
	drop string
}

func (p conditionalDropPlugin) Name() string { return "conditional-drop" }

func (p conditionalDropPlugin) OnMessage(_ core.Session, data []byte) ([]byte, error) {
	if string(data) == p.drop {
		return nil, core.ErrPluginDrop
	}
	return data, nil
}

func TestGatewayTCPGlobalPluginEchoAndShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gateway := runtime.NewGateway(runtime.WithPlugins(prefixPlugin{prefix: []byte("global:")}))
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}

	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}

	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	framer := LengthPrefixFramer{MaxFrameBytes: 1024}
	if err := framer.WriteFrame(conn, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	got, err := framer.ReadFrame(conn)
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte("global:hello"); !bytes.Equal(got, want) {
		t.Fatalf("echo = %q, want %q", got, want)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	if err := gateway.Stop(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	if count := gateway.Runtime().Sessions().Count(); count != 0 {
		t.Fatalf("session count = %d, want 0", count)
	}
}

func TestTCPClientEcho(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = gateway.Stop(shutdownCtx)
	}()

	client := NewClient(server.Addr().String())
	if err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Send([]byte("client-hello")); err != nil {
		t.Fatal(err)
	}
	got, err := client.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "client-hello" {
		t.Fatalf("echo = %q, want client-hello", got)
	}
}

func TestTCPServerTLSEcho(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverTLS := testServerTLSConfig(t)
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithTLS(serverTLS),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = gateway.Stop(shutdownCtx)
	}()

	client := NewClient(server.Addr().String(), WithClientTLS(&tls.Config{InsecureSkipVerify: true}))
	if err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Send([]byte("tls-hello")); err != nil {
		t.Fatal(err)
	}
	got, err := client.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "tls-hello" {
		t.Fatalf("echo = %q, want tls-hello", got)
	}
}

func TestGatewayTCPRestartKeepsSessionManagerUsable(t *testing.T) {
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

	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	firstAddr := server.Addr().String()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	if err := gateway.Stop(stopCtx); err != nil {
		stopCancel()
		t.Fatal(err)
	}
	stopCancel()

	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = gateway.Stop(shutdownCtx)
	}()
	if server.Addr().String() == firstAddr {
		t.Fatalf("server reused stopped listener address %s", firstAddr)
	}

	client := NewClient(server.Addr().String())
	if err := client.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Send([]byte("restart")); err != nil {
		t.Fatal(err)
	}
	got, err := client.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "restart" {
		t.Fatalf("echo = %q, want restart", got)
	}
}

func testServerTLSConfig(t *testing.T) *tls.Config {
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
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}}
}

func TestGatewayTCPPluginDropSkipsHandlerAndKeepsConnection(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gateway := runtime.NewGateway(runtime.WithPlugins(conditionalDropPlugin{drop: "drop"}))
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = gateway.Stop(shutdownCtx)
	}()

	client := NewClient(server.Addr().String())
	if err := client.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if err := client.Send([]byte("drop")); err != nil {
		t.Fatal(err)
	}
	if conn, ok := client.conn.(interface{ SetReadDeadline(time.Time) error }); ok {
		if err := conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := client.Receive(); err == nil {
		t.Fatalf("received dropped payload response %q", got)
	}
	if conn, ok := client.conn.(interface{ SetReadDeadline(time.Time) error }); ok {
		if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := client.Send([]byte("keep")); err != nil {
		t.Fatal(err)
	}
	got, err := client.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep" {
		t.Fatalf("echo = %q, want keep", got)
	}
}
