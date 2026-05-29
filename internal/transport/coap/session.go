package coap

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket-new/internal/core"
)

type session struct {
	id        uint64
	conn      *net.UDPConn
	remote    *net.UDPAddr
	local     net.Addr
	createdAt time.Time
	activeAt  atomic.Int64
	state     atomic.Uint32
	meta      sync.Map
	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
}

func newSession(id uint64, conn *net.UDPConn, remote *net.UDPAddr) *session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{
		id:        id,
		conn:      conn,
		remote:    remote,
		local:     conn.LocalAddr(),
		createdAt: time.Now(),
		ctx:       ctx,
		cancel:    cancel,
	}
	s.activeAt.Store(time.Now().UnixNano())
	s.state.Store(uint32(core.StateActive))
	return s
}

func (s *session) ID() uint64                   { return s.id }
func (s *session) Protocol() core.Protocol      { return core.ProtocolCoAP }
func (s *session) RemoteAddr() net.Addr         { return s.remote }
func (s *session) LocalAddr() net.Addr          { return s.local }
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
	_, err := s.conn.WriteToUDP(payload, s.remote)
	return err
}

func (s *session) Close(context.Context) error {
	s.closeOnce.Do(func() {
		s.state.Store(uint32(core.StateClosed))
		s.cancel()
	})
	return nil
}

var _ core.Session = (*session)(nil)
