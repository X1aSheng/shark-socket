package udp

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/runtime"
)

// releaseProbeConn is a net.Conn whose Read fails immediately, standing in for
// a DTLS peer that has gone away. It records whether it was closed, which is
// what decides if the underlying socket is released or leaked.
type releaseProbeConn struct {
	closed atomic.Bool
}

func (c *releaseProbeConn) Read([]byte) (int, error)    { return 0, errors.New("peer gone") }
func (c *releaseProbeConn) Write(p []byte) (int, error) { return len(p), nil }
func (c *releaseProbeConn) Close() error                { c.closed.Store(true); return nil }
func (c *releaseProbeConn) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}
}
func (c *releaseProbeConn) RemoteAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}
}
func (c *releaseProbeConn) SetDeadline(time.Time) error      { return nil }
func (c *releaseProbeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *releaseProbeConn) SetWriteDeadline(time.Time) error { return nil }

// TestUDPDTLSConnReleasedOnPeerDisconnect pins the fix for a socket leak: when a
// DTLS read fails because the peer went away, handleDTLSConn must close the
// connection rather than only dropping it from the bookkeeping maps. Otherwise
// every disconnecting DTLS peer leaks a socket until the process exits.
func TestUDPDTLSConnReleasedOnPeerDisconnect(t *testing.T) {
	s := NewServer(WithAddr("127.0.0.1:0"))
	s.rt = runtime.NewRuntime(nil, nil)

	conn := &releaseProbeConn{}
	s.wg.Add(1)
	s.handleDTLSConn(context.Background(), conn)

	if !conn.closed.Load() {
		t.Fatal("handleDTLSConn returned without closing the DTLS connection: socket leaked")
	}
	if n := s.SessionCount(); n != 0 {
		t.Fatalf("session count = %d after disconnect, want 0", n)
	}
}
