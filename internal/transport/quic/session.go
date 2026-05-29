package quic

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket-new/internal/core"
	quicgo "github.com/quic-go/quic-go"
)

type session struct {
	id        uint64
	conn      *quicgo.Conn
	createdAt time.Time
	activeAt  atomic.Int64
	state     atomic.Uint32
	meta      sync.Map
	ctx       context.Context
	cancel    context.CancelFunc
	writeCh   chan []byte
	closeOnce sync.Once
}

func newSession(id uint64, conn *quicgo.Conn, writeQueue int) *session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{
		id:        id,
		conn:      conn,
		createdAt: time.Now(),
		ctx:       ctx,
		cancel:    cancel,
		writeCh:   make(chan []byte, writeQueue),
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
	default:
		return core.ErrWriteQueueFull
	}
}

func (s *session) Close(context.Context) error {
	var err error
	s.closeOnce.Do(func() {
		s.state.Store(uint32(core.StateClosed))
		s.cancel()
		close(s.writeCh)
		err = s.conn.CloseWithError(0, "closed")
	})
	return err
}

func (s *session) writeLoop() {
	for payload := range s.writeCh {
		stream, err := s.conn.OpenStreamSync(s.ctx)
		if err != nil {
			return
		}
		_, _ = stream.Write(payload)
		_ = stream.Close()
	}
}

var _ core.Session = (*session)(nil)
