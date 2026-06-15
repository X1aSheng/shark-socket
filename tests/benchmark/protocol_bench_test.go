package benchmark

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/X1aSheng/shark-socket/internal/transport/grpcweb"
	transporthttp "github.com/X1aSheng/shark-socket/internal/transport/http"
	"github.com/X1aSheng/shark-socket/internal/transport/quic"
	"github.com/X1aSheng/shark-socket/internal/transport/tcp"
	"github.com/X1aSheng/shark-socket/internal/transport/udp"
	"github.com/X1aSheng/shark-socket/internal/transport/websocket"
	gws "github.com/gorilla/websocket"
	quicgo "github.com/quic-go/quic-go"
)

func BenchmarkSessionManager_NextID(b *testing.B) {
	manager := runtime.NewSessionManager()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = manager.NextID()
	}
}

func BenchmarkSessionManager_NextID_Parallel(b *testing.B) {
	manager := runtime.NewSessionManager()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = manager.NextID()
		}
	})
}

func BenchmarkSessionManager_RegisterGetUnregister(b *testing.B) {
	manager := runtime.NewSessionManager(runtime.WithMaxSessions(int64(b.N + 1)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := manager.NextID()
		sess := newBenchSession(id, core.ProtocolCustom)
		if err := manager.Register(sess); err != nil {
			b.Fatal(err)
		}
		if _, ok := manager.Get(id); !ok {
			b.Fatalf("session %d not found", id)
		}
		manager.Unregister(id)
	}
}

func BenchmarkPluginChain_Empty(b *testing.B) {
	chain := runtime.NewPluginChain()
	sess := newBenchSession(1, core.ProtocolCustom)
	payload := []byte("payload")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := chain.OnMessage(sess, payload); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPluginChain_5Plugins(b *testing.B) {
	chain := runtime.NewPluginChain(
		benchPlugin{priority: 1},
		benchPlugin{priority: 2},
		benchPlugin{priority: 3},
		benchPlugin{priority: 4},
		benchPlugin{priority: 5},
	)
	sess := newBenchSession(1, core.ProtocolCustom)
	payload := []byte("payload")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := chain.OnMessage(sess, payload); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTCPEcho(b *testing.B) {
	skipIfShort(b)
	h := newEchoHarness(b, func() core.Server {
		return tcp.NewServer(
			tcp.WithAddr("127.0.0.1:0"),
			tcp.WithHandler(echoHandler),
		)
	})

	client := tcp.NewClient(h.Addr, tcp.WithClientLinger(0))
	if err := client.Connect(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = client.Close() })
	payload := []byte("benchmark-payload")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := client.Send(payload); err != nil {
			b.Fatal(err)
		}
		got, err := client.Receive()
		if err != nil {
			b.Fatal(err)
		}
		if !bytes.Equal(got, payload) {
			b.Fatalf("echo = %q, want %q", got, payload)
		}
	}
}

func BenchmarkUDPEcho(b *testing.B) {
	skipIfShort(b)
	h := newEchoHarness(b, func() core.Server {
		return udp.NewServer(
			udp.WithAddr("127.0.0.1:0"),
			udp.WithHandler(echoHandler),
		)
	})

	conn, err := net.Dial("udp", h.Addr)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = conn.Close() })
	payload := []byte("benchmark-payload")
	buf := make([]byte, 1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := conn.Write(payload); err != nil {
			b.Fatal(err)
		}
		if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			b.Fatal(err)
		}
		n, err := conn.Read(buf)
		if err != nil {
			b.Fatal(err)
		}
		if !bytes.Equal(buf[:n], payload) {
			b.Fatalf("echo = %q, want %q", buf[:n], payload)
		}
	}
}

func BenchmarkWSEcho(b *testing.B) {
	skipIfShort(b)
	h := newEchoHarness(b, func() core.Server {
		return websocket.NewServer(
			websocket.WithAddr("127.0.0.1:0"),
			websocket.WithPath("/ws"),
			websocket.WithHandler(echoHandler), websocket.WithCheckOrigin(allowAllOrigins),
		)
	})

	u := url.URL{Scheme: "ws", Host: h.Addr, Path: "/ws"}
	conn, _, err := gws.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = conn.Close() })
	payload := []byte("benchmark-payload")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := conn.WriteMessage(gws.BinaryMessage, payload); err != nil {
			b.Fatal(err)
		}
		if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			b.Fatal(err)
		}
		_, got, err := conn.ReadMessage()
		if err != nil {
			b.Fatal(err)
		}
		if !bytes.Equal(got, payload) {
			b.Fatalf("echo = %q, want %q", got, payload)
		}
	}
}

