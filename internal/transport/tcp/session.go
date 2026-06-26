package tcp

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

var ErrSessionClosed = errors.New("session closed")

type session struct {
	id                  uint64
	conn                net.Conn
	framer              Framer
	createdAt           time.Time
	activeAt            atomic.Int64
	state               atomic.Uint32
	meta                sync.Map
	ctx                 context.Context
	cancel              context.CancelFunc
	writeCh             chan []byte
	writeTimeout        time.Duration
	writeQueueHighWater float64
	closeOnce           sync.Once
}

func newSession(id uint64, conn net.Conn, framer Framer, writeQueue int, writeTimeout time.Duration, writeQueueHighWater float64) *session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{
		id:                  id,
		conn:                conn,
		framer:              framer,
		createdAt:           time.Now(),
		ctx:                 ctx,
		cancel:              cancel,
		writeCh:             make(chan []byte, writeQueue),
		writeTimeout:        writeTimeout,
		writeQueueHighWater: writeQueueHighWater,
	}
	s.activeAt.Store(time.Now().UnixNano())
	s.state.Store(uint32(core.StateActive))
	return s
}

func (s *session) ID() uint64                   { return s.id }
func (s *session) Protocol() core.Protocol      { return core.ProtocolTCP }
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
		return ErrSessionClosed
	}
	copied := append([]byte(nil), payload...)
	select {
	case s.writeCh <- copied:
		if s.writeQueueHighWater > 0 && len(s.writeCh) >= int(float64(cap(s.writeCh))*s.writeQueueHighWater) {
			// high-water mark reached; caller can observe via write errors
		}
		return nil
	case <-s.ctx.Done():
		return ErrSessionClosed
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
		err = s.conn.Close()
	})
	return err
}

func (s *session) readLoop(handler func([]byte)) {
	defer func() { _ = s.Close(context.Background()) }()
	for {
		payload, err := s.framer.ReadFrame(s.conn)
		if err != nil {
			return
		}
		s.touch()
		handler(payload)
	}
}

func (s *session) writeLoop() {
	defer func() { _ = s.Close(context.Background()) }()
	for payload := range s.writeCh {
		if s.writeTimeout > 0 {
			if err := s.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout)); err != nil {
				return
			}
		}
		if err := s.framer.WriteFrame(s.conn, payload); err != nil {
			return
		}
	}
}

var _ core.Session = (*session)(nil)
