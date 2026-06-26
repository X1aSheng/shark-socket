package tcp

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/X1aSheng/shark-socket/internal/transport/shared"
)

type Server struct {
	opts     Options
	rt       core.Runtime
	listener net.Listener
	acceptor *shared.Acceptor
	closed   atomic.Bool
	started  atomic.Bool
	acceptWG sync.WaitGroup
	connWG   sync.WaitGroup
	sessions sync.Map
	pool     *workerPool
}

func NewServer(opts ...Option) *Server {
	cfg := defaultOptions()
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Server{opts: cfg}
}

func (s *Server) Protocol() core.Protocol { return core.ProtocolTCP }

func (s *Server) UseRuntime(rt core.Runtime) {
	s.rt = rt
}

func (s *Server) Start(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return fmt.Errorf("tcp server already started")
	}
	if s.rt == nil {
		s.rt = runtime.NewRuntime(nil, nil)
	}
	s.closed.Store(false)
	s.acceptor = shared.NewAcceptor(s.opts.MaxConnections, s.opts.AcceptRate)
	s.pool = newWorkerPool(s.opts.Handler, s.opts.WorkerCount, s.opts.TaskQueueSize, s.opts.FullPolicy)
	s.pool.start(s.opts.WorkerCount)
	ln, err := net.Listen("tcp", s.opts.Addr)
	if err != nil {
		s.pool.stop()
		s.started.Store(false)
		return fmt.Errorf("tcp listen %s: %w", s.opts.Addr, err)
	}
	if s.opts.TLSConfig != nil {
		ln = tls.NewListener(ln, s.opts.TLSConfig)
	}
	s.listener = ln

	s.acceptWG.Add(1)
	go s.acceptLoop(ctx)
	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	_ = s.StopAccept(ctx)
	if err := s.CloseSessions(ctx); err != nil {
		return err
	}
	return s.Drain(ctx)
}

func (s *Server) StopAccept(context.Context) error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	s.started.Store(false)
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

func (s *Server) Drain(ctx context.Context) error {
	// Wait for acceptLoop to finish. The drain goroutine is fire-and-forget:
	// StopAccept already closed the listener, so acceptLoop will exit promptly
	// and the goroutine completes in bounded time.
	done := make(chan struct{})
	go func() {
		s.acceptWG.Wait()
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
	var firstErr error
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*session)
		if err := sess.Close(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
		return true
	})
	done := make(chan struct{})
	go func() {
		s.connWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
	if s.pool != nil {
		s.pool.stop()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return firstErr
}

func (s *Server) Addr() net.Addr {
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

func (s *Server) acceptLoop(ctx context.Context) {
	defer s.acceptWG.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if s.closed.Load() || ctx.Err() != nil {
				return
			}
			if s.rt != nil {
				s.rt.Logger().Warn("tcp accept failed", "error", err)
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if s.acceptor != nil && !s.acceptor.TryAccept() {
			conn.Close()
			continue
		}
		s.connWG.Add(1)
		go func() {
			defer s.connWG.Done()
			defer s.acceptor.Done()
			s.handleConn(conn)
		}()
	}
}

func (s *Server) handleConn(conn net.Conn) {
	id := s.rt.Sessions().NextID()
	sess := newSession(id, conn, s.opts.Framer, s.opts.WriteQueue, s.opts.WriteTimeout, s.opts.WriteQueueHighWater)
	s.sessions.Store(id, sess)
	defer func() {
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

	s.connWG.Add(1)
	go func() {
		defer s.connWG.Done()
		sess.writeLoop()
	}()
	sess.readLoop(func(payload []byte) {
		payload, err := s.rt.Plugins().OnMessage(sess, payload)
		if err != nil {
			if err == core.ErrPluginDrop {
				return
			}
			_ = sess.Close(context.Background())
			return
		}
		if s.pool != nil {
			if err := s.pool.submit(sess, payload); err != nil && err == core.ErrWriteQueueFull {
				s.rt.Metrics().IncCounter("tcp_task_queue_full_total")
			}
		}
	})
}

var (
	_ core.Server              = (*Server)(nil)
	_ core.RuntimeConfigurable = (*Server)(nil)
	_ core.StagedServer        = (*Server)(nil)
)
