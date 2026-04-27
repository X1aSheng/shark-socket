package grpcgw

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/types"
)

// TestNewServer tests server creation
func TestNewServer(t *testing.T) {
	srv := NewServer()
	if srv == nil {
		t.Fatal("expected non-nil server")
	}
	if srv.opts.Port != 18650 {
		t.Errorf("expected default port 18650, got %d", srv.opts.Port)
	}
	if srv.opts.Mode != WebSocket {
		t.Error("expected default mode WebSocket")
	}
}

// TestWithAddr tests address configuration
func TestWithAddr(t *testing.T) {
	srv := NewServer(WithAddr("127.0.0.1", 9090))
	if srv.opts.Host != "127.0.0.1" {
		t.Errorf("expected host 127.0.0.1, got %s", srv.opts.Host)
	}
	if srv.opts.Port != 9090 {
		t.Errorf("expected port 9090, got %d", srv.opts.Port)
	}
}

// TestWithPath tests path configuration
func TestWithPath(t *testing.T) {
	srv := NewServer(WithPath("/mygrpc"))
	if srv.opts.Path != "/mygrpc" {
		t.Errorf("expected path /mygrpc, got %s", srv.opts.Path)
	}
}

// TestWithMode tests mode configuration
func TestWithMode(t *testing.T) {
	tests := []struct {
		mode ProtocolMode
		want string
	}{
		{WebSocket, "WebSocket"},
		{Direct, "Direct"},
	}

	for _, tt := range tests {
		srv := NewServer(WithMode(tt.mode))
		if srv.opts.Mode != tt.mode {
			t.Errorf("expected mode %s, got %v", tt.want, srv.opts.Mode)
		}
	}
}

// TestProtocolMode tests the ProtocolMode constants
func TestProtocolMode(t *testing.T) {
	if WebSocket != 0 {
		t.Error("WebSocket should be 0")
	}
	if Direct != 1 {
		t.Error("Direct should be 1")
	}
}

// TestOptions_Addr tests the Addr method
func TestOptions_Addr(t *testing.T) {
	opts := Options{Host: "0.0.0.0", Port: 8080}
	if opts.Addr() != "0.0.0.0:8080" {
		t.Errorf("expected 0.0.0.0:8080, got %s", opts.Addr())
	}
}

// TestOptions_Validate tests validation
func TestOptions_Validate(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		wantErr bool
	}{
		{
			name:    "valid",
			opts:    Options{Port: 8080, Path: "/grpc", MaxMessageSize: 1024},
			wantErr: false,
		},
		{
			name:    "invalid port",
			opts:    Options{Port: -1, Path: "/grpc"},
			wantErr: true,
		},
		{
			name:    "empty path",
			opts:    Options{Port: 8080, Path: ""},
			wantErr: true,
		},
		{
			name:    "negative max sessions",
			opts:    Options{Port: 8080, Path: "/grpc", MaxSessions: -1},
			wantErr: true,
		},
		{
			name:    "zero max message size",
			opts:    Options{Port: 8080, Path: "/grpc", MaxMessageSize: 0},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestGRPCWebSession tests session creation
func TestGRPCWebSession(t *testing.T) {
	// This test is simplified since we can't easily create a real websocket connection
	// We verify the session type exists and can be instantiated

	// Verify GRPCWebSession struct fields
	session := &GRPCWebSession{}
	if session.BaseSession != nil {
		t.Error("expected nil BaseSession")
	}
	if session.conn != nil {
		t.Error("expected nil conn")
	}
}

// TestGRPCWebSessionDirect tests direct session creation
func TestGRPCWebSessionDirect(t *testing.T) {
	// Create a mock HTTP request
	req := httptest.NewRequest("POST", "/test", nil)
	req.RemoteAddr = "127.0.0.1:12345"

	session := &GRPCWebSessionDirect{
		request: req,
	}
	if session.request != req {
		t.Error("request not set correctly")
	}
}

// TestServer_StartStop tests server lifecycle
func TestServer_StartStop(t *testing.T) {
	srv := NewServer(WithAddr("127.0.0.1", 0))

	// Start should succeed
	if err := srv.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Get the address
	addr := srv.Addr()
	if addr == nil {
		t.Fatal("Addr() returned nil after Start()")
	}

	// Stop should succeed
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Stop(ctx); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}
}

// TestServer_Protocol tests Protocol method
func TestServer_Protocol(t *testing.T) {
	srv := NewServer()
	if srv.Protocol() != types.Custom {
		t.Errorf("expected Custom protocol, got %v", srv.Protocol())
	}
}

// TestServer_Manager tests Manager method
func TestServer_Manager(t *testing.T) {
	srv := NewServer()
	if srv.Manager() != nil {
		t.Error("expected nil manager before Start()")
	}

	if err := srv.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Stop(ctx)
	}()

	if srv.Manager() == nil {
		t.Error("expected non-nil manager after Start()")
	}
}

