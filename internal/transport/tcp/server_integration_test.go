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
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
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

// dialWithLinger dials a TCP connection with SO_LINGER(0) to avoid
// TIME_WAIT port accumulation during tests.
func dialWithLinger(ctx context.Context, network, addr string) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetLinger(0)
	}
	return conn, nil
}

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

	conn, err := dialWithLinger(ctx, "tcp", server.Addr().String())
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

// TestGatewayTCPHandlerPanicClosesSession verifies that a panicking user
// handler is recovered (the session is closed) instead of crashing the whole
// process, and that the server keeps serving afterwards.
func TestGatewayTCPHandlerPanicClosesSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(sess core.Session, msg core.Message) error {
			panic("user handler boom")
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

	conn, err := dialWithLinger(ctx, "tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	framer := LengthPrefixFramer{MaxFrameBytes: 1024}
	if err := framer.WriteFrame(conn, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	// The panicking handler must be recovered and the session closed, so the
	// client read sees EOF instead of the test process crashing.
	if _, err := framer.ReadFrame(conn); err == nil {
		t.Fatal("expected connection to be closed after handler panic")
	}

	// The server must still be accepting new connections.
	conn2, err := dialWithLinger(ctx, "tcp", server.Addr().String())
	if err != nil {
		t.Fatalf("server not serving after handler panic: %v", err)
	}
	_ = conn2.Close()
}

// TestTCPMaxConnectionsRejectsExcess verifies the accept-cap wiring end to
// end: with a cap of 1, the first connection is served normally and the
// second is closed immediately on accept.
func TestTCPMaxConnectionsRejectsExcess(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
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
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = gateway.Stop(shutdownCtx)
	}()

	framer := LengthPrefixFramer{MaxFrameBytes: 1024}

	// First connection is served normally and must stay open.
	conn1, err := dialWithLinger(context.Background(), "tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn1.Close()
	if err := framer.WriteFrame(conn1, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	got, err := framer.ReadFrame(conn1)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ping" {
		t.Fatalf("echo = %q, want ping", got)
	}

	// Second connection exceeds the cap: closed immediately, so the client
	// read sees EOF.
	conn2, err := dialWithLinger(context.Background(), "tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close()
	if err := conn2.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := framer.ReadFrame(conn2); err == nil {
		t.Fatal("excess connection was not closed")
	}
}

// TestTCPAcceptRateRejectsBurst verifies the accept-rate wiring end to end:
// with a rate of 1/s (burst 1), a second connection inside the same window
// is closed immediately.
func TestTCPAcceptRateRejectsBurst(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithAcceptRate(1),
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
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = gateway.Stop(shutdownCtx)
	}()

	framer := LengthPrefixFramer{MaxFrameBytes: 1024}

	// First connection consumes the single burst token and is served.
	conn1, err := dialWithLinger(context.Background(), "tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn1.Close()
	if err := framer.WriteFrame(conn1, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	if _, err := framer.ReadFrame(conn1); err != nil {
		t.Fatal(err)
	}

	// Second connection inside the same second has no token: closed.
	conn2, err := dialWithLinger(context.Background(), "tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close()
	if err := conn2.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := framer.ReadFrame(conn2); err == nil {
		t.Fatal("rate-limited connection was not closed")
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

	client := NewClient(server.Addr().String(), WithClientLinger(0))
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

	client := NewClient(server.Addr().String(), WithClientLinger(0), WithClientTLS(&tls.Config{InsecureSkipVerify: true}))
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

func TestTCPServerMTLSRequiresVerifiedClientCertificate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverTLS, clientTLS := testMutualTLSConfigs(t)
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

	withoutCert := NewClient(server.Addr().String(), WithClientLinger(0), WithClientTLS(&tls.Config{InsecureSkipVerify: true}))
	if err := withoutCert.Connect(ctx); err != nil {
		_ = withoutCert.Close()
	} else {
		defer withoutCert.Close()
		if err := withoutCert.Send([]byte("no-cert")); err == nil {
			if conn, ok := withoutCert.conn.(interface{ SetReadDeadline(time.Time) error }); ok {
				if err := conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
					t.Fatal(err)
				}
			}
			if got, err := withoutCert.Receive(); err == nil {
				t.Fatalf("client without certificate completed frame exchange: %q", got)
			}
		}
	}

	withCert := NewClient(server.Addr().String(), WithClientLinger(0), WithClientTLS(clientTLS))
	if err := withCert.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer withCert.Close()
	if err := withCert.Send([]byte("mtls-hello")); err != nil {
		t.Fatal(err)
	}
	got, err := withCert.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "mtls-hello" {
		t.Fatalf("echo = %q, want mtls-hello", got)
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
	// On Windows, Linger(0) may release the port fast enough that
	// the OS reassigns it. The important check is that the restarted
	// server accepts connections (verified below with client echo).
	if server.Addr().String() == firstAddr {
		t.Logf("server reused listener address %s (port recycled fast)", firstAddr)
	}

	client := NewClient(server.Addr().String(), WithClientLinger(0))
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

func testMutualTLSConfigs(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := x509.Certificate{
		SerialNumber:          big.NewInt(100),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	serverCert := signedTestCertificate(t, "localhost", x509.ExtKeyUsageServerAuth, caDER, &caTemplate, caKey)
	clientCert := signedTestCertificate(t, "client", x509.ExtKeyUsageClientAuth, caDER, &caTemplate, caKey)
	roots := x509.NewCertPool()
	roots.AddCert(mustParseCert(t, caDER))

	serverTLS := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    roots,
	}
	clientTLS := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      roots,
		ServerName:   "localhost",
	}
	return serverTLS, clientTLS
}

func signedTestCertificate(t *testing.T, commonName string, usage x509.ExtKeyUsage, caDER []byte, ca *x509.Certificate, caKey *rsa.PrivateKey) tls.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
	}
	if usage == x509.ExtKeyUsageServerAuth {
		template.DNSNames = []string{commonName}
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	cert.Certificate = append(cert.Certificate, caDER)
	return cert
}

func mustParseCert(t *testing.T, der []byte) *x509.Certificate {
	t.Helper()
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
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

	client := NewClient(server.Addr().String(), WithClientLinger(0))
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

// countPlugin records accept/close calls so a test can assert the plugin chain
// and transport do not double-notify OnClose for a session that was never
// accepted.
type countPlugin struct {
	core.BasePlugin
	acceptCalls atomic.Int32
	closeCalls  atomic.Int32
	failAccept  bool
}

func (p *countPlugin) Name() string { return "count" }

func (p *countPlugin) OnAccept(core.Session) error {
	p.acceptCalls.Add(1)
	if p.failAccept {
		return core.ErrPluginBlock
	}
	return nil
}

func (p *countPlugin) OnClose(core.Session) { p.closeCalls.Add(1) }

// TestTCPOnAcceptFailureNoDoubleClose verifies that when a later plugin fails
// OnAccept, the already-accepted plugin receives OnClose exactly once (via the
// plugin chain rollback) and the transport does not call OnClose again for a
// session that was never fully accepted.
func TestTCPOnAcceptFailureNoDoubleClose(t *testing.T) {
	okPlugin := &countPlugin{}
	badPlugin := &countPlugin{failAccept: true}
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(sess core.Session, msg core.Message) error { return nil }),
	)
	gateway := runtime.NewGateway(runtime.WithPlugins(okPlugin, badPlugin))
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	conn, err := dialWithLinger(context.Background(), "tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if badPlugin.acceptCalls.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if badPlugin.acceptCalls.Load() != 1 {
		t.Fatalf("bad plugin accept calls = %d, want 1", badPlugin.acceptCalls.Load())
	}
	// Give any (incorrect) transport-level OnClose time to fire.
	time.Sleep(200 * time.Millisecond)
	if got := okPlugin.closeCalls.Load(); got != 1 {
		t.Fatalf("ok plugin close calls = %d, want exactly 1 (chain rollback only)", got)
	}
	if got := badPlugin.closeCalls.Load(); got != 0 {
		t.Fatalf("bad plugin close calls = %d, want 0 (never accepted)", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = gateway.Stop(ctx)
}
