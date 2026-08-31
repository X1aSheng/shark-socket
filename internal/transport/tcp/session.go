package tcp

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/transport/shared"
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
	readTimeout         time.Duration
	writeQueueHighWater float64
	onIdleClose         func() // invoked when a read deadline reclaims the session
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
	// High-water backpressure: reject before the queue is saturated so a slow
	// consumer sheds load earlier (WriteQueueHighWater, default 0.8 of the
	// queue capacity; 0 disables). The threshold is clamped to at least 1 so a
	// small queue (e.g. WithWriteQueue(1)) still accepts its first write
	// instead of truncating 0.8*cap to 0 and rejecting everything.
	if s.writeQueueHighWater > 0 {
		threshold := int(float64(cap(s.writeCh)) * s.writeQueueHighWater)
		if threshold < 1 {
			threshold = 1
		}
		if len(s.writeCh) >= threshold {
			return core.ErrWriteQueueFull
		}
	}
	copied := append([]byte(nil), payload...)
	select {
	case s.writeCh <- copied:
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
		// writeCh is intentionally never closed: Send and writeLoop
		// both select on ctx.Done() so no "send on closed channel"
		// panic can occur during a concurrent Close.
		err = s.conn.Close()
	})
	return err
}

func (s *session) readLoop(handler func([]byte)) {
	defer func() { _ = s.Close(context.Background()) }()
	for {
		if s.readTimeout > 0 {
			if err := s.conn.SetReadDeadline(time.Now().Add(s.readTimeout)); err != nil {
				return
			}
		}
		payload, err := s.framer.ReadFrame(s.conn)
		if err != nil {
			// A deadline expiry means the peer went silent (a half-open or
			// zombie connection) rather than disconnecting cleanly; surface
			// the reclaim so the server can count it.
			if shared.IsTimeout(err) && s.onIdleClose != nil {
				s.onIdleClose()
			}
			return
		}
		s.touch()
		handler(payload)
	}
}

func (s *session) writeLoop() {
	defer func() { _ = s.Close(context.Background()) }()
	for {
		select {
		case payload := <-s.writeCh:
			if s.writeTimeout > 0 {
				if err := s.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout)); err != nil {
					return
				}
			}
			if err := s.framer.WriteFrame(s.conn, payload); err != nil {
				return
			}
		case <-s.ctx.Done():
			return
		}
	}
}

var _ core.Session = (*session)(nil)
