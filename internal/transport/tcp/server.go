package tcp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/X1aSheng/shark-socket/internal/transport/shared"
)

// taskPool is the dispatch surface the TCP server needs from a worker pool.
//
// It exists because there are two implementations with different sizing
// policies: WorkerPool (fixed) and shared.ScalablePool (elastic). Declaring the
// two methods the server actually calls keeps the choice between them in
// Start, instead of duplicating the pool field or type-switching at each of the
// three call sites.
type taskPool interface {
	Submit(sess core.Session, protocol core.Protocol, payload []byte) error
	Stop()
	WorkerCount() int
	WorkerBounds() (min, max int)
}

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
	pool     taskPool
	// elastic is the running elastic pool, kept only so tests and operators can
	// read its live worker count. Nil when the fixed pool is in use.
	elastic *shared.ScalablePool
}

func NewServer(opts ...Option) *Server {
	cfg := defaultOptions()
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Server{opts: cfg}
}

func (s *Server) Protocol() core.Protocol { return core.ProtocolTCP }

// WorkerCount reports how many workers are currently dispatching tasks.
//
// Returns 0 before Start, when no pool exists yet. Exposed because the elastic
// pool's worker count moves at run time, and a load test cannot tell scaling
// apart from a fixed pool without being able to read it.
func (s *Server) WorkerCount() int {
	if s.pool == nil {
		return 0
	}
	return s.pool.WorkerCount()
}

// WorkerBounds returns the pool's configured floor and ceiling.
func (s *Server) WorkerBounds() (min, max int) {
	if s.pool == nil {
		return 0, 0
	}
	return s.pool.WorkerBounds()
}

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
	s.acceptor = shared.NewAcceptor(s.opts.MaxConnections, s.opts.AcceptRate, s.opts.MaxConnectionsPerIP)
	// Listen before creating the pool. The pool used to be built and started
	// first, which meant a failed listen had to stop it again -- and if Start
	// were ever retried, the previous pool was overwritten while its workers
	// kept running with no handle left to stop them. Creating the pool only
	// once the listener exists removes that failure mode instead of cleaning up
	// after it.
	ln, err := shared.Listen("tcp", s.opts.Addr, s.opts.Listen)
	if err != nil {
		s.started.Store(false)
		return fmt.Errorf("tcp listen %s: %w", s.opts.Addr, err)
	}
	// The PROXY header sits outside TLS: the balancer speaks plaintext, and an
	// encrypted record cannot precede the handshake that would decrypt it.
	if s.opts.Proxy.Mode != shared.ProxyProtocolOff {
		ln = shared.NewProxyListener(ln, s.opts.Proxy.WithRejectHook(s.rt.Logger(), s.rt.Metrics()))
	}
	if s.opts.TLSConfig != nil {
		ln = tls.NewListener(ln, s.opts.TLSConfig)
	}
	s.listener = ln
	s.pool, s.elastic = s.buildPool()

	s.acceptWG.Add(1)
	go s.acceptLoop(ctx)
	return nil
}

// buildPool picks the worker pool implementation from the options.
func (s *Server) buildPool() (taskPool, *shared.ScalablePool) {
	logger := s.rt.Logger()
	cfg := s.opts.ElasticPool
	if cfg == nil {
		p := shared.NewWorkerPool(s.opts.Handler, logger, s.opts.WorkerCount, s.opts.TaskQueueSize, s.opts.FullPolicy, shared.WithPoolMetrics(s.rt.Metrics()))
		p.Start(s.opts.WorkerCount)
		return p, nil
	}
	p := shared.NewScalablePool(s.opts.Handler, logger, cfg.MinWorker, cfg.MaxWorker, s.opts.TaskQueueSize, s.opts.FullPolicy, shared.WithPoolMetrics(s.rt.Metrics()))
	// Only override what the caller set: a zero value means "package default",
	// and passing it through would be rejected by the ratio validation.
	if cfg.ScaleUpRatio != 0 || cfg.ScaleDownRatio != 0 {
		up, down := cfg.ScaleUpRatio, cfg.ScaleDownRatio
		if up == 0 {
			up = shared.DefaultScaleUpRatio
		}
		if down == 0 {
			down = shared.DefaultScaleDownRatio
		}
		p.SetScaleRatio(up, down)
	}
	p.Start()
	return p, p
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
		s.pool.Stop()
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
	backoff := shared.NewBackoff(shared.DefaultBackoffMin, shared.DefaultBackoffMax)
	// reported collapses a failure streak into one log line. A descriptor
	// exhaustion lasts for as long as the process cannot free an fd, so logging
	// every retry turns one operational incident into a log flood; the counters
	// keep counting whether or not the line is printed.
	reported := false
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if s.closed.Load() || ctx.Err() != nil {
				return
			}
			kind := shared.ClassifyAcceptError(err)
			if kind == shared.AcceptErrorClosed {
				return
			}
			if s.rt != nil {
				s.rt.Metrics().IncCounter("accept_errors_total")
				if kind == shared.AcceptErrorResource {
					s.rt.Metrics().IncCounter("accept_resource_errors_total")
				}
			}
			if !reported {
				if s.rt != nil {
					s.rt.Logger().Warn("tcp accept failed", "error", err, "kind", kind.String())
				}
				reported = true
			}
			if !backoff.Wait(ctx) {
				return
			}
			continue
		}
		backoff.Reset()
		reported = false
		peer := shared.PeerIP(conn.RemoteAddr())
		if s.acceptor != nil {
			if reason := s.acceptor.TryAccept(peer); reason != shared.AcceptOK {
				if s.rt != nil {
					shared.IncProtocolCounter(s.rt.Metrics(), shared.MetricSessionsRejected, core.ProtocolTCP)
				}
				conn.Close()
				continue
			}
		}
		s.connWG.Add(1)
		go func() {
			defer s.connWG.Done()
			if s.acceptor != nil {
				defer s.acceptor.Done(peer)
			}
			s.handleConn(conn)
		}()
	}
}

