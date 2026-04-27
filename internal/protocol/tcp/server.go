package tcp

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket/internal/infra/tracing"
	"github.com/X1aSheng/shark-socket/internal/plugin"
	"github.com/X1aSheng/shark-socket/internal/session"
	"github.com/X1aSheng/shark-socket/internal/types"
)

// Server is a TCP protocol server.
type Server struct {
	opts        Options
	listener    net.Listener
	manager     *session.Manager
	chain       *plugin.Chain
	pool        *WorkerPool
	wg          sync.WaitGroup
	closed      atomic.Bool
	handler     types.RawHandler
	tlsReloader *TLSReloader
}

// Compile-time verification.
var _ types.Server = (*Server)(nil)

// NewServer creates a new TCP server.
func NewServer(handler types.RawHandler, opts ...Option) *Server {
	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}
	return &Server{
		opts:    o,
		handler: handler,
	}
}

// Start begins listening and accepting connections.
func (s *Server) Start() error {
	if err := s.opts.validate(); err != nil {
		return err
	}

	var ln net.Listener
	var err error

	// If cert/key files are provided, use TLSReloader for hot reload
	if s.opts.CertFile != "" && s.opts.KeyFile != "" {
		reloader, rErr := NewTLSReloader(s.opts.CertFile, s.opts.KeyFile)
		if rErr != nil {
			return fmt.Errorf("tcp: load TLS cert: %w", rErr)
		}
		s.tlsReloader = reloader
		ln, err = tls.Listen("tcp", s.opts.Addr(), reloader.TLSConfig())
	} else if s.opts.TLSConfig != nil {
		ln, err = tls.Listen("tcp", s.opts.Addr(), s.opts.TLSConfig)
	} else {
		ln, err = net.Listen("tcp", s.opts.Addr())
	}
	if err != nil {
		return fmt.Errorf("tcp: listen on %s: %w", s.opts.Addr(), err)
	}
	s.listener = ln

	s.manager = session.NewManager(session.WithMaxSessions(s.opts.MaxSessions))

	if len(s.opts.Plugins) > 0 {
		s.chain = plugin.NewChain(s.opts.Plugins...)
	}

	s.pool = NewWorkerPool(
		s.handler,
		s.chain,
		s.opts.WorkerCount,
		s.opts.TaskQueueSize,
		s.opts.MaxWorkers,
		s.opts.FullPolicy,
	)
	s.pool.Start()

	s.wg.Add(1)
	go s.acceptLoop()

	log.Printf("TCP server listening on %s", s.opts.Addr())
	return nil
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()

	var consecutiveErrors int
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if s.closed.Load() {
				return
			}
			consecutiveErrors++
			if consecutiveErrors > s.opts.MaxConsecutiveErrors {
				log.Printf("TCP accept: too many errors (%d), stopping", consecutiveErrors)
				return
			}
			time.Sleep(acceptBackoff(consecutiveErrors))
			continue
		}
		consecutiveErrors = 0

		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			s.handleConn(c)
		}(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	var span tracing.Span
	if s.opts.Tracer != nil {
		span, _ = s.opts.Tracer.StartSpan(context.Background(), "tcp.accept",
			tracing.WithAttribute("protocol", "tcp"),
			tracing.WithAttribute("remote_addr", conn.RemoteAddr().String()),
		)
	}

	// Check connection rate limit if configured
	if s.opts.ConnRateLimit != nil {
		if !s.opts.ConnRateLimit.AllowAddr(conn.RemoteAddr()) {
			if span != nil {
				span.End()
			}
			conn.Close()
			return
		}
	}

	id := s.manager.NextID()
	sess := NewTCPSession(id, conn, s.opts.Framer, s.opts.WriteQueueSize, s.opts.MaxMessageSize)
	if s.opts.DrainTimeout > 0 {
		sess.SetDrainTimeout(time.Duration(s.opts.DrainTimeout) * time.Second)
	}

	if err := s.manager.Register(sess); err != nil {
		if span != nil {
			span.End()
		}
		conn.Close()
		return
	}

	if s.chain != nil {
		if err := s.chain.OnAccept(sess); err != nil {
			s.manager.Unregister(id)
			sess.Close()
			if span != nil {
				span.End()
			}
			return
		}
	}

	s.wg.Add(2)
	go func() {
		defer s.wg.Done()
		sess.ReadLoop(s.pool, nil)
	}()
	go func() {
		defer s.wg.Done()
		sess.WriteLoop()
	}()

	// Cleanup on session exit
	<-sess.Context().Done()

	// Remove from rate limiter when connection closes
	if s.opts.ConnRateLimit != nil {
		s.opts.ConnRateLimit.RemoveAddr(conn.RemoteAddr())
	}

	s.manager.Unregister(id)
	if s.chain != nil {
		s.chain.OnClose(sess)
	}
	if span != nil {
		span.End()
	}
}

// Stop gracefully shuts down the server.
func (s *Server) Stop(ctx context.Context) error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}

	if s.listener != nil {
		s.listener.Close()
	}
	if s.pool != nil {
		s.pool.Stop()
	}
	if s.manager != nil {
		s.manager.Close()
	}
	if s.tlsReloader != nil {
		s.tlsReloader.Close()
	}

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

// Protocol returns the protocol type.
func (s *Server) Protocol() types.ProtocolType {
	if s.opts.TLSConfig != nil || s.opts.CertFile != "" {
		return types.TLS
	}
	return types.TCP
}

// Addr returns the listener address. Only valid after Start().
func (s *Server) Addr() net.Addr {
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// Manager returns the session manager.
func (s *Server) Manager() *session.Manager {
	return s.manager
}

func acceptBackoff(errors int) time.Duration {
	d := time.Duration(5<<min(errors-1, 10)) * time.Millisecond
	if d > 10*time.Second {
		d = 10 * time.Second
	}
	return d
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
