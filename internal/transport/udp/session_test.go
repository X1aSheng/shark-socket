package udp

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

func TestSessionGetters(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	remote := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345}
	s := newSession(42, conn, remote)

	if s.ID() != 42 {
		t.Fatalf("ID = %d, want 42", s.ID())
	}
	if s.Protocol() != core.ProtocolUDP {
		t.Fatalf("Protocol = %s, want udp", s.Protocol())
	}
	if s.RemoteAddr().String() != "127.0.0.1:12345" {
		t.Fatalf("RemoteAddr = %s, want 127.0.0.1:12345", s.RemoteAddr())
	}
	if s.LocalAddr() == nil {
		t.Fatal("LocalAddr should not be nil")
	}
	if s.CreatedAt().IsZero() {
		t.Fatal("CreatedAt should not be zero")
	}
	if s.LastActiveAt().IsZero() {
		t.Fatal("LastActiveAt should not be zero")
	}
	if s.Context() == nil {
		t.Fatal("Context should not be nil")
	}
	if s.State() != core.StateActive {
		t.Fatalf("State = %d, want active", s.State())
	}
}

func TestSessionMeta(t *testing.T) {
	conn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	defer conn.Close()
	s := newSession(1, conn, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1})

	s.SetMeta("key1", "value1")
	v, ok := s.GetMeta("key1")
	if !ok || v != "value1" {
		t.Fatalf("GetMeta = %v, %v, want value1, true", v, ok)
	}

	_, ok = s.GetMeta("nonexistent")
	if ok {
		t.Fatal("GetMeta for nonexistent key should return false")
	}

	s.DelMeta("key1")
	_, ok = s.GetMeta("key1")
	if ok {
		t.Fatal("GetMeta after DelMeta should return false")
	}
}

func TestSessionTouch(t *testing.T) {
	conn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	defer conn.Close()
	s := newSession(1, conn, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1})

	before := s.LastActiveAt()
	time.Sleep(10 * time.Millisecond)
	s.touch()
	after := s.LastActiveAt()
	if !after.After(before) {
		t.Fatal("touch should update LastActiveAt")
	}
}

func TestSessionSendActive(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Setup a reader
	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 1024)
		n, _, _ := conn.ReadFromUDP(buf)
		done <- buf[:n]
	}()

	s := newSession(1, conn, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: conn.LocalAddr().(*net.UDPAddr).Port})
	if err := s.Send([]byte("hello")); err != nil {
		t.Fatal(err)
	}

	select {
	case data := <-done:
		if string(data) != "hello" {
			t.Fatalf("received = %q, want hello", data)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for data")
	}
}

func TestSessionSendClosed(t *testing.T) {
	conn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	defer conn.Close()
	s := newSession(1, conn, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1})
	s.Close(context.Background())

	if err := s.Send([]byte("data")); err != core.ErrSessionClosed {
		t.Fatalf("Send on closed session = %v, want ErrSessionClosed", err)
	}
}

func TestSessionClose(t *testing.T) {
	conn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	defer conn.Close()
	s := newSession(1, conn, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1})

	ctx := context.Background()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if s.State() != core.StateClosed {
		t.Fatalf("State = %d, want closed", s.State())
	}
	// Close again should be safe (sync.Once)
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSessionContextCancelledOnClose(t *testing.T) {
	conn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	defer conn.Close()
	s := newSession(1, conn, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1})

	select {
	case <-s.Context().Done():
		t.Fatal("context should not be cancelled before close")
	default:
	}
	s.Close(context.Background())
	select {
	case <-s.Context().Done():
		// expected
	default:
		t.Fatal("context should be cancelled after close")
	}
}

func TestSessionCreatedAt(t *testing.T) {
	before := time.Now()
	conn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	defer conn.Close()
	s := newSession(1, conn, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1})
	after := time.Now()

	if s.CreatedAt().Before(before) || s.CreatedAt().After(after) {
		t.Fatalf("CreatedAt = %v, expected between %v and %v", s.CreatedAt(), before, after)
	}
}

// DTLS session tests
func TestDTLSSessionGetters(t *testing.T) {
	// Use a pipe to simulate DTLS connection
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	s := newDTLSSession(99, server)
	if s.ID() != 99 {
		t.Fatalf("ID = %d, want 99", s.ID())
	}
	if s.Protocol() != core.ProtocolUDP {
		t.Fatalf("Protocol = %s, want udp", s.Protocol())
	}
	if s.RemoteAddr() == nil {
		t.Fatal("RemoteAddr should not be nil")
	}
	if s.LocalAddr() == nil {
		t.Fatal("LocalAddr should not be nil")
	}
	if s.State() != core.StateActive {
		t.Fatalf("State = %d, want active", s.State())
	}
}

func TestDTLSSessionSend(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	s := newDTLSSession(1, server)

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 1024)
		n, _ := client.Read(buf)
		done <- buf[:n]
	}()

	if err := s.Send([]byte("dtls-data")); err != nil {
		t.Fatal(err)
	}

	select {
	case data := <-done:
		if string(data) != "dtls-data" {
			t.Fatalf("received = %q, want dtls-data", data)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestDTLSSessionSendClosed(t *testing.T) {
	server, _ := net.Pipe()
	defer server.Close()

	s := newDTLSSession(1, server)
	s.Close(context.Background())
	if err := s.Send([]byte("x")); err != core.ErrSessionClosed {
		t.Fatalf("Send on closed DTLS session = %v, want ErrSessionClosed", err)
	}
}

func TestDTLSSessionClose(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()

	s := newDTLSSession(1, server)
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.State() != core.StateClosed {
		t.Fatal("DTLS session should be closed")
	}
	// Double close should be safe
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Server side should see the close
	select {
	case <-s.Context().Done():
		// expected
	default:
		t.Fatal("DTLS session context should be cancelled")
	}
}
