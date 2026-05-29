package udp

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket-new/internal/core"
	"github.com/X1aSheng/shark-socket-new/internal/runtime"
)

type Server struct {
	opts     Options
	rt       core.Runtime
	conn     *net.UDPConn
	closed   atomic.Bool
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	sessions sync.Map
}

func NewServer(opts ...Option) *Server {
	cfg := defaultOptions()
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Server{opts: cfg}
}

func (s *Server) Protocol() core.Protocol { return core.ProtocolUDP }

func (s *Server) UseRuntime(rt core.Runtime) {
	s.rt = rt
}

func (s *Server) Start(ctx context.Context) error {
	if s.rt == nil {
		s.rt = runtime.NewRuntime(nil, nil)
	}
	s.closed.Store(false)
	addr, err := net.ResolveUDPAddr("udp", s.opts.Addr)
	if err != nil {
		return fmt.Errorf("udp resolve %s: %w", s.opts.Addr, err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("udp listen %s: %w", s.opts.Addr, err)
	}
	s.conn = conn
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.wg.Add(2)
	go s.readLoop(runCtx)
	go s.sweepLoop(runCtx)
	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	_ = s.StopAccept(ctx)
	_ = s.Drain(ctx)
	return s.CloseSessions(ctx)
}

func (s *Server) StopAccept(context.Context) error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

func (s *Server) Drain(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) CloseSessions(ctx context.Context) error {
	s.sessions.Range(func(key, value any) bool {
		s.closeSession(ctx, key.(string), value.(*session))
		return true
	})
	return nil
}

func (s *Server) Addr() net.Addr {
	if s.conn == nil {
		return nil
	}
	return s.conn.LocalAddr()
}

func (s *Server) SessionCount() int {
	count := 0
	s.sessions.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

func (s *Server) readLoop(ctx context.Context) {
	defer s.wg.Done()
	buf := make([]byte, s.opts.MaxDatagram)
	for {
		n, addr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if s.closed.Load() || ctx.Err() != nil {
				return
			}
			continue
		}
		payload := append([]byte(nil), buf[:n]...)
		sess := s.getOrCreateSession(addr)
		if sess == nil {
			continue
		}
		sess.touch()
		payload, err = s.rt.Plugins().OnMessage(sess, payload)
		if err != nil {
			if err != core.ErrPluginDrop {
				_ = sess.Close(context.Background())
			}
			continue
		}
		if s.opts.Handler != nil {
			msg := core.Message{SessionID: sess.ID(), Protocol: core.ProtocolUDP, Payload: payload}
			if err := s.opts.Handler(sess, msg); err != nil {
				_ = sess.Close(context.Background())
			}
		}
	}
}

func (s *Server) sweepLoop(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.opts.SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			s.sessions.Range(func(key, value any) bool {
				sess := value.(*session)
				if now.Sub(sess.LastActiveAt()) > s.opts.SessionTTL {
					s.closeSession(context.Background(), key.(string), sess)
				}
				return true
			})
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) getOrCreateSession(addr *net.UDPAddr) *session {
	key := addr.String()
	if value, ok := s.sessions.Load(key); ok {
		return value.(*session)
	}
	id := s.rt.Sessions().NextID()
	sess := newSession(id, s.conn, addr)
	actual, loaded := s.sessions.LoadOrStore(key, sess)
	if loaded {
		_ = sess.Close(context.Background())
		return actual.(*session)
	}
	if err := s.rt.Sessions().Register(sess); err != nil {
		s.sessions.Delete(key)
		_ = sess.Close(context.Background())
		return nil
	}
	if err := s.rt.Plugins().OnAccept(sess); err != nil {
		s.closeSession(context.Background(), key, sess)
		return nil
	}
	return sess
}

func (s *Server) closeSession(ctx context.Context, key string, sess *session) {
	s.sessions.Delete(key)
	s.rt.Sessions().Unregister(sess.ID())
	_ = sess.Close(ctx)
	s.rt.Plugins().OnClose(sess)
}

var (
	_ core.Server              = (*Server)(nil)
	_ core.RuntimeConfigurable = (*Server)(nil)
	_ core.StagedServer        = (*Server)(nil)
)
