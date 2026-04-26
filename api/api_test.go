package api

import (
	"context"
	"crypto/tls"
	"errors"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/errs"
	"github.com/X1aSheng/shark-socket/internal/infra/cache"
	"github.com/X1aSheng/shark-socket/internal/infra/circuitbreaker"
	"github.com/X1aSheng/shark-socket/internal/infra/logger"
	"github.com/X1aSheng/shark-socket/internal/infra/metrics"
	"github.com/X1aSheng/shark-socket/internal/infra/pubsub"
	"github.com/X1aSheng/shark-socket/internal/infra/store"
	"github.com/X1aSheng/shark-socket/internal/protocol/tcp"
	"github.com/X1aSheng/shark-socket/internal/types"
)

// ---------------------------------------------------------------------------
// Compile-time type alias verification
// ---------------------------------------------------------------------------

func TestTypeAliases_CompileCheck(t *testing.T) {
	// These assignments verify the type aliases exist and resolve correctly.
	var _ Session[[]byte] = (types.Session[[]byte])(nil)
	var _ RawSession = (types.RawSession)(nil)
	var _ SessionManager = (types.SessionManager)(nil)
	var _ SessionState = types.SessionState(0)
	var _ Message[[]byte] = types.Message[[]byte]{}
	var _ RawMessage = types.RawMessage{}
	var _ MessageConstraint = types.MessageConstraint(nil)
	var _ MessageHandler[[]byte] = (types.MessageHandler[[]byte])(nil)
	var _ RawHandler = (types.RawHandler)(nil)
	var _ Server = (types.Server)(nil)
	var _ Plugin = (types.Plugin)(nil)
	var _ BasePlugin = types.BasePlugin{}
	var _ ProtocolType = types.ProtocolType(0)
	var _ MessageType = types.MessageType(0)
	var _ Logger = (logger.Logger)(nil)
	var _ Cache = (cache.Cache)(nil)
	var _ Store = (store.Store)(nil)
	var _ PubSub = (pubsub.PubSub)(nil)
	var _ *CircuitBreaker = (*circuitbreaker.CircuitBreaker)(nil)
}

// ---------------------------------------------------------------------------
// Protocol Constants
// ---------------------------------------------------------------------------

func TestProtocolConstants(t *testing.T) {
	tests := []struct {
		name     string
		got      types.ProtocolType
		expected types.ProtocolType
	}{
		{"TCP", TCP, 1},
		{"TLS", TLS, 2},
		{"UDP", UDP, 3},
		{"HTTP", HTTP, 4},
		{"WebSocket", WebSocket, 5},
		{"CoAP", CoAP, 6},
		{"Custom", Custom, 99},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("%s = %d, want %d", tt.name, tt.got, tt.expected)
			}
		})
	}
}

func TestMessageTypeConstants(t *testing.T) {
	tests := []struct {
		name     string
		got      types.MessageType
		expected types.MessageType
	}{
		{"Text", Text, 1},
		{"Binary", Binary, 2},
		{"Ping", Ping, 3},
		{"Pong", Pong, 4},
		{"Close", Close, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("%s = %d, want %d", tt.name, tt.got, tt.expected)
			}
		})
	}
}

