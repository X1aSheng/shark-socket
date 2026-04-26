package udp

import (
	"net"
	"sync"

	"github.com/yourname/shark-socket/internal/errs"
	"github.com/yourname/shark-socket/internal/session"
	"github.com/yourname/shark-socket/internal/types"
)

// UDPSession implements a pseudo-session for UDP (connectionless).
type UDPSession struct {
	*session.BaseSession
	conn *net.UDPConn
	addr *net.UDPAddr
	mu   sync.Mutex
}

// Compile-time verification.
var _ types.RawSession = (*UDPSession)(nil)

// NewUDPSession creates a new UDP pseudo-session.
func NewUDPSession(id uint64, conn *net.UDPConn, addr *net.UDPAddr) *UDPSession {
	s := &UDPSession{
		BaseSession: session.NewBase(id, types.UDP, addr, conn.LocalAddr()),
		conn:        conn,
		addr:        addr,
	}
	s.SetState(types.Active)
	return s
}

// Send writes data directly to the UDP address (no queue).
func (s *UDPSession) Send(data []byte) error {
	if !s.IsAlive() {
		return errs.ErrSessionClosed
	}
	s.mu.Lock()
	_, err := s.conn.WriteToUDP(data, s.addr)
	s.mu.Unlock()
	return err
}

// SendTyped encodes and sends (for UDP, same as Send since M=[]byte).
func (s *UDPSession) SendTyped(msg []byte) error {
	return s.Send(msg)
}

// Close marks the session as closed (no drain needed for UDP).
func (s *UDPSession) Close() error {
	if s.SetState(types.Closed) {
		s.DoClose()
	}
	return nil
}

// Addr returns the remote UDP address.
func (s *UDPSession) Addr() *net.UDPAddr {
	return s.addr
}
