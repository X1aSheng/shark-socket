package websocket

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/X1aSheng/shark-socket/internal/transport/shared"
	"github.com/gorilla/websocket"
)

type Server struct {
	opts     Options
	rt       core.Runtime
	listener net.Listener
	server   *http.Server
	acceptor *shared.Acceptor
	closed   atomic.Bool
	started  atomic.Bool
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

func (s *Server) Protocol() core.Protocol { return core.ProtocolWS }

func (s *Server) UseRuntime(rt core.Runtime) {
	s.rt = rt
}

func (s *Server) Start(context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return fmt.Errorf("websocket server already started")
	}
	s.closed.Store(false)
	if s.rt == nil {
		s.rt = runtime.NewRuntime(nil, nil)
	}
	s.acceptor = shared.NewAcceptor(s.opts.MaxConnections, s.opts.AcceptRate, s.opts.MaxConnectionsPerIP)
	mux := http.NewServeMux()
	mux.HandleFunc(s.opts.Path, s.handleUpgrade)
	var ln net.Listener
	s.server = &http.Server{
		Addr:         s.opts.Addr,
		Handler:      mux,
		ReadTimeout:  s.opts.ReadTimeout,
		WriteTimeout: s.opts.WriteTimeout,
		IdleTimeout:  s.opts.IdleTimeout,
	}
	// One listener for both the TLS and the plain case: tls.Listen would create
	// its own socket and drop the ListenOptions (SO_REUSEPORT, keepalive) on
	// the floor, so the options are applied first and TLS wraps the result.
	// TLSConfig is still not set on http.Server, which would negotiate HTTP/2
	// and conflict with the WebSocket upgrade.
	ln, err := shared.Listen("tcp", s.opts.Addr, s.opts.Listen)
	if err != nil {
		s.started.Store(false)
		return fmt.Errorf("websocket listen %s: %w", s.opts.Addr, err)
	}
	// PROXY before TLS: the balancer writes plaintext bytes, so the header
	// cannot be inside the encrypted stream.
	if s.opts.Proxy.Mode != shared.ProxyProtocolOff {
		ln = shared.NewProxyListener(ln, s.opts.Proxy.WithRejectHook(s.rt.Logger(), s.rt.Metrics()))
	}
	if s.opts.TLSConfig != nil {
		ln = tls.NewListener(ln, s.opts.TLSConfig)
	}
	s.listener = ln
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.rt.Logger().Error("websocket serve failed", "error", err)
		}
	}()
	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	_ = s.StopAccept(ctx)
	_ = s.Drain(ctx)
	return s.CloseSessions(ctx)
}

func (s *Server) StopAccept(ctx context.Context) error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	s.started.Store(false)
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

func (s *Server) Drain(ctx context.Context) error {
	// Drain is a no-op for WebSocket: http.Server.Shutdown in StopAccept
	// already waits for the Serve loop, and CloseSessions handles connections.
	return nil
}

func (s *Server) CloseSessions(ctx context.Context) error {
	s.sessions.Range(func(key, value any) bool {
		s.closeSession(ctx, key.(uint64), value.(*session))
		return true
	})
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

func (s *Server) Addr() net.Addr {
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

func (s *Server) handleUpgrade(w http.ResponseWriter, r *http.Request) {
	peer := shared.PeerIPString(r.RemoteAddr)
	if s.acceptor != nil && s.acceptor.TryAccept(peer) != shared.AcceptOK {
		http.Error(w, "server busy", http.StatusServiceUnavailable)
		return
	}
	upgrader := websocket.Upgrader{
		CheckOrigin:      s.opts.CheckOrigin,
		HandshakeTimeout: s.opts.HandshakeTimeout,
		ReadBufferSize:   s.opts.ReadBufferSize,
		WriteBufferSize:  s.opts.WriteBufferSize,
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.acceptor.Done(peer)
		return
	}
	if s.opts.MaxMessageSize > 0 {
		conn.SetReadLimit(s.opts.MaxMessageSize)
	}
	// Dead-peer detection: a pong extends the read deadline; a peer that stops
	// answering pings is closed after PongTimeout (RFC 6455 requires pong
	// responses, so a compliant client always answers).
	if s.opts.PongTimeout > 0 {
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(s.opts.PongTimeout))
		})
	}
	id := s.rt.Sessions().NextID()
	sess := newSession(id, conn, s.opts.WriteTimeout)
	s.sessions.Store(id, sess)
	if err := s.rt.Sessions().Register(sess); err != nil {
		s.acceptor.Done(peer)
		// Never accepted: drop without OnClose (the plugin chain never ran).
		if _, loaded := s.sessions.LoadAndDelete(id); loaded {
			s.rt.Sessions().Unregister(id)
			_ = sess.Close(context.Background())
		}
		return
	}
	if err := s.rt.Plugins().OnAccept(sess); err != nil {
		s.acceptor.Done(peer)
		// OnAccept failed: the plugin chain rolled back partial accepts; do not
		// call OnClose again here.
		if _, loaded := s.sessions.LoadAndDelete(id); loaded {
			s.rt.Sessions().Unregister(id)
			_ = sess.Close(context.Background())
		}
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.acceptor.Done(peer)
		s.readLoop(sess)
	}()
	if s.opts.PingInterval > 0 {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.pingLoop(sess)
		}()
	}
}

func (s *Server) readLoop(sess *session) {
	defer s.closeSession(context.Background(), sess.ID(), sess)
	for {
		if s.opts.PongTimeout > 0 {
			if err := sess.conn.SetReadDeadline(time.Now().Add(s.opts.PongTimeout)); err != nil {
				return
			}
		}
		_, payload, err := sess.conn.ReadMessage()
		if err != nil {
			// A read-deadline expiry means the peer stopped answering pings
			// (a vanished client): count the reclaim.
			if shared.IsTimeout(err) {
				s.rt.Metrics().IncCounter("sessions_reclaimed_total")
			}
			return
		}
		sess.touch()
		payload, err = s.rt.Plugins().OnMessage(sess, payload)
		if err != nil {
			if errors.Is(err, core.ErrPluginDrop) {
				continue
			}
			return
		}
		if s.opts.Handler != nil {
			msg := core.Message{SessionID: sess.ID(), Protocol: core.ProtocolWS, Payload: payload}
			if err := shared.CallHandler(func() error { return s.opts.Handler(sess, msg) }, s.rt.Logger()); err != nil {
				return
			}
		}
	}
}

func (s *Server) pingLoop(sess *session) {
	ticker := time.NewTicker(s.opts.PingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := sess.ping(); err != nil {
				id := sess.ID()
				s.closeSession(context.Background(), id, sess)
				return
			}
		case <-sess.Context().Done():
			return
		}
	}
}

func (s *Server) closeSession(ctx context.Context, id uint64, sess *session) {
	if _, loaded := s.sessions.LoadAndDelete(id); !loaded {
		return
	}
	s.rt.Sessions().Unregister(id)
	_ = sess.Close(ctx)
	s.rt.Plugins().OnClose(sess)
}

var (
	_ core.Server              = (*Server)(nil)
	_ core.RuntimeConfigurable = (*Server)(nil)
	_ core.StagedServer        = (*Server)(nil)
)
