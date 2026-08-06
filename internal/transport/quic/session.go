package quic

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	quicgo "github.com/quic-go/quic-go"
)

type session struct {
	id           uint64
	conn         *quicgo.Conn
	createdAt    time.Time
	activeAt     atomic.Int64
	state        atomic.Uint32
	meta         sync.Map
	ctx          context.Context
	cancel       context.CancelFunc
	writeCh      chan []byte
	writeTimeout time.Duration
	closeOnce    sync.Once
}

func newSession(id uint64, conn *quicgo.Conn, writeQueue int, writeTimeout time.Duration) *session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{
		id:           id,
		conn:         conn,
		createdAt:    time.Now(),
		ctx:          ctx,
		cancel:       cancel,
		writeCh:      make(chan []byte, writeQueue),
		writeTimeout: writeTimeout,
	}
	s.activeAt.Store(time.Now().UnixNano())
	s.state.Store(uint32(core.StateActive))
	return s
}

func (s *session) ID() uint64                   { return s.id }
func (s *session) Protocol() core.Protocol      { return core.ProtocolQUIC }
func (s *session) RemoteAddr() net.Addr         { return s.conn.RemoteAddr() }
func (s *session) LocalAddr() net.Addr          { return s.conn.LocalAddr() }
func (s *session) CreatedAt() time.Time         { return s.createdAt }
func (s *session) LastActiveAt() time.Time      { return time.Unix(0, s.activeAt.Load()) }
func (s *session) Context() context.Context     { return s.ctx }
func (s *session) SetMeta(k string, v any)      { s.meta.Store(k, v) }
func (s *session) GetMeta(k string) (any, bool) { return s.meta.Load(k) }
func (s *session) DelMeta(k string)             { s.meta.Delete(k) }

func (s *session) State() core.SessionState {
	return core.SessionState(s.state.Load())
}

func (s *session) touch() {
	s.activeAt.Store(time.Now().UnixNano())
}

func (s *session) Send(payload []byte) error {
	if s.State() != core.StateActive {
		return core.ErrSessionClosed
	}
	copied := append([]byte(nil), payload...)
	select {
	case s.writeCh <- copied:
		return nil
	case <-s.ctx.Done():
		return core.ErrSessionClosed
	default:
		return core.ErrWriteQueueFull
	}
}

func (s *session) Close(context.Context) error {
	var err error
	s.closeOnce.Do(func() {
		s.state.Store(uint32(core.StateClosed))
		s.cancel()
		// writeCh is intentionally never closed: Send and writeLoop
		// both select on ctx.Done() so no "send on closed channel"
		// panic can occur during a concurrent Close.
		err = s.conn.CloseWithError(0, "closed")
	})
	return err
}

func (s *session) writeLoop() {
	for {
		select {
		case payload := <-s.writeCh:
			stream, err := s.conn.OpenStreamSync(s.ctx)
			if err != nil {
				return
			}
			if s.writeTimeout > 0 {
				stream.SetWriteDeadline(time.Now().Add(s.writeTimeout))
			}
			// io.Writer may return a short write with a nil error; retry the
			// remainder rather than silently dropping the rest of the payload.
			written := 0
			for written < len(payload) {
				n, err := stream.Write(payload[written:])
				if err != nil {
					_ = stream.Close()
					return
				}
				if n <= 0 {
					_ = stream.Close()
					return
				}
				written += n
			}
			_ = stream.Close()
		case <-s.ctx.Done():
			return
		}
	}
}

var _ core.Session = (*session)(nil)