// handshakeTLS completes the TLS handshake under its own deadline, before the
// session exists.
//
// tls.NewListener defers the handshake to the first Read, which means the
// expensive part of the connection setup was previously bounded only by
// ReadTimeout - five minutes by default, during which the peer occupied a
// connection slot and a goroutine while contributing nothing. Doing it here
// gives it a budget of its own and a place to count failures.
//
// With no TLSHandshakeTimeout configured the bound falls back to ReadTimeout,
// which is exactly what the old code enforced (the session's read deadline was
// already in place before the handshake data arrived). Falling back to "no
// bound at all" would have been a regression hiding inside a new feature.
//
// A non-TLS connection is returned unchanged, so the plaintext path pays only a
// failed type assertion.
func (s *Server) handshakeTLS(conn net.Conn) (net.Conn, bool) {
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		return conn, true
	}
	timeout := s.opts.TLSHandshakeTimeout
	if timeout <= 0 {
		timeout = s.opts.ReadTimeout
	}
	if timeout > 0 {
		_ = tlsConn.SetDeadline(time.Now().Add(timeout))
	}
	// The deadline, not a context timeout, is what enforces the budget.
	// HandshakeContext with a deadline context closes the socket from a
	// context callback goroutine, so the peer can observe the disconnect
	// before the failure is counted and logged - the accounting then depends
	// on goroutine scheduling. SetDeadline fails the read synchronously, so
	// the counter and the log always precede the close.
	err := tlsConn.HandshakeContext(context.Background())
	if err != nil {
		if s.rt != nil {
			s.rt.Metrics().IncCounter("tls_handshake_failed_total")
			s.rt.Logger().Debug("tls handshake failed", "remote", conn.RemoteAddr(), "error", err)
		}
		_ = conn.Close()
		return nil, false
	}
	// The session's own read and write deadlines must start from now, not from
	// the handshake: leaving the handshake deadline in place would close the
	// connection on schedule even though it is healthy.
	_ = tlsConn.SetDeadline(time.Time{})
	return conn, true
}

func (s *Server) handleConn(conn net.Conn) {
	conn, ok := s.handshakeTLS(conn)
	if !ok {
		return
	}
	id := s.rt.Sessions().NextID()
	sess := newSession(id, conn, sessionConfig{
		framer:              s.opts.Framer,
		writeQueue:          s.opts.WriteQueue,
		writeTimeout:        s.opts.WriteTimeout,
		writeQueueHighWater: s.opts.WriteQueueHighWater,
		readBufferBytes:     s.opts.ReadBufferBytes,
		writeBufferBytes:    s.opts.WriteBufferBytes,
	})
	sess.readTimeout = s.opts.ReadTimeout
	sess.onIdleClose = func() {
		shared.IncProtocolCounter(s.rt.Metrics(), shared.MetricSessionsReclaimed, core.ProtocolTCP)
	}
	s.sessions.Store(id, sess)
	// OnClose must fire only for sessions that were actually accepted; when
	// Register or OnAccept fails the plugin chain already rolled back partial
	// accepts, so calling OnClose again would double-notify plugins.
	accepted := false
	defer func() {
		s.sessions.Delete(id)
		s.rt.Sessions().Unregister(id)
		if accepted {
			s.rt.Plugins().OnClose(sess)
		}
	}()

	if err := s.rt.Sessions().Register(sess); err != nil {
		_ = sess.Close(context.Background())
		return
	}
	if err := s.rt.Plugins().OnAccept(sess); err != nil {
		_ = sess.Close(context.Background())
		return
	}
	accepted = true

	s.connWG.Add(1)
	go func() {
		defer s.connWG.Done()
		sess.writeLoop()
	}()
	sess.readLoop(func(payload []byte) {
		payload, err := s.rt.Plugins().OnMessage(sess, payload)
		if err != nil {
			if errors.Is(err, core.ErrPluginDrop) {
				return
			}
			_ = sess.Close(context.Background())
			return
		}
		if s.pool != nil {
			if err := s.pool.Submit(sess, core.ProtocolTCP, payload); err != nil && errors.Is(err, core.ErrWriteQueueFull) {
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
