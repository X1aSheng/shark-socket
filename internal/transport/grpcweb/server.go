package grpcweb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/X1aSheng/shark-socket-new/internal/core"
	"github.com/X1aSheng/shark-socket-new/internal/runtime"
	"github.com/gorilla/websocket"
)

type Server struct {
	opts     Options
	rt       core.Runtime
	listener net.Listener
	server   *http.Server
	closed   atomic.Bool
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
	if s.rt == nil {
		s.rt = runtime.NewRuntime(nil, nil)
	}
	mux := http.NewServeMux()
	mux.HandleFunc(s.opts.Path, s.handle)
	if s.opts.WebSocket {
		mux.HandleFunc(s.opts.WebSocketPath, s.handleWebSocket)
	}
	s.server = &http.Server{
		Addr:         s.opts.Addr,
		Handler:      mux,
		ReadTimeout:  s.opts.ReadTimeout,
		WriteTimeout: s.opts.WriteTimeout,
	}
	ln, err := net.Listen("tcp", s.opts.Addr)
	if err != nil {
		return fmt.Errorf("grpc-web listen %s: %w", s.opts.Addr, err)
	}
	s.listener = ln
	go func() {
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
	if s.server != nil {
		return s.server.Shutdown(ctx)
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
			_ = sess.SendTrailers(13, err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	_ = sess.SendTrailers(0, "")
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{CheckOrigin: s.opts.CheckOrigin}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
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
	s.sessions.Delete(id)
	s.rt.Sessions().Unregister(id)
	_ = sess.Close(ctx)
	s.rt.Plugins().OnClose(sess)
}

var (
	_ core.Server              = (*Server)(nil)
	_ core.RuntimeConfigurable = (*Server)(nil)
	_ core.StagedServer        = (*Server)(nil)
)