func TestSessionStateConstants(t *testing.T) {
	tests := []struct {
		name     string
		got      types.SessionState
		expected types.SessionState
	}{
		{"Connecting", Connecting, 0},
		{"Active", Active, 1},
		{"Closing", Closing, 2},
		{"Closed", Closed, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("%s = %d, want %d", tt.name, tt.got, tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Error re-exports
// ---------------------------------------------------------------------------

func TestErrorReExports(t *testing.T) {
	tests := []struct {
		name    string
		got     error
		want    error
	}{
		{"ErrSkip", ErrSkip, errs.ErrSkip},
		{"ErrDrop", ErrDrop, errs.ErrDrop},
		{"ErrBlock", ErrBlock, errs.ErrBlock},
		{"ErrSessionClosed", ErrSessionClosed, errs.ErrSessionClosed},
		{"ErrWriteQueueFull", ErrWriteQueueFull, errs.ErrWriteQueueFull},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !errors.Is(tt.got, tt.want) {
				t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Server Factory Functions
// ---------------------------------------------------------------------------

func TestNewTCPServer_NonNil(t *testing.T) {
	handler := func(sess types.RawSession, msg types.RawMessage) error { return nil }
	srv := NewTCPServer(handler)
	if srv == nil {
		t.Fatal("NewTCPServer returned nil")
	}
}

func TestNewUDPServer_NonNil(t *testing.T) {
	handler := func(sess types.RawSession, msg types.RawMessage) error { return nil }
	srv := NewUDPServer(handler)
	if srv == nil {
		t.Fatal("NewUDPServer returned nil")
	}
}

func TestNewHTTPServer_NonNil(t *testing.T) {
	srv := NewHTTPServer()
	if srv == nil {
		t.Fatal("NewHTTPServer returned nil")
	}
}

func TestNewWebSocketServer_NonNil(t *testing.T) {
	handler := func(sess types.RawSession, msg types.RawMessage) error { return nil }
	srv := NewWebSocketServer(handler)
	if srv == nil {
		t.Fatal("NewWebSocketServer returned nil")
	}
}

func TestNewCoAPServer_NonNil(t *testing.T) {
	handler := func(sess types.RawSession, msg types.RawMessage) error { return nil }
	srv := NewCoAPServer(handler)
	if srv == nil {
		t.Fatal("NewCoAPServer returned nil")
	}
}

// ---------------------------------------------------------------------------
// Gateway Factory
// ---------------------------------------------------------------------------

func TestNewGateway_NonNil(t *testing.T) {
	gw := NewGateway()
	if gw == nil {
		t.Fatal("NewGateway returned nil")
	}
}

// ---------------------------------------------------------------------------
// TCP Client Factory
// ---------------------------------------------------------------------------

func TestNewTCPRawClient_NonNil(t *testing.T) {
	c := NewTCPRawClient("127.0.0.1:0")
	if c == nil {
		t.Fatal("NewTCPRawClient returned nil")
	}
}

// ---------------------------------------------------------------------------
// Plugin Factories
// ---------------------------------------------------------------------------

func TestNewBlacklistPlugin_NonNil(t *testing.T) {
	p := NewBlacklistPlugin("1.2.3.4")
	if p == nil {
		t.Fatal("NewBlacklistPlugin returned nil")
	}
}

func TestNewBlacklistPlugin_ImplementsPlugin(t *testing.T) {
	p := NewBlacklistPlugin("1.2.3.4")
	var _ Plugin = p
}

func TestNewRateLimitPlugin_NonNil(t *testing.T) {
	p := NewRateLimitPlugin(100, 200)
	if p == nil {
		t.Fatal("NewRateLimitPlugin returned nil")
	}
}

func TestNewRateLimitPlugin_ImplementsPlugin(t *testing.T) {
	p := NewRateLimitPlugin(100, 200)
	var _ Plugin = p
}

func TestNewAutoBanPlugin_NonNil(t *testing.T) {
	bl := NewBlacklistPlugin()
	p := NewAutoBanPlugin(bl)
	if p == nil {
		t.Fatal("NewAutoBanPlugin returned nil")
	}
}

func TestNewAutoBanPlugin_ImplementsPlugin(t *testing.T) {
	bl := NewBlacklistPlugin()
	p := NewAutoBanPlugin(bl)
	var _ Plugin = p
}

// ---------------------------------------------------------------------------
// Infrastructure Factories
// ---------------------------------------------------------------------------

func TestNewMemoryCache_NonNil(t *testing.T) {
	c := NewMemoryCache()
	if c == nil {
		t.Fatal("NewMemoryCache returned nil")
	}
}

func TestNewMemoryCache_ImplementsCache(t *testing.T) {
	c := NewMemoryCache()
	var _ Cache = c
}

func TestNewMemoryStore_NonNil(t *testing.T) {
	s := NewMemoryStore()
	if s == nil {
		t.Fatal("NewMemoryStore returned nil")
	}
}

func TestNewMemoryStore_ImplementsStore(t *testing.T) {
	s := NewMemoryStore()
	var _ Store = s
}

func TestNewChannelPubSub_NonNil(t *testing.T) {
	ps := NewChannelPubSub()
	if ps == nil {
		t.Fatal("NewChannelPubSub returned nil")
	}
}

func TestNewChannelPubSub_ImplementsPubSub(t *testing.T) {
	ps := NewChannelPubSub()
	var _ PubSub = ps
}

func TestNewCircuitBreaker_NonNil(t *testing.T) {
	cb := NewCircuitBreaker(5, 30*time.Second)
	if cb == nil {
		t.Fatal("NewCircuitBreaker returned nil")
	}
}

// ---------------------------------------------------------------------------
// Configuration Options - TCP Server
// ---------------------------------------------------------------------------

func TestWithTCPAddr(t *testing.T) {
	opt := WithTCPAddr("127.0.0.1", 9090)
	var o tcp.Options
	opt(&o)
	if o.Host != "127.0.0.1" {
		t.Fatalf("expected host '127.0.0.1', got %q", o.Host)
	}
	if o.Port != 9090 {
		t.Fatalf("expected port 9090, got %d", o.Port)
	}
}

func TestWithTCPTLS(t *testing.T) {
	cfg := &tls.Config{}
	opt := WithTCPTLS(cfg)
	var o tcp.Options
	opt(&o)
	if o.TLSConfig != cfg {
		t.Fatal("expected TLS config to be set")
	}
}

func TestWithTCPPlugins(t *testing.T) {
	bl := NewBlacklistPlugin("1.2.3.4")
	opt := WithTCPPlugins(bl)
	var o tcp.Options
	opt(&o)
	if len(o.Plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(o.Plugins))
	}
}

func TestWithTCPMaxSessions(t *testing.T) {
	opt := WithTCPMaxSessions(5000)
	var o tcp.Options
	opt(&o)
	if o.MaxSessions != 5000 {
		t.Fatalf("expected max sessions 5000, got %d", o.MaxSessions)
	}
}

func TestWithTCPMaxMessageSize(t *testing.T) {
	opt := WithTCPMaxMessageSize(2048)
	var o tcp.Options
	opt(&o)
	if o.MaxMessageSize != 2048 {
		t.Fatalf("expected max message size 2048, got %d", o.MaxMessageSize)
	}
}

// ---------------------------------------------------------------------------
// Configuration Options - Gateway
// ---------------------------------------------------------------------------

func TestWithShutdownTimeout(t *testing.T) {
	opt := WithShutdownTimeout(30 * time.Second)
	// We can't access the internal Options struct directly from outside
	// the gateway package, but we can verify it doesn't panic and returns non-nil.
	if opt == nil {
		t.Fatal("WithShutdownTimeout returned nil")
	}
}

func TestWithMetricsEnabled(t *testing.T) {
	opt := WithMetricsEnabled(false)
	if opt == nil {
		t.Fatal("WithMetricsEnabled returned nil")
	}
}

func TestWithMetricsAddr(t *testing.T) {
	opt := WithMetricsAddr(":8081")
	if opt == nil {
		t.Fatal("WithMetricsAddr returned nil")
	}
}

func TestWithGlobalPlugins(t *testing.T) {
	bl := NewBlacklistPlugin("10.0.0.1")
	opt := WithGlobalPlugins(bl)
	if opt == nil {
		t.Fatal("WithGlobalPlugins returned nil")
	}
}

// ---------------------------------------------------------------------------
// Integration-style: Gateway with servers
// ---------------------------------------------------------------------------

func TestGateway_RegisterTCPAndStop(t *testing.T) {
	gw := NewGateway(WithMetricsEnabled(false))
	handler := func(sess types.RawSession, msg types.RawMessage) error { return nil }
	tcpSrv := NewTCPServer(handler, WithTCPAddr("127.0.0.1", 0))
	if err := gw.Register(tcpSrv); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if err := gw.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := gw.Stop(ctx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

func TestGateway_DuplicateRegistration(t *testing.T) {
	gw := NewGateway(WithMetricsEnabled(false))
	handler := func(sess types.RawSession, msg types.RawMessage) error { return nil }
	srv1 := NewTCPServer(handler, WithTCPAddr("127.0.0.1", 0))
	srv2 := NewTCPServer(handler, WithTCPAddr("127.0.0.1", 0))
	if err := gw.Register(srv1); err != nil {
		t.Fatalf("first Register failed: %v", err)
	}
	err := gw.Register(srv2)
	if err == nil {
		t.Fatal("expected error on duplicate protocol registration")
	}
}

// ---------------------------------------------------------------------------
// SetLogger / DefaultMetrics
// ---------------------------------------------------------------------------

func TestSetLogger(t *testing.T) {
	// Just verify it doesn't panic with nil
	SetLogger(nil)
}

func TestDefaultMetrics(t *testing.T) {
	m := DefaultMetrics()
	if m == nil {
		t.Fatal("DefaultMetrics returned nil")
	}
	// Verify it implements Metrics
	var _ metrics.Metrics = m
}

// ---------------------------------------------------------------------------
// Infrastructure smoke tests (verify the factory-wired objects actually work)
// ---------------------------------------------------------------------------

func TestMemoryCache_RoundTrip(t *testing.T) {
	c := NewMemoryCache()
	defer c.Close()
	ctx := context.Background()
	if err := c.Set(ctx, "key", []byte("val"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := c.Get(ctx, "key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "val" {
		t.Fatalf("expected 'val', got %q", got)
	}
}

func TestMemoryStore_RoundTrip(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	if err := s.Save(ctx, "k", []byte("v")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := s.Load(ctx, "k")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(got) != "v" {
		t.Fatalf("expected 'v', got %q", got)
	}
}

func TestChannelPubSub_RoundTrip(t *testing.T) {
	ps := NewChannelPubSub()
	defer ps.Close()
	ctx := context.Background()

	received := make(chan []byte, 1)
	sub, err := ps.Subscribe(ctx, "topic", func(data []byte) {
		received <- data
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	_ = sub

	if err := ps.Publish(ctx, "topic", []byte("hello")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case msg := <-received:
		if string(msg) != "hello" {
			t.Fatalf("expected 'hello', got %q", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for published message")
	}
}

func TestCircuitBreaker_RoundTrip(t *testing.T) {
	cb := NewCircuitBreaker(3, 100*time.Millisecond)
	// Successful call
	if err := cb.Do(func() error { return nil }); err != nil {
		t.Fatalf("Do (success): %v", err)
	}
}