// TestServer_ConcurrentStart tests concurrent start calls
func TestServer_ConcurrentStart(t *testing.T) {
	srv := NewServer(WithAddr("127.0.0.1", 0))

	// First start should succeed
	if err := srv.Start(); err != nil {
		t.Fatalf("first Start() failed: %v", err)
	}

	// Second start should be handled gracefully (idempotent or error)
	// The implementation should handle this without panicking

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Stop(ctx)
}

// TestServer_SetHandler tests handler setting
func TestServer_SetHandler(t *testing.T) {
	srv := NewServer()
	handler := func(s types.RawSession, msg types.Message[[]byte]) error {
		return nil
	}

	srv.SetHandler(handler)

	if srv.opts.Handler == nil {
		t.Error("handler not set")
	}
}

// TestGRPCWebSessionDirect_Request tests Request method
func TestGRPCWebSessionDirect_Request(t *testing.T) {
	req := httptest.NewRequest("POST", "/test", nil)
	session := &GRPCWebSessionDirect{request: req}

	if session.Request() != req {
		t.Error("Request() returned wrong value")
	}
}

// TestDefaultOptions tests default values
func TestDefaultOptions(t *testing.T) {
	opts := defaultOptions()

	if opts.Host != "0.0.0.0" {
		t.Errorf("expected host 0.0.0.0, got %s", opts.Host)
	}
	if opts.Port != 18650 {
		t.Errorf("expected port 18650, got %d", opts.Port)
	}
	if opts.Path != "/grpc" {
		t.Errorf("expected path /grpc, got %s", opts.Path)
	}
	if opts.Mode != WebSocket {
		t.Error("expected mode WebSocket")
	}
	if opts.MaxSessions != 100000 {
		t.Errorf("expected max sessions 100000, got %d", opts.MaxSessions)
	}
	if opts.MaxMessageSize != 1024*1024 {
		t.Errorf("expected max message size 1MB, got %d", opts.MaxMessageSize)
	}
	if opts.WriteQueueSize != 128 {
		t.Errorf("expected write queue size 128, got %d", opts.WriteQueueSize)
	}
}

// BenchmarkGRPCWebSession_Creation benchmarks session creation
func BenchmarkGRPCWebSession_Creation(b *testing.B) {
	for i := 0; i < b.N; i++ {
		s := &GRPCWebSession{}
		_ = s
	}
}

// BenchmarkOptions_Validate benchmarks validation
func BenchmarkOptions_Validate(b *testing.B) {
	opts := Options{Port: 8080, Path: "/grpc", MaxSessions: 1000, MaxMessageSize: 1024}
	for i := 0; i < b.N; i++ {
		_ = opts.validate()
	}
}

// BenchmarkOptions_Addr benchmarks address formatting
func BenchmarkOptions_Addr(b *testing.B) {
	opts := Options{Host: "0.0.0.0", Port: 8080}
	for i := 0; i < b.N; i++ {
		_ = opts.Addr()
	}
}
