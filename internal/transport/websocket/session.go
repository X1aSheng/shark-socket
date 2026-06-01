package websocket

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/gorilla/websocket"
)

type session struct {
	id           uint64
	conn         *websocket.Conn
	createdAt    time.Time
	activeAt     atomic.Int64
	state        atomic.Uint32
	meta         sync.Map
	writeMu      sync.Mutex
	writeTimeout time.Duration
	ctx          context.Context
	cancel       context.CancelFunc
	closeOnce    sync.Once
}

func newSession(id uint64, conn *websocket.Conn, writeTimeout time.Duration) *session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{
		id:           id,
		conn:         conn,
		createdAt:    time.Now(),
		ctx:          ctx,
		cancel:       cancel,
		writeTimeout: writeTimeout,
	}
	s.activeAt.Store(time.Now().UnixNano())
	s.state.Store(uint32(core.StateActive))
	return s
}

func (s *session) ID() uint64                   { return s.id }
func (s *session) Protocol() core.Protocol      { return core.ProtocolWS }
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
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.writeTimeout > 0 {
		s.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout))
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, payload)
}

func (s *session) Close(context.Context) error {
	var err error
	s.closeOnce.Do(func() {
		s.state.Store(uint32(core.StateClosed))
		s.cancel()
		s.writeMu.Lock()
		_ = s.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		s.writeMu.Unlock()
		err = s.conn.Close()
	})
	return err
}

func (s *session) ping() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.WriteMessage(websocket.PingMessage, nil)
}

var _ core.Session = (*session)(nil)
