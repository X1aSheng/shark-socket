package benchmark

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/X1aSheng/shark-socket/internal/transport/grpcweb"
	transporthttp "github.com/X1aSheng/shark-socket/internal/transport/http"
	"github.com/X1aSheng/shark-socket/internal/transport/tcp"
	"github.com/X1aSheng/shark-socket/internal/transport/udp"
	"github.com/X1aSheng/shark-socket/internal/transport/websocket"
	gws "github.com/gorilla/websocket"
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
	server := tcp.NewServer(
		tcp.WithAddr("127.0.0.1:0"),
		tcp.WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		b.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { stopGateway(b, gateway) })

	client := tcp.NewClient(server.Addr().String())
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
	server := udp.NewServer(
		udp.WithAddr("127.0.0.1:0"),
		udp.WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		b.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { stopGateway(b, gateway) })

	conn, err := net.Dial("udp", server.Addr().String())
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
	server := websocket.NewServer(
		websocket.WithAddr("127.0.0.1:0"),
		websocket.WithPath("/ws"),
		websocket.WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		b.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { stopGateway(b, gateway) })

	u := url.URL{Scheme: "ws", Host: server.Addr().String(), Path: "/ws"}
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
	server := transporthttp.NewServer(
		transporthttp.WithAddr("127.0.0.1:0"),
		transporthttp.WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		b.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { stopGateway(b, gateway) })

	client := &http.Client{Timeout: 5 * time.Second}
	endpoint := "http://" + server.Addr().String() + "/"
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
	server := grpcweb.NewServer(
		grpcweb.WithAddr("127.0.0.1:0"),
		grpcweb.WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		b.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { stopGateway(b, gateway) })

	payload := []byte("benchmark-payload")
	url := "http://" + server.Addr().String() + "/grpc"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// gRPC-Web framed message: 1 byte flag + 4 bytes length + payload
		frame := make([]byte, 5+len(payload))
		frame[0] = 0
		frame[1] = byte(len(payload) >> 24)
		frame[2] = byte(len(payload) >> 16)
		frame[3] = byte(len(payload) >> 8)
		frame[4] = byte(len(payload))
		copy(frame[5:], payload)
		resp, err := http.Post(url, "application/grpc-web", bytes.NewReader(frame))
		if err != nil {
			b.Fatal(err)
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if len(respBody) < 5 {
			b.Fatal("response too short")
		}
	}
}

func stopGateway(tb testing.TB, gateway *runtime.Gateway) {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := gateway.Stop(ctx); err != nil {
		tb.Fatal(err)
	}
}
