package udp

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/X1aSheng/shark-socket/internal/transport/shared"
	"github.com/pion/dtls/v3"
)

type Server struct {
	opts      Options
	rt        core.Runtime
	conn      *net.UDPConn
	dtlsLn    net.Listener
	dtlsConns sync.Map // active DTLS connections, closed on shutdown
	closed    atomic.Bool
	started   atomic.Bool
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	sessions  sync.Map
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
	if !s.started.CompareAndSwap(false, true) {
		return fmt.Errorf("udp server already started")
	}
	if s.rt == nil {
		s.rt = runtime.NewRuntime(nil, nil)
	}
	s.closed.Store(false)
	addr, err := net.ResolveUDPAddr("udp", s.opts.Addr)
	if err != nil {
		s.started.Store(false)
		return fmt.Errorf("udp resolve %s: %w", s.opts.Addr, err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	if s.opts.TLSConfig != nil {
		ln, err := dtls.Listen("udp", addr, shared.DTLSConfig(s.opts.TLSConfig))
		if err != nil {
			cancel()
			s.started.Store(false)
			return fmt.Errorf("udp dtls listen %s: %w", s.opts.Addr, err)
		}
		s.dtlsLn = ln
		s.wg.Add(1)
		go s.dtlsAcceptLoop(runCtx)
	} else {
		conn, err := net.ListenUDP("udp", addr)
		if err != nil {
			cancel()
			s.started.Store(false)
			return fmt.Errorf("udp listen %s: %w", s.opts.Addr, err)
		}
		s.conn = conn
		s.wg.Add(2)
		go s.readLoop(runCtx)
		go s.sweepLoop(runCtx)
	}
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
	s.started.Store(false)
	if s.cancel != nil {
		s.cancel()
	}
	// Close individual DTLS connections to unblock read goroutines
	s.dtlsConns.Range(func(key, value any) bool {
		if conn, ok := value.(net.Conn); ok {
			_ = conn.Close()
		}
		return true
	})
	if s.dtlsLn != nil {
		return s.dtlsLn.Close()
	}
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

func (s *Server) Drain(ctx context.Context) error {
	// Wait for read/sweep/DTLS goroutines to finish. The drain goroutine is
	// fire-and-forget: StopAccept already closed the listener and cancelled
	// the context, so all tracked goroutines exit promptly.
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
		s.closeSession(ctx, key, value.(*session))
		return true
	})
	return nil
}

func (s *Server) Addr() net.Addr {
	if s.dtlsLn != nil {
		return s.dtlsLn.Addr()
	}
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

func (s *Server) dtlsAcceptLoop(ctx context.Context) {
	defer s.wg.Done()
	for {
		conn, err := s.dtlsLn.Accept()
		if err != nil {
			if s.closed.Load() || ctx.Err() != nil {
				return
			}
			continue
		}
		s.wg.Add(1)
		go s.handleDTLSConn(ctx, conn)
	}
}

func (s *Server) handleDTLSConn(ctx context.Context, conn net.Conn) {
	defer s.wg.Done()

	id := s.rt.Sessions().NextID()
	sess := newDTLSSession(id, conn)
	s.sessions.Store(id, sess)
	s.dtlsConns.Store(id, conn)
	defer func() {
		s.dtlsConns.Delete(id)
		s.sessions.Delete(id)
		s.rt.Sessions().Unregister(id)
		s.rt.Plugins().OnClose(sess)
	}()

	if err := s.rt.Sessions().Register(sess); err != nil {
		_ = sess.Close(context.Background())
		return
	}
	if err := s.rt.Plugins().OnAccept(sess); err != nil {
		_ = sess.Close(context.Background())
		return
	}

	buf := make([]byte, s.opts.MaxDatagram)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		sess.touch()
		payload := append([]byte(nil), buf[:n]...)
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
				// Remove the session so the peer is not wedged to a closed
				// session. Plain UDP has no per-peer conn whose close would
				// unblock a read loop, so cleanup must happen here.
				s.closeSession(context.Background(), sess.remote.String(), sess)
			}
			continue
		}
		if s.opts.Handler != nil {
			msg := core.Message{SessionID: sess.ID(), Protocol: core.ProtocolUDP, Payload: payload}
			if err := s.opts.Handler(sess, msg); err != nil {
				s.closeSession(context.Background(), sess.remote.String(), sess)
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
					s.closeSession(context.Background(), key, sess)
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
	// Create a provisional session; only allocate ID if it's actually new
	sess := newSession(0, s.conn, addr)
	actual, loaded := s.sessions.LoadOrStore(key, sess)
	if loaded {
		_ = sess.Close(context.Background())
		return actual.(*session)
	}
	// Session is new; assign a proper ID now
	sess.id = s.rt.Sessions().NextID()
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

func (s *Server) closeSession(ctx context.Context, key any, sess *session) {
	if _, loaded := s.sessions.LoadAndDelete(key); !loaded {
		return
	}
	s.rt.Sessions().Unregister(sess.ID())
	_ = sess.Close(ctx)
	s.rt.Plugins().OnClose(sess)
}

var (
	_ core.Server              = (*Server)(nil)
	_ core.RuntimeConfigurable = (*Server)(nil)
	_ core.StagedServer        = (*Server)(nil)
)
