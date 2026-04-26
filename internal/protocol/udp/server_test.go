package udp

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

func TestNewUDPServer(t *testing.T) {
	s := NewServer(nil)
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
	if s.Protocol() != types.UDP {
		t.Fatalf("Protocol() = %v, want UDP", s.Protocol())
	}
}

func TestUDPServer_StartStop(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find port: %v", err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	conn.Close()

	s := NewServer(nil, WithAddr("127.0.0.1", port))
	if err := s.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	waitForUDPServer(t, fmt.Sprintf("127.0.0.1:%d", port), 3*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
}

func TestUDPServer_StopIdempotent(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find port: %v", err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	conn.Close()

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

func TestUDPServer_StopWithoutStart(t *testing.T) {
	s := NewServer(nil)
	ctx := context.Background()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop() on unstarted server: %v", err)
	}
}

func TestUDPServer_SessionCount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find port: %v", err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	conn.Close()

	var handlerCalled int
	handler := func(sess types.RawSession, msg types.RawMessage) error {
		handlerCalled++
		return nil
	}

	srv := NewServer(handler, WithAddr("127.0.0.1", port))
	if err := srv.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Stop(ctx)
	}()
	waitForUDPServer(t, fmt.Sprintf("127.0.0.1:%d", port), 3*time.Second)

	// Send a packet
	clientConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer clientConn.Close()
	clientConn.Write([]byte("test"))

	// Poll for handler to process
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if srv.SessionCount() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	count := srv.SessionCount()
	if count < 1 {
		t.Fatalf("SessionCount() = %d, want >= 1", count)
	}
}
