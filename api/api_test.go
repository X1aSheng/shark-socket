package api

import (
	"context"
	"testing"
	"time"
)

func TestRunNilGateway(t *testing.T) {
	if err := Run(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil gateway")
	}
}

func TestDefaultGatewayCreation(t *testing.T) {
	gw := NewGateway()
	if gw == nil {
		t.Fatal("gateway should not be nil")
	}
	if gw.Ready() {
		t.Fatal("unstarted gateway should not be ready")
	}
}

func TestTCPServerCreation(t *testing.T) {
	srv := NewTCPServer(WithTCPAddr("127.0.0.1:0"))
	if srv == nil {
		t.Fatal("tcp server should not be nil")
	}
}

func TestUDPServerCreation(t *testing.T) {
	srv := NewUDPServer(WithUDPAddr("127.0.0.1:0"))
	if srv == nil {
		t.Fatal("udp server should not be nil")
	}
}

func TestHTTPServerCreation(t *testing.T) {
	srv := NewHTTPServer(WithHTTPAddr("127.0.0.1:0"))
	if srv == nil {
		t.Fatal("http server should not be nil")
	}
}

func TestWebSocketServerCreation(t *testing.T) {
	srv := NewWebSocketServer(WithWebSocketAddr("127.0.0.1:0"))
	if srv == nil {
		t.Fatal("websocket server should not be nil")
	}
}

func TestCoAPServerCreation(t *testing.T) {
	srv := NewCoAPServer(WithCoAPAddr("127.0.0.1:0"))
	if srv == nil {
		t.Fatal("coap server should not be nil")
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

func TestGatewayWithPlugins(t *testing.T) {
	gw := NewGateway(WithPlugins(NewBlacklistPlugin()))
	if gw == nil {
		t.Fatal("gateway with plugins should not be nil")
	}
}


func TestPrometheusMetricsCreation(t *testing.T) {
	pm := NewPrometheusMetrics()
	if pm == nil {
		t.Fatal("prometheus metrics should not be nil")
	}
}

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

func TestStoreCreation(t *testing.T) {
	s := NewMemoryStore()
	if s == nil {
		t.Fatal("memory store should not be nil")
	}
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

func TestPluginCreation(t *testing.T) {
	blacklist := NewBlacklistPlugin()
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
	pubsub := NewPubSub()
	if pubsub == nil {
		t.Fatal("pubsub should not be nil")
	}
}

func TestLwM2MServerCreation(t *testing.T) {
	srv := NewLwM2MServer()
	if srv == nil {
		t.Fatal("lwm2m server should not be nil")
	}
}

func TestTCPClientCreation(t *testing.T) {
	client := NewTCPClient("127.0.0.1:0")
	if client == nil {
		t.Fatal("tcp client should not be nil")
	}
}
