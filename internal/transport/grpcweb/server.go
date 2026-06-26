package grpcweb

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"

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

func (s *Server) Protocol() core.Protocol { return core.ProtocolGRPCWeb }

func (s *Server) UseRuntime(rt core.Runtime) {
	s.rt = rt
}

func (s *Server) Start(context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return fmt.Errorf("grpc-web server already started")
	}
	s.closed.Store(false)
	if s.rt == nil {
		s.rt = runtime.NewRuntime(nil, nil)
	}
	s.acceptor = shared.NewAcceptor(s.opts.MaxConnections, s.opts.AcceptRate)
	mux := http.NewServeMux()
	mux.HandleFunc(s.opts.Path, s.handle)
	if s.opts.WebSocket {
		mux.HandleFunc(s.opts.WebSocketPath, s.handleWebSocket)
	}
	var ln net.Listener
	var err error
	if s.opts.TLSConfig != nil {
		// Use tls.Listen for TLS; do NOT set TLSConfig on http.Server
		// to avoid HTTP/2 negotiation that conflicts with WebSocket/gRPC-Web.
		s.server = &http.Server{
			Addr:         s.opts.Addr,
			Handler:      mux,
			ReadTimeout:  s.opts.ReadTimeout,
			WriteTimeout: s.opts.WriteTimeout,
			IdleTimeout:  s.opts.IdleTimeout,
		}
		ln, err = tls.Listen("tcp", s.opts.Addr, s.opts.TLSConfig)
	} else {
		s.server = &http.Server{
			Addr:         s.opts.Addr,
			Handler:      mux,
			ReadTimeout:  s.opts.ReadTimeout,
			WriteTimeout: s.opts.WriteTimeout,
			IdleTimeout:  s.opts.IdleTimeout,
		}
		ln, err = net.Listen("tcp", s.opts.Addr)
	}
	if err != nil {
		s.started.Store(false)
		return fmt.Errorf("grpc-web listen %s: %w", s.opts.Addr, err)
	}
	s.listener = ln
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.rt.Logger().Error("grpc-web serve failed", "error", err)
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
	// Wait for WebSocket read loops to finish. The drain goroutine is
	// fire-and-forget: StopAccept already called http.Server.Shutdown,
	// so all tracked goroutines exit promptly.
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
		s.closeWebSocketSession(ctx, key.(uint64), value.(*webSocketSession))
		return true
	})
	return nil
}

func (s *Server) Addr() net.Addr {
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if s.acceptor != nil && !s.acceptor.TryAccept() {
		http.Error(w, "server busy", http.StatusServiceUnavailable)
		return
	}
	defer s.acceptor.Done()
	if s.opts.MaxMessageBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, s.opts.MaxMessageBytes)
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
		return
	}
	strictFraming := r.Header.Get("x-grpc-web") == "1"
	body, framed, err := parseRequestPayload(body, strictFraming)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	framed = framed || isGRPCWebRequest(r) && strictFraming
	id := s.rt.Sessions().NextID()
	sess := newSession(id, w, r, framed)
	if err := s.rt.Sessions().Register(sess); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer func() {
		s.rt.Sessions().Unregister(id)
		_ = sess.Close(context.Background())
		s.rt.Plugins().OnClose(sess)
	}()
	if err := s.rt.Plugins().OnAccept(sess); err != nil {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	body, err = s.rt.Plugins().OnMessage(sess, body)
	if err != nil {
		if err == core.ErrPluginDrop {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if s.opts.Handler != nil {
		msg := core.Message{SessionID: id, Protocol: core.ProtocolGRPCWeb, Payload: body}
		if err := s.opts.Handler(sess, msg); err != nil {
			if s.rt != nil {
				s.rt.Logger().Error("grpc-web handler error", "session", id, "error", err)
			}
			_ = sess.SendTrailers(13, err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := sess.SendTrailers(0, ""); err != nil && s.rt != nil {
		s.rt.Logger().Warn("grpc-web send trailers error", "session", id, "error", err)
	}
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if s.acceptor != nil && !s.acceptor.TryAccept() {
		http.Error(w, "server busy", http.StatusServiceUnavailable)
		return
	}
	upgrader := websocket.Upgrader{CheckOrigin: s.opts.CheckOrigin}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.acceptor.Done()
		return
	}
	if s.opts.MaxMessageBytes > 0 {
		conn.SetReadLimit(s.opts.MaxMessageBytes)
	}
	id := s.rt.Sessions().NextID()
	sess := newWebSocketSession(id, conn)
	s.sessions.Store(id, sess)
	if err := s.rt.Sessions().Register(sess); err != nil {
		s.closeWebSocketSession(context.Background(), id, sess)
		return
	}
	if err := s.rt.Plugins().OnAccept(sess); err != nil {
		s.closeWebSocketSession(context.Background(), id, sess)
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.readWebSocketLoop(sess)
	}()
}

func (s *Server) readWebSocketLoop(sess *webSocketSession) {
	defer s.closeWebSocketSession(context.Background(), sess.ID(), sess)
	for {
		_, payload, err := sess.conn.ReadMessage()
		if err != nil {
			return
		}
		sess.touch()
		payload, err = s.rt.Plugins().OnMessage(sess, payload)
		if err != nil {
			if err == core.ErrPluginDrop {
				continue
			}
			return
		}
		if s.opts.Handler != nil {
			msg := core.Message{SessionID: sess.ID(), Protocol: core.ProtocolGRPCWeb, Payload: payload}
			if err := s.opts.Handler(sess, msg); err != nil {
				return
			}
		}
	}
}

func (s *Server) closeWebSocketSession(ctx context.Context, id uint64, sess *webSocketSession) {
	if _, loaded := s.sessions.LoadAndDelete(id); !loaded {
		return
	}
	s.rt.Sessions().Unregister(id)
	_ = sess.Close(ctx)
	s.rt.Plugins().OnClose(sess)
	if s.acceptor != nil {
		s.acceptor.Done()
	}
}

var (
	_ core.Server              = (*Server)(nil)
	_ core.RuntimeConfigurable = (*Server)(nil)
	_ core.StagedServer        = (*Server)(nil)
)
