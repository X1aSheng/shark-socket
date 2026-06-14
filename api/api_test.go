package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

func TestRunNilGateway(t *testing.T) {
	if err := Run(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil gateway")
	}
}

// Gateway tests
func TestDefaultGatewayCreation(t *testing.T) {
	gw := NewGateway()
	if gw == nil {
		t.Fatal("gateway should not be nil")
	}
	if gw.Ready() {
		t.Fatal("unstarted gateway should not be ready")
	}
}

func TestGatewayWithTimeouts(t *testing.T) {
	timeouts := StageTimeouts{
		StopAccept:    time.Second,
		Drain:         2 * time.Second,
		CloseSessions: 3 * time.Second,
		Finalize:      5 * time.Second,
	}
	gw := NewGateway(WithStageTimeouts(timeouts))
	if gw == nil {
		t.Fatal("gateway with timeouts should not be nil")
	}
}

// Server creation tests
func TestTCPServerCreation(t *testing.T) {
	srv := NewTCPServer(WithTCPAddr("127.0.0.1:0"))
	if srv == nil {
		t.Fatal("tcp server should not be nil")
	}
}

func TestTCPServerWithHandler(t *testing.T) {
	srv := NewTCPServer(WithTCPAddr("127.0.0.1:0"), WithTCPHandler(func(core.Session, core.Message) error { return nil }))
	if srv == nil {
		t.Fatal("tcp server with handler should not be nil")
	}
}

func TestUDPServerCreation(t *testing.T) {
	srv := NewUDPServer(WithUDPAddr("127.0.0.1:0"))
	if srv == nil {
		t.Fatal("udp server should not be nil")
	}
}

func TestUDPServerWithHandler(t *testing.T) {
	srv := NewUDPServer(WithUDPAddr("127.0.0.1:0"), WithUDPHandler(func(core.Session, core.Message) error { return nil }))
	if srv == nil {
		t.Fatal("udp server with handler should not be nil")
	}
}

func TestHTTPServerCreation(t *testing.T) {
	srv := NewHTTPServer(WithHTTPAddr("127.0.0.1:0"))
	if srv == nil {
		t.Fatal("http server should not be nil")
	}
}

func TestHTTPServerWithCORS(t *testing.T) {
	srv := NewHTTPServer(WithHTTPAddr("127.0.0.1:0"), WithHTTPCORSAllowedOrigins([]string{"*"}))
	if srv == nil {
		t.Fatal("http server with CORS should not be nil")
	}
}

func TestWebSocketServerCreation(t *testing.T) {
	srv := NewWebSocketServer(WithWebSocketAddr("127.0.0.1:0"))
	if srv == nil {
		t.Fatal("websocket server should not be nil")
	}
}

func TestWebSocketServerWithPath(t *testing.T) {
	srv := NewWebSocketServer(WithWebSocketAddr("127.0.0.1:0"), WithWebSocketPath("/custom"))
	if srv == nil {
		t.Fatal("websocket server with path should not be nil")
	}
}

func TestCoAPServerCreation(t *testing.T) {
	srv := NewCoAPServer(WithCoAPAddr("127.0.0.1:0"))
	if srv == nil {
		t.Fatal("coap server should not be nil")
	}
}

func TestCoAPServerWithHandler(t *testing.T) {
	srv := NewCoAPServer(
		WithCoAPAddr("127.0.0.1:0"),
		WithCoAPHandler(func(core.Session, core.Message) error { return nil }),
	)
	if srv == nil {
		t.Fatal("coap server with handler should not be nil")
	}
}

func TestCoAPServerWithResponder(t *testing.T) {
	srv := NewCoAPServer(
		WithCoAPAddr("127.0.0.1:0"),
		WithCoAPResponder(func(Session, Message) ([]byte, error) { return nil, nil }),
	)
	if srv == nil {
		t.Fatal("coap server with responder should not be nil")
	}
}

func TestQUICServerCreation(t *testing.T) {
	srv := NewQUICServer()
	if srv == nil {
		t.Fatal("quic server should not be nil")
	}
}

func TestGRPCWebServerCreation(t *testing.T) {
	srv := NewGRPCWebServer(WithGRPCWebAddr("127.0.0.1:0"))
	if srv == nil {
		t.Fatal("grpc-web server should not be nil")
	}
}

func TestGRPCWebServerWithWebSocket(t *testing.T) {
	srv := NewGRPCWebServer(WithGRPCWebAddr("127.0.0.1:0"), WithGRPCWebWebSocketMode("/grpc/ws"))
	if srv == nil {
		t.Fatal("grpc-web with WS mode should not be nil")
	}
}

func TestGRPCWebServerWithMaxBytes(t *testing.T) {
	srv := NewGRPCWebServer(WithGRPCWebAddr("127.0.0.1:0"), WithGRPCWebMaxMessageBytes(65536))
	if srv == nil {
		t.Fatal("grpc-web with max bytes should not be nil")
	}
}

