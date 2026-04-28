package quic

import (
	"context"
	"sync"
	"time"

	"github.com/X1aSheng/shark-socket/internal/errs"
	"github.com/X1aSheng/shark-socket/internal/session"
	"github.com/X1aSheng/shark-socket/internal/types"
	"github.com/quic-go/quic-go"
)

// Session wraps a QUIC connection as a RawSession.
type Session struct {
	*session.BaseSession
	conn       *quic.Conn
	writeQueue chan []byte
	draining   chan struct{}
	drained    chan struct{}
	closeOnce  sync.Once
}

// newSession creates a new QUIC session.
func newSession(id uint64, conn *quic.Conn, writeQueueSize int) *Session {
	sess := &Session{
		BaseSession: session.NewBase(id, types.QUIC, conn.RemoteAddr(), conn.LocalAddr()),
		conn:        conn,
		writeQueue:  make(chan []byte, writeQueueSize),
		draining:    make(chan struct{}),
		drained:     make(chan struct{}),
	}
	sess.SetState(types.Active)
	return sess
}

// startWriteLoop drains the writeQueue and opens a unidirectional stream for each message.
func (s *Session) startWriteLoop() {
	go func() {
		defer close(s.drained)
		for {
			select {
			case data, ok := <-s.writeQueue:
				if !ok {
					return
				}
				s.writeToStream(data)
			case <-s.draining:
				// Drain remaining items
				for {
					select {
					case data, ok := <-s.writeQueue:
						if !ok {
							return
						}
						s.writeToStream(data)
					default:
						return
					}
				}
			case <-s.BaseSession.Context().Done():
				return
			}
		}
	}()
}

func (s *Session) writeToStream(data []byte) {
	str, err := s.conn.OpenStreamSync(s.BaseSession.Context())
	if err != nil {
		return
	}
	defer str.Close()
	_, _ = str.Write(data)
}

// Send enqueues data for writing on a new QUIC stream.
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

// SendTyped encodes and sends a typed message.
func (s *Session) SendTyped(msg []byte) error {
	return s.Send(msg)
}

// Close closes the session.
func (s *Session) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		s.SetState(types.Closing)
		close(s.draining)
		select {
		case <-s.drained:
		case <-time.After(5 * time.Second):
		}
		s.SetState(types.Closed)
		s.DoClose()
		closeErr = s.conn.CloseWithError(0, "closed")
	})
	return closeErr
}

// Context returns the BaseSession's context.
func (s *Session) Context() context.Context {
	return s.BaseSession.Context()
}

// Conn returns the underlying QUIC connection.
func (s *Session) Conn() any {
	return s.conn
}

// Compile-time verification.
var _ types.RawSession = (*Session)(nil)
