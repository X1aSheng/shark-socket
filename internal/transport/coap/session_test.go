package coap

import (
	"context"
	"net"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/core"
)

func TestCoAPSessionGetters(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	remote := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9999}
	s := newUDPSession(7, conn, remote)

	if s.ID() != 7 {
		t.Fatalf("ID = %d, want 7", s.ID())
	}
	if s.Protocol() != core.ProtocolCoAP {
		t.Fatalf("Protocol = %s, want coap", s.Protocol())
	}
	if s.RemoteAddr().String() != "127.0.0.1:9999" {
		t.Fatalf("RemoteAddr = %s, want 127.0.0.1:9999", s.RemoteAddr())
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

func TestCoAPSessionMeta(t *testing.T) {
	conn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	defer conn.Close()
	s := newUDPSession(1, conn, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1})

	s.SetMeta("a", "b")
	v, ok := s.GetMeta("a")
	if !ok || v != "b" {
		t.Fatalf("GetMeta = %v, %v", v, ok)
	}
	s.DelMeta("a")
	_, ok = s.GetMeta("a")
	if ok {
		t.Fatal("GetMeta after DelMeta should return false")
	}
}

func TestCoAPSessionClose(t *testing.T) {
	conn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	defer conn.Close()
	s := newUDPSession(1, conn, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1})

	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.State() != core.StateClosed {
		t.Fatal("session should be closed")
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDTLSSessionGetters(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	s := newDTLSSession(99, server)
	if s.ID() != 99 {
		t.Fatalf("ID = %d, want 99", s.ID())
	}
	if s.Protocol() != core.ProtocolCoAP {
		t.Fatalf("Protocol = %s, want coap", s.Protocol())
	}
	if s.RemoteAddr() == nil {
		t.Fatal("RemoteAddr should not be nil")
	}
}
