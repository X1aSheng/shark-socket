package quic

import (
	"context"
	"net"
	"time"

	"github.com/X1aSheng/shark-socket/internal/errs"
	"github.com/X1aSheng/shark-socket/internal/session"
	"github.com/X1aSheng/shark-socket/internal/types"
	"github.com/quic-go/quic-go"
)

// Session wraps a QUIC connection as a RawSession.
type Session struct {
	session.BaseSession
	writeQueue chan []byte
	conn       *quic.Conn
}

// newSession creates a new QUIC session.
func newSession(id uint64, conn *quic.Conn, writeQueueSize int) *Session {
	sess := &Session{
		conn:       conn,
		writeQueue: make(chan []byte, writeQueueSize),
	}
	session.InitBase(&sess.BaseSession, id, types.QUIC, conn.LocalAddr(), conn.RemoteAddr())
	sess.SetState(types.Active)
	return sess
}

// Send sends a message.
func (s *Session) Send(data []byte) error {
	if !s.IsAlive() {
		return errs.ErrSessionClosed
	}
	select {
	case s.writeQueue <- data:
		return nil
	default:
		return errs.ErrWriteQueueFull
	}
}

// SendTyped sends a typed message.
func (s *Session) SendTyped(data []byte) error {
	return s.Send(data)
}

// Close closes the session.
func (s *Session) Close() error {
	s.DoClose()
	return s.conn.CloseWithError(0, "closed")
}

// RemoteAddr returns the peer's address.
func (s *Session) RemoteAddr() net.Addr {
	return s.conn.RemoteAddr()
}

// LocalAddr returns the local address.
func (s *Session) LocalAddr() net.Addr {
	return s.conn.LocalAddr()
}

// Context returns the connection's context.
func (s *Session) Context() context.Context {
	return s.conn.Context()
}

// Write writes to the stream.
func (s *Session) Write(data []byte) (int, error) {
	select {
	case s.writeQueue <- data:
		return len(data), nil
	default:
		return 0, errs.ErrWriteQueueFull
	}
}

// Conn returns the underlying QUIC connection.
func (s *Session) Conn() any {
	return s.conn
}

// SetDeadline sets read/write deadlines.
func (s *Session) SetDeadline(t time.Time) error {
	return s.conn.CloseWithError(0, "")
}

// SetReadDeadline sets the read deadline.
func (s *Session) SetReadDeadline(t time.Time) error {
	return s.conn.CloseWithError(0, "")
}

// SetWriteDeadline sets the write deadline.
func (s *Session) SetWriteDeadline(t time.Time) error {
	return s.conn.CloseWithError(0, "")
}

// Compile-time verification that Session implements types.RawSession.
var _ types.RawSession = (*Session)(nil)