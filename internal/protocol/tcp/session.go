package tcp

import (
	"net"
	"sync"
	"time"

	"github.com/X1aSheng/shark-socket/internal/errs"
	"github.com/X1aSheng/shark-socket/internal/infra/bufferpool"
	"github.com/X1aSheng/shark-socket/internal/session"
	"github.com/X1aSheng/shark-socket/internal/types"
)

// TCPSession implements types.Session for TCP connections.
type TCPSession struct {
	*session.BaseSession
	conn       net.Conn
	framer     Framer
	writeQueue chan *bufferpool.Buffer
	encoder    func([]byte) ([]byte, error)
	drained    chan struct{}
	closeOnce  sync.Once
	draining   chan struct{}
	drainTimeout time.Duration
}

// Compile-time verification.
var _ types.RawSession = (*TCPSession)(nil)

// NewTCPSession creates a new TCP session.
func NewTCPSession(id uint64, conn net.Conn, framer Framer, writeQueueSize int) *TCPSession {
	s := &TCPSession{
		BaseSession: session.NewBase(id, types.TCP, conn.RemoteAddr(), conn.LocalAddr()),
		conn:        conn,
		framer:      framer,
		writeQueue:  make(chan *bufferpool.Buffer, writeQueueSize),
		drained:     make(chan struct{}),
		draining:    make(chan struct{}),
	}
	s.SetState(types.Active)
	return s
}

// SetDrainTimeout sets the drain timeout for graceful close.
func (s *TCPSession) SetDrainTimeout(d time.Duration) {
	s.drainTimeout = d
}

// Send enqueues data for non-blocking write.
func (s *TCPSession) Send(data []byte) error {
	if !s.IsAlive() {
		return errs.ErrSessionClosed
	}
	buf := bufferpool.GetDefault(len(data))
	copy(buf.Bytes(), data)
	select {
	case s.writeQueue <- buf:
		return nil
	default:
		bufferpool.PutDefault(buf)
		return errs.ErrWriteQueueFull
	}
}

// SendTyped encodes and sends a typed message.
func (s *TCPSession) SendTyped(msg []byte) error {
	if s.encoder != nil {
		encoded, err := s.encoder(msg)
		if err != nil {
			return errs.ErrEncodeFailure
		}
		return s.Send(encoded)
	}
	return s.Send(msg)
}

// Close performs the 6-step graceful close state machine.
func (s *TCPSession) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		// Step 1: CAS Active -> Closing
		if !s.SetState(types.Closing) {
			if s.State() == types.Closed {
				return
			}
		}

		// Step 2: Signal writeLoop to drain
		close(s.draining)

		// Step 3: Wait for writeQueue drain with timeout
		timeout := s.drainTimeout
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		select {
		case <-s.drained:
		case <-time.After(timeout):
		}

		// Step 4: CAS Closing -> Closed
		s.SetState(types.Closed)

		// Step 5: Cancel context
		s.DoClose()

		// Step 6: Close connection
		closeErr = s.conn.Close()
	})
	return closeErr
}

// ReadLoop reads frames and submits to the worker pool.
func (s *TCPSession) ReadLoop(pool *WorkerPool, chainHandler func(types.RawSession, []byte)) {
	defer s.Close()

	for {
		select {
		case <-s.Context().Done():
			return
		default:
		}

		payload, err := s.framer.ReadFrame(s.conn)
		if err != nil {
			return
		}
		s.TouchActive()
		if chainHandler != nil {
			chainHandler(s, payload)
		} else if pool != nil {
			_ = pool.Submit(s, payload)
		}
	}
}

// WriteLoop drains the writeQueue and writes to the connection.
func (s *TCPSession) WriteLoop() {
	defer close(s.drained)

	for {
		select {
		case buf, ok := <-s.writeQueue:
			if !ok {
				return
			}
			_ = s.framer.WriteFrame(s.conn, buf.Bytes())
			bufferpool.PutDefault(buf)
		case <-s.draining:
			// Drain remaining items
			for {
				select {
				case buf, ok := <-s.writeQueue:
					if !ok {
						return
					}
					_ = s.framer.WriteFrame(s.conn, buf.Bytes())
					bufferpool.PutDefault(buf)
				default:
					return
				}
			}
		case <-s.Context().Done():
			return
		}
	}
}