func BenchmarkHTTPEcho(b *testing.B) {
	skipIfShort(b)
	h := newEchoHarness(b, func() core.Server {
		return transporthttp.NewServer(
			transporthttp.WithAddr("127.0.0.1:0"),
			transporthttp.WithHandler(echoHandler),
		)
	})

	client := &http.Client{Timeout: 5 * time.Second}
	endpoint := "http://" + h.Addr + "/"
	payload := []byte("benchmark-payload")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := client.Post(endpoint, "application/octet-stream", bytes.NewReader(payload))
		if err != nil {
			b.Fatal(err)
		}
		got, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil {
			b.Fatal(readErr)
		}
		if closeErr != nil {
			b.Fatal(closeErr)
		}
		if resp.StatusCode != http.StatusOK {
			b.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		if !bytes.Equal(got, payload) {
			b.Fatalf("echo = %q, want %q", got, payload)
		}
	}
}

type benchPlugin struct {
	core.BasePlugin
	priority int
}

func (p benchPlugin) Name() string  { return "bench-plugin" }
func (p benchPlugin) Priority() int { return p.priority }

type benchSession struct {
	id        uint64
	protocol  core.Protocol
	createdAt time.Time
	closed    chan struct{}
	meta      map[string]any
}

func newBenchSession(id uint64, protocol core.Protocol) *benchSession {
	return &benchSession{
		id:        id,
		protocol:  protocol,
		createdAt: time.Now(),
		closed:    make(chan struct{}),
		meta:      make(map[string]any),
	}
}

func (s *benchSession) ID() uint64                   { return s.id }
func (s *benchSession) Protocol() core.Protocol      { return s.protocol }
func (s *benchSession) RemoteAddr() net.Addr         { return benchAddr("remote") }
func (s *benchSession) LocalAddr() net.Addr          { return benchAddr("local") }
func (s *benchSession) State() core.SessionState     { return core.StateActive }
func (s *benchSession) CreatedAt() time.Time         { return s.createdAt }
func (s *benchSession) LastActiveAt() time.Time      { return s.createdAt }
func (s *benchSession) Context() context.Context     { return context.Background() }
func (s *benchSession) Send([]byte) error            { return nil }
func (s *benchSession) Close(context.Context) error  { return nil }
func (s *benchSession) SetMeta(k string, v any)      { s.meta[k] = v }
func (s *benchSession) GetMeta(k string) (any, bool) { v, ok := s.meta[k]; return v, ok }
func (s *benchSession) DelMeta(k string)             { delete(s.meta, k) }

type benchAddr string

func (a benchAddr) Network() string { return string(a) }
func (a benchAddr) String() string  { return string(a) }

func BenchmarkGRPCWebEcho(b *testing.B) {
	skipIfShort(b)
	h := newEchoHarness(b, func() core.Server {
		return grpcweb.NewServer(
			grpcweb.WithAddr("127.0.0.1:0"),
			grpcweb.WithHandler(echoHandler), grpcweb.WithCheckOrigin(allowAllOrigins),
		)
	})

	client := &http.Client{Timeout: 5 * time.Second}
	url := "http://" + h.Addr + "/grpc"
	payload := []byte("benchmark-payload")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame := make([]byte, 5+len(payload))
		frame[0] = 0
		frame[1] = byte(len(payload) >> 24)
		frame[2] = byte(len(payload) >> 16)
		frame[3] = byte(len(payload) >> 8)
		frame[4] = byte(len(payload))
		copy(frame[5:], payload)
		resp, err := client.Post(url, "application/grpc-web", bytes.NewReader(frame))
		if err != nil {
			b.Fatal(err)
		}
		respBody, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil {
			b.Fatal(readErr)
		}
		if closeErr != nil {
			b.Fatal(closeErr)
		}
		if len(respBody) < 5 {
			b.Fatal("response too short")
		}
	}
}

