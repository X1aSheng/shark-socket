package websocket

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/types"
)

func TestNewWSServer(t *testing.T) {
	s := NewServer(nil)
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
	if s.Protocol() != types.WebSocket {
		t.Fatalf("Protocol() = %v, want WebSocket", s.Protocol())
	}
}

func TestNewWSServer_WithOptions(t *testing.T) {
	s := NewServer(nil,
		WithAddr("127.0.0.1", 0),
		WithPath("/custom-ws"),
		WithMaxSessions(500),
		WithMaxMessageSize(2048),
		WithPingPong(10*time.Second, 5*time.Second),
		WithAllowedOrigins("http://localhost:3000"),
	)
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
}

func TestWSServer_StartStop(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find port: %v", err)
	}
	port := lis.Addr().(*net.TCPAddr).Port
	lis.Close()

	s := NewServer(nil, WithAddr("127.0.0.1", port))
	if err := s.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	waitForTCPServer(t, fmt.Sprintf("127.0.0.1:%d", port), 3*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
}

func TestWSServer_StopIdempotent(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find port: %v", err)
	}
	port := lis.Addr().(*net.TCPAddr).Port
	lis.Close()

	s := NewServer(nil, WithAddr("127.0.0.1", port))
	if err := s.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	waitForTCPServer(t, fmt.Sprintf("127.0.0.1:%d", port), 3*time.Second)

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := s.Stop(ctx); err != nil {
			t.Fatalf("Stop() call %d: %v", i, err)
		}
	}
}

func TestWSServer_StopWithoutStart(t *testing.T) {
	s := NewServer(nil)
	ctx := context.Background()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop() on unstarted server: %v", err)
	}
}

func TestWSServer_OriginCheck(t *testing.T) {
	s := NewServer(nil, WithAllowedOrigins("http://allowed.com"))

	// Test allowed origin
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Origin", "http://allowed.com")
	if !s.upgrader.CheckOrigin(req) {
		t.Error("CheckOrigin should allow http://allowed.com")
	}

	// Test blocked origin
	req2 := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req2.Header.Set("Origin", "http://evil.com")
	if s.upgrader.CheckOrigin(req2) {
		t.Error("CheckOrigin should block http://evil.com")
	}
}

func TestWSServer_OriginCheck_Empty(t *testing.T) {
	s := NewServer(nil) // no origins = allow all
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Origin", "http://anything.com")
	if !s.upgrader.CheckOrigin(req) {
		t.Error("CheckOrigin should allow all when no origins configured")
	}
}

func TestWSServer_Manager_BeforeStart(t *testing.T) {
	s := NewServer(nil)
	if mgr := s.Manager(); mgr != nil {
		t.Fatal("Manager() should be nil before Start()")
	}
}
