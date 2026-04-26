package coap

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/types"
)

func waitForUDPServer(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("udp", addr, 10*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("UDP server at %s not ready after %v", addr, timeout)
}

func TestNewCoAPServer(t *testing.T) {
	s := NewServer(nil)
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
	if s.Protocol() != types.CoAP {
		t.Fatalf("Protocol() = %v, want CoAP", s.Protocol())
	}
}

func TestNewCoAPServer_WithOptions(t *testing.T) {
	s := NewServer(nil,
		WithAddr("127.0.0.1", 0),
		WithSessionTTL(10*time.Second),
		WithAckTimeout(500*time.Millisecond, 2),
		WithMaxSessions(50),
	)
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
}

func TestCoAPServer_StartStop(t *testing.T) {
	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find port: %v", err)
	}
	port := lis.LocalAddr().(*net.UDPAddr).Port
	lis.Close()

	var handlerCalled bool
	handler := func(sess types.RawSession, msg types.RawMessage) error {
		handlerCalled = true
		return sess.Send(msg.Payload)
	}

	s := NewServer(handler, WithAddr("127.0.0.1", port))
	if err := s.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	waitForUDPServer(t, fmt.Sprintf("127.0.0.1:%d", port), 3*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
	_ = handlerCalled
}

func TestCoAPServer_StopIdempotent(t *testing.T) {
	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find port: %v", err)
	}
	port := lis.LocalAddr().(*net.UDPAddr).Port
	lis.Close()

	s := NewServer(nil, WithAddr("127.0.0.1", port))
	if err := s.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	waitForUDPServer(t, fmt.Sprintf("127.0.0.1:%d", port), 3*time.Second)

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := s.Stop(ctx); err != nil {
			t.Fatalf("Stop() call %d: %v", i, err)
		}
	}
}

func TestCoAPServer_StopWithoutStart(t *testing.T) {
	s := NewServer(nil)
	ctx := context.Background()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop() on unstarted server: %v", err)
	}
}
