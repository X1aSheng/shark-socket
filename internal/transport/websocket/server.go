package websocket

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

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

func (s *Server) Protocol() core.Protocol { return core.ProtocolWS }

func (s *Server) UseRuntime(rt core.Runtime) {
	s.rt = rt
}

func (s *Server) Start(context.Context) error {
	if s.rt == nil {
		s.rt = runtime.NewRuntime(nil, nil)
	}
	mux := http.NewServeMux()
	mux.HandleFunc(s.opts.Path, s.handleUpgrade)
	s.server = &http.Server{Addr: s.opts.Addr, Handler: mux}
	ln, err := net.Listen("tcp", s.opts.Addr)
	if err != nil {
		return fmt.Errorf("websocket listen %s: %w", s.opts.Addr, err)
	}
	s.listener = ln
	go func() {
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
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

func (s *Server) Drain(ctx context.Context) error {
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
	upgrader := websocket.Upgrader{CheckOrigin: s.opts.CheckOrigin}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	if s.opts.MaxMessageSize > 0 {
		conn.SetReadLimit(s.opts.MaxMessageSize)
	}
	id := s.rt.Sessions().NextID()
	sess := newSession(id, conn)
	s.sessions.Store(id, sess)
	if err := s.rt.Sessions().Register(sess); err != nil {
		s.closeSession(context.Background(), id, sess)
		return
	}
	if err := s.rt.Plugins().OnAccept(sess); err != nil {
		s.closeSession(context.Background(), id, sess)
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
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
			msg := core.Message{SessionID: sess.ID(), Protocol: core.ProtocolWS, Payload: payload}
			if err := s.opts.Handler(sess, msg); err != nil {
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
				return
			}
		case <-sess.Context().Done():
			return
		}
	}
}

func (s *Server) closeSession(ctx context.Context, id uint64, sess *session) {
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