// BenchmarkQUICEcho measures QUIC echo round-trip including connection setup.
// Each iteration establishes a new QUIC connection (TLS 1.3 handshake),
// opens a stream, sends payload, and reads the echoed response.
func BenchmarkQUICEcho(b *testing.B) {
	skipIfShort(b)
	cfg := &tls.Config{
		Certificates: []tls.Certificate{mustGenerateBenchCert(b)},
		NextProtos:   []string{"shark-socket-quic"},
	}
	h := newEchoHarness(b, func() core.Server {
		return quic.NewServer(
			quic.WithAddr("127.0.0.1:0"),
			quic.WithTLS(cfg),
			quic.WithHandler(echoHandler),
		)
	})

	payload := []byte("benchmark-payload")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		conn, err := quicgo.DialAddr(context.Background(), h.Addr, quic.ClientTLSConfig(true), nil)
		if err != nil {
			b.Fatal(err)
		}
		stream, err := conn.OpenStreamSync(context.Background())
		if err != nil {
			b.Fatal(err)
		}
		_, _ = stream.Write(payload)
		if err := stream.Close(); err != nil {
			_ = conn.CloseWithError(0, "")
			b.Fatal(err)
		}
		readCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		resp, err := conn.AcceptStream(readCtx)
		cancel()
		if err != nil {
			_ = conn.CloseWithError(0, "")
			b.Fatal(err)
		}
		buf := make([]byte, 1024)
		if err := resp.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			_ = conn.CloseWithError(0, "")
			b.Fatal(err)
		}
		n, err := io.ReadFull(resp, buf[:len(payload)])
		if err != nil {
			_ = conn.CloseWithError(0, "")
			b.Fatal(err)
		}
		_ = conn.CloseWithError(0, "")
		if !bytes.Equal(buf[:n], payload) {
			b.Fatalf("echo = %q, want %q", buf[:n], payload)
		}
	}
}

func mustGenerateBenchCert(tb testing.TB) tls.Certificate {
	tb.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		tb.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		tb.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		tb.Fatal(err)
	}
	return cert
}

func stopGateway(tb testing.TB, gateway *runtime.Gateway) {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := gateway.Stop(ctx); err != nil {
		tb.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// Shared benchmark infrastructure
// ---------------------------------------------------------------------------

// echoHandler is the canonical echo handler used by all network benchmarks.
var echoHandler = func(sess core.Session, msg core.Message) error {
	return sess.Send(msg.Payload)
}

// allowAllOrigins permits all WebSocket/gRPC-Web origins in benchmarks.
var allowAllOrigins = func(*http.Request) bool { return true }

// skipIfShort skips the benchmark when -short is set.
// Network benchmarks should call this at the top.
func skipIfShort(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping network benchmark in short mode")
	}
}

// echoHarness holds a running echo server and gateway for benchmarks.
type echoHarness struct {
	Gateway *runtime.Gateway
	Addr    string
}

// newEchoHarness creates a server via the factory, registers it with a new
// Gateway, starts it, and registers cleanup. Returns the address for clients.
func newEchoHarness(b *testing.B, createServer func() core.Server) *echoHarness {
	b.Helper()
	server := createServer()
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		b.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { stopGateway(b, gateway) })
	return &echoHarness{Gateway: gateway, Addr: getAddr(server)}
}

// newEchoHarnessWithPlugins is like newEchoHarness but passes plugins to the Gateway.
func newEchoHarnessWithPlugins(b *testing.B, createServer func() core.Server, plugins ...core.Plugin) *echoHarness {
	b.Helper()
	server := createServer()
	gateway := runtime.NewGateway(runtime.WithPlugins(plugins...))
	if err := gateway.Register(server); err != nil {
		b.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { stopGateway(b, gateway) })
	return &echoHarness{Gateway: gateway, Addr: getAddr(server)}
}

// getAddr extracts the listen address from a server.
func getAddr(srv core.Server) string {
	type addrProvider interface{ Addr() net.Addr }
	if ap, ok := srv.(addrProvider); ok {
		if addr := ap.Addr(); addr != nil {
			return addr.String()
		}
	}
	panic("server does not implement Addr() net.Addr")
}
