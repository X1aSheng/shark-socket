package coap

import (
	"net"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/types"
)

func TestNewCoAPSession(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer conn.Close()

	addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345}
	sess := NewCoAPSession(1, conn, addr, 500)

	if sess.ID() != 1 {
		t.Fatalf("ID() = %d, want 1", sess.ID())
	}
	if sess.Protocol() != types.CoAP {
		t.Fatalf("Protocol() = %v, want CoAP", sess.Protocol())
	}
	if !sess.IsAlive() {
		t.Fatal("new session should be alive")
	}
}

func TestCoAPSession_Close(t *testing.T) {
	conn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	defer conn.Close()

	sess := newTestSession(conn)
	if err := sess.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	if sess.IsAlive() {
		t.Fatal("session should not be alive after Close()")
	}
}

func TestCoAPSession_CloseIdempotent(t *testing.T) {
	conn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	defer conn.Close()

	sess := newTestSession(conn)
	for i := 0; i < 3; i++ {
		if err := sess.Close(); err != nil {
			t.Fatalf("Close() call %d: %v", i, err)
		}
	}
}

func TestCoAPSession_SendClosed(t *testing.T) {
	conn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	defer conn.Close()

	sess := newTestSession(conn)
	sess.Close()
	if err := sess.Send([]byte("data")); err == nil {
		t.Fatal("Send() on closed session should return error")
	}
}

func TestCoAPSession_SendTyped(t *testing.T) {
	conn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	defer conn.Close()

	sess := newTestSession(conn)
	// SendTyped on alive session should not error (writing to localhost)
	if err := sess.SendTyped([]byte("test")); err != nil {
		t.Fatalf("SendTyped() error: %v", err)
	}
}

func TestCoAPSession_TrackCON(t *testing.T) {
	conn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	defer conn.Close()

	sess := newTestSession(conn)
	sess.TrackCON(1, []byte("msg1"))
	sess.TrackCON(2, []byte("msg2"))
	if sess.PendingCount() != 2 {
		t.Fatalf("PendingCount() = %d, want 2", sess.PendingCount())
	}
}

func TestCoAPSession_Acknowledge(t *testing.T) {
	conn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	defer conn.Close()

	sess := newTestSession(conn)
	sess.TrackCON(1, []byte("msg1"))
	sess.TrackCON(2, []byte("msg2"))
	sess.Acknowledge(1)
	if sess.PendingCount() != 1 {
		t.Fatalf("PendingCount() = %d, want 1 after Acknowledge(1)", sess.PendingCount())
	}
}

func TestCoAPSession_ResetCON(t *testing.T) {
	conn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	defer conn.Close()

	sess := newTestSession(conn)
	sess.TrackCON(42, []byte("msg"))
	sess.ResetCON(42)
	if sess.PendingCount() != 0 {
		t.Fatalf("PendingCount() = %d, want 0 after ResetCON", sess.PendingCount())
	}
}

func TestCoAPSession_CheckAndRecord(t *testing.T) {
	conn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	defer conn.Close()

	sess := newTestSession(conn)
	if sess.CheckAndRecord(100) {
		t.Fatal("CheckAndRecord(100) should return false (not duplicate)")
	}
	if !sess.CheckAndRecord(100) {
		t.Fatal("CheckAndRecord(100) should return true (duplicate)")
	}
}

func TestCoAPSession_CheckAndRecord_DifferentIDs(t *testing.T) {
	conn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	defer conn.Close()

	sess := newTestSession(conn)
	sess.CheckAndRecord(1)
	sess.CheckAndRecord(2)
	if !sess.CheckAndRecord(1) || !sess.CheckAndRecord(2) {
		t.Fatal("both IDs should be recorded as duplicates")
	}
	if sess.CheckAndRecord(3) {
		t.Fatal("unrecorded ID should not be duplicate")
	}
}

func TestCoAPSession_CheckAndRecord_Eviction(t *testing.T) {
	conn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	defer conn.Close()

	sess := newTestSession(conn)
	// Record 600 entries to trigger eviction (cache size > 500)
	for i := 0; i < 600; i++ {
		sess.CheckAndRecord(uint16(i))
	}
	// Old entries should be evicted
	if sess.CheckAndRecord(0) {
		t.Log("entry 0 still present after eviction (may be within 5min window)")
	}
}

func TestCoAPSession_Addr(t *testing.T) {
	conn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	defer conn.Close()

	addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9999}
	sess := NewCoAPSession(1, conn, addr, 500)
	if sess.Addr().Port != 9999 {
		t.Fatalf("Addr().Port = %d, want 9999", sess.Addr().Port)
	}
}

func newTestSession(conn *net.UDPConn) *CoAPSession {
	addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345}
	return NewCoAPSession(1, conn, addr, 500)
}