// Gateway registration
func TestGatewayRegistrationAndHealth(t *testing.T) {
	gw := NewGateway()
	if gw.Ready() {
		t.Fatal("unstarted gateway should not be ready")
	}
	health := gw.Health()
	if health == nil {
		t.Fatal("health should not be nil")
	}
	protocols := gw.Protocols()
	if len(protocols) != 0 {
		t.Fatalf("unregistered gateway should have 0 protocols, got %d", len(protocols))
	}
}

func TestGatewayStartStop(t *testing.T) {
	srv := NewTCPServer(WithTCPAddr("127.0.0.1:0"))
	gw := NewGateway(WithPlugins(NewBlacklistPlugin()))
	if err := gw.Register(srv); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := gw.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if !gw.Ready() {
		t.Fatal("started gateway should be ready")
	}
	if err := gw.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if gw.Ready() {
		t.Fatal("stopped gateway should not be ready")
	}
}

func TestGatewayDuplicateProtocol(t *testing.T) {
	srv1 := NewTCPServer(WithTCPAddr("127.0.0.1:0"))
	srv2 := NewTCPServer(WithTCPAddr("127.0.0.1:1"))
	gw := NewGateway(WithPlugins(NewBlacklistPlugin()))
	if err := gw.Register(srv1); err != nil {
		t.Fatal(err)
	}
	if err := gw.Register(srv2); err == nil {
		t.Fatal("expected duplicate protocol error")
	}
}

func TestGatewayNilServer(t *testing.T) {
	gw := NewGateway()
	if err := gw.Register(nil); err == nil {
		t.Fatal("expected error for nil server")
	}
}

// Plugin tests
func TestPluginCreation(t *testing.T) {
	blacklist := NewBlacklistPlugin("192.168.0.1")
	if blacklist == nil {
		t.Fatal("blacklist plugin should not be nil")
	}
	ratelimit := NewRateLimitPlugin(10, time.Second)
	if ratelimit == nil {
		t.Fatal("rate limit plugin should not be nil")
	}
	autoban := NewAutoBanPlugin(3)
	if autoban == nil {
		t.Fatal("autoban plugin should not be nil")
	}
}

func TestHeartbeatPlugin(t *testing.T) {
	sm := NewGateway().Runtime().Sessions()
	p := NewHeartbeatPlugin(sm, 30*time.Second)
	if p == nil {
		t.Fatal("heartbeat plugin should not be nil")
	}
}

func TestPersistencePlugin(t *testing.T) {
	s := NewMemoryStore()
	p := NewPersistencePlugin(s, "test-bucket")
	if p == nil {
		t.Fatal("persistence plugin should not be nil")
	}
}

func TestPersistenceV2Plugin(t *testing.T) {
	s := NewMemoryStore()
	p := NewPersistenceV2Plugin(s, "test-bucket")
	if p == nil {
		t.Fatal("persistence v2 plugin should not be nil")
	}
}

func TestClusterPlugin(t *testing.T) {
	bus := NewPubSub()
	sm := NewGateway().Runtime().Sessions()
	p := NewClusterPlugin("node-1", bus, sm)
	if p == nil {
		t.Fatal("cluster plugin should not be nil")
	}
}

func TestPubSub(t *testing.T) {
	bus := NewPubSub()
	if bus == nil {
		t.Fatal("pubsub should not be nil")
	}
}

// Storage tests
func TestStoreCreation(t *testing.T) {
	s := NewMemoryStore()
	if s == nil {
		t.Fatal("memory store should not be nil")
	}
}

func TestBoltStoreCreation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bolt")
	s, err := NewBoltStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if s == nil {
		t.Fatal("bolt store should not be nil")
	}
	defer s.Close()
}

func TestMessageLogCreation(t *testing.T) {
	store := NewMemoryStore()
	ml, err := NewMessageLog(store, "test-bucket")
	if err != nil {
		t.Fatal(err)
	}
	if ml == nil {
		t.Fatal("message log should not be nil")
	}
}

func TestSessionStoreCreation(t *testing.T) {
	store := NewMemoryStore()
	ss := NewSessionStore(store, "snapshots")
	if ss == nil {
		t.Fatal("session store should not be nil")
	}
}

// LwM2M tests
func TestLwM2MServerCreation(t *testing.T) {
	srv := NewLwM2MServer()
	if srv == nil {
		t.Fatal("lwm2m server should not be nil")
	}
}

func TestLwM2MClientCreation(t *testing.T) {
	srv := NewLwM2MServer()
	client := NewLwM2MClient("device-1", srv)
	if client == nil {
		t.Fatal("lwm2m client should not be nil")
	}
}

func TestParseLwM2MPath(t *testing.T) {
	path, err := ParseLwM2MPath("/3/0/1")
	if err != nil {
		t.Fatal(err)
	}
	if path.ObjectID != 3 || path.InstanceID != 0 || path.ResourceID != 1 {
		t.Fatalf("path = %v, want /3/0/1", path)
	}
	_, err = ParseLwM2MPath("invalid")
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
}

