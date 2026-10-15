package grpcweb

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/gorilla/websocket"
)

type webSocketSession struct {
	id           uint64
	conn         *websocket.Conn
	writeTimeout time.Duration
	createdAt    time.Time
	activeAt     atomic.Int64
	state        atomic.Uint32
	meta         sync.Map
	writeMu      sync.Mutex
	ctx          context.Context
	cancel       context.CancelFunc
	closeOnce    sync.Once
}

func newWebSocketSession(id uint64, conn *websocket.Conn, writeTimeout time.Duration) *webSocketSession {
	ctx, cancel := context.WithCancel(context.Background())
	s := &webSocketSession{
		id:           id,
		conn:         conn,
		writeTimeout: writeTimeout,
		createdAt:    time.Now(),
		ctx:          ctx,
		cancel:       cancel,
	}
	s.activeAt.Store(time.Now().UnixNano())
	s.state.Store(uint32(core.StateActive))
	return s
}

func (s *webSocketSession) ID() uint64                   { return s.id }
func (s *webSocketSession) Protocol() core.Protocol      { return core.ProtocolGRPCWeb }
func (s *webSocketSession) RemoteAddr() net.Addr         { return s.conn.RemoteAddr() }
func (s *webSocketSession) LocalAddr() net.Addr          { return s.conn.LocalAddr() }
func (s *webSocketSession) State() core.SessionState     { return core.SessionState(s.state.Load()) }
func (s *webSocketSession) CreatedAt() time.Time         { return s.createdAt }
func (s *webSocketSession) LastActiveAt() time.Time      { return time.Unix(0, s.activeAt.Load()) }
func (s *webSocketSession) Context() context.Context     { return s.ctx }
func (s *webSocketSession) SetMeta(k string, v any)      { s.meta.Store(k, v) }
func (s *webSocketSession) GetMeta(k string) (any, bool) { return s.meta.Load(k) }
func (s *webSocketSession) DelMeta(k string)             { s.meta.Delete(k) }

func (s *webSocketSession) touch() {
	s.activeAt.Store(time.Now().UnixNano())
}

// setWriteDeadline bounds the next write. Without it a Send to a dead or
// zero-window peer blocks forever while holding writeMu, which in turn blocks
// Close (it takes the same mutex) and therefore CloseSessions and the whole
// gateway shutdown.
func (s *webSocketSession) setWriteDeadline() {
	if s.writeTimeout > 0 {
		_ = s.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout))
	}
}

func (s *webSocketSession) Send(payload []byte) error {
	if s.State() != core.StateActive {
		return core.ErrSessionClosed
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.setWriteDeadline()
	return s.conn.WriteMessage(websocket.BinaryMessage, payload)
}

func (s *webSocketSession) ping() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.setWriteDeadline()
	return s.conn.WriteMessage(websocket.PingMessage, nil)
}

func (s *webSocketSession) Close(context.Context) error {
	var err error
	s.closeOnce.Do(func() {
		s.state.Store(uint32(core.StateClosed))
		s.cancel()
		s.writeMu.Lock()
		s.setWriteDeadline()
		_ = s.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		s.writeMu.Unlock()
		err = s.conn.Close()
	})
	return err
}

var _ core.Session = (*webSocketSession)(nil)