func TestLwM2MCoAPResponder(t *testing.T) {
	srv := NewLwM2MServer()
	fn := NewLwM2MCoAPResponder(srv)
	if fn == nil {
		t.Fatal("coap responder should not be nil")
	}
}

// TCP Client tests
func TestTCPClientCreation(t *testing.T) {
	client := NewTCPClient("127.0.0.1:0")
	if client == nil {
		t.Fatal("tcp client should not be nil")
	}
}

// Observability tests
func TestPrometheusMetricsCreation(t *testing.T) {
	pm := NewPrometheusMetrics()
	if pm == nil {
		t.Fatal("prometheus metrics should not be nil")
	}
}

// Gateway with sessions
func TestGatewayPlugins(t *testing.T) {
	p := NewBlacklistPlugin("10.0.0.1")
	gw := NewGateway(WithPlugins(p))
	if gw == nil {
		t.Fatal("gateway with plugins should not be nil")
	}
}

func TestGatewayWithLogger(t *testing.T) {
	gw := NewGateway(WithLogger(core.NopLogger()))
	if gw == nil {
		t.Fatal("gateway with logger should not be nil")
	}
}

func TestGatewayWithMetrics(t *testing.T) {
	gw := NewGateway(WithMetrics(core.NopMetrics()))
	if gw == nil {
		t.Fatal("gateway with metrics should not be nil")
	}
}

func TestGatewayWithTracer(t *testing.T) {
	gw := NewGateway(WithTracer(core.NopTracer()))
	if gw == nil {
		t.Fatal("gateway with tracer should not be nil")
	}
}

func TestBoltStoreTempDir(t *testing.T) {
	dir := filepath.Join(os.TempDir(), "shark-test-bolt-"+t.Name())
	defer os.RemoveAll(dir)
	s, err := NewBoltStore(filepath.Join(dir, "sub", "test.bolt"))
	if err != nil {
		t.Fatal(err)
	}
	if s == nil {
		t.Fatal("bolt store should not be nil")
	}
	s.Close()
}

func TestSlowHandler(t *testing.T) {
	var called bool
	h := NewSlowHandler(core.NopLogger(), 1*time.Minute, Handler(func(sess Session, msg Message) error {
		called = true
		return nil
	}))
	if h == nil {
		t.Fatal("slow handler should not be nil")
	}
	// Call the handler
	if err := h(nil, Message{}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("handler should have been called")
	}
}

// API Option constructor coverage tests
func TestWithTCPTLS(t *testing.T) {
	opt := WithTCPTLS(nil)
	if opt == nil {
		t.Fatal("WithTCPTLS should return an option")
	}
}

func TestWithUDPDTLS(t *testing.T) {
	opt := WithUDPDTLS(nil)
	if opt == nil {
		t.Fatal("WithUDPDTLS should return an option")
	}
}

func TestWithHTTPHandler(t *testing.T) {
	opt := WithHTTPHandler(nil)
	if opt == nil {
		t.Fatal("WithHTTPHandler should return an option")
	}
	h := NewHTTPServer(WithHTTPAddr("127.0.0.1:0"), WithHTTPHandler(nil))
	if h == nil {
		t.Fatal("server with handler should not be nil")
	}
}

func TestWithWebSocketHandler(t *testing.T) {
	opt := WithWebSocketHandler(nil)
	if opt == nil {
		t.Fatal("WithWebSocketHandler should return an option")
	}
}

func TestWithWebSocketCheckOrigin(t *testing.T) {
	opt := WithWebSocketCheckOrigin(nil)
	if opt == nil {
		t.Fatal("WithWebSocketCheckOrigin should return an option")
	}
}

func TestWithCoAPDTLS(t *testing.T) {
	opt := WithCoAPDTLS(nil)
	if opt == nil {
		t.Fatal("WithCoAPDTLS should return an option")
	}
}

func TestWithQUICOptions(t *testing.T) {
	a := WithQUICAddr("127.0.0.1:0")
	if a == nil {
		t.Fatal("WithQUICAddr should return an option")
	}
	tlsOpt := WithQUICTLS(nil)
	if tlsOpt == nil {
		t.Fatal("WithQUICTLS should return an option")
	}
	h := WithQUICHandler(nil)
	if h == nil {
		t.Fatal("WithQUICHandler should return an option")
	}
}

func TestWithGRPCWebHandler(t *testing.T) {
	opt := WithGRPCWebHandler(nil)
	if opt == nil {
		t.Fatal("WithGRPCWebHandler should return an option")
	}
}

func TestWithGRPCWebCheckOrigin(t *testing.T) {
	opt := WithGRPCWebCheckOrigin(nil)
	if opt == nil {
		t.Fatal("WithGRPCWebCheckOrigin should return an option")
	}
}

func TestNewOpenTelemetryTracer(t *testing.T) {
	tracer := NewOpenTelemetryTracer(nil)
	if tracer == nil {
		t.Fatal("tracer should not be nil")
	}
}

func TestAdaptTyped(t *testing.T) {
	handler := AdaptTyped[*core.Message](nil, nil)
	if handler == nil {
		t.Fatal("adapted handler should not be nil")
	}
}
