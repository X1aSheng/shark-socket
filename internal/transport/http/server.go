package http

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	stdhttp "net/http"
	"strings"
	"sync/atomic"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
)

type Server struct {
	opts     Options
	rt       core.Runtime
	listener net.Listener
	server   *stdhttp.Server
	closed   atomic.Bool
}

func NewServer(opts ...Option) *Server {
	cfg := defaultOptions()
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Server{opts: cfg}
}

func (s *Server) Protocol() core.Protocol { return core.ProtocolHTTP }

func (s *Server) UseRuntime(rt core.Runtime) {
	s.rt = rt
}

func (s *Server) Handle(pattern string, handler stdhttp.Handler) {
	s.opts.Mux.Handle(pattern, handler)
}

func (s *Server) HandleFunc(pattern string, handler stdhttp.HandlerFunc) {
	s.opts.Mux.HandleFunc(pattern, handler)
}

func (s *Server) Start(context.Context) error {
	if s.rt == nil {
		s.rt = runtime.NewRuntime(nil, nil)
	}
	handler := stdhttp.Handler(s.opts.Mux)
	if s.opts.Handler != nil {
		handler = stdhttp.HandlerFunc(s.handleWithSession)
	}
	if len(s.opts.CORSOrigins) > 0 {
		handler = corsHandler(handler, s.opts.CORSOrigins)
	}
	s.server = &stdhttp.Server{
		Addr:         s.opts.Addr,
		Handler:      handler,
		ReadTimeout:  s.opts.ReadTimeout,
		WriteTimeout: s.opts.WriteTimeout,
		IdleTimeout:  s.opts.IdleTimeout,
	}
	ln, err := net.Listen("tcp", s.opts.Addr)
	if err != nil {
		return fmt.Errorf("http listen %s: %w", s.opts.Addr, err)
	}
	s.listener = ln
	go func() {
		if err := s.server.Serve(ln); err != nil && !errors.Is(err, stdhttp.ErrServerClosed) {
			s.rt.Logger().Error("http serve failed", "error", err)
		}
	}()
	return nil
}

func corsHandler(next stdhttp.Handler, origins []string) stdhttp.Handler {
	allowed := make(map[string]struct{}, len(origins))
	allowAll := false
	for _, origin := range origins {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			continue
		}
		if origin == "*" {
			allowAll = true
			continue
		}
		allowed[origin] = struct{}{}
	}
	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			if allowAll {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else if _, ok := allowed[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
			}
			if w.Header().Get("Access-Control-Allow-Origin") != "" {
				w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization")
			}
		}
		if r.Method == stdhttp.MethodOptions && w.Header().Get("Access-Control-Allow-Origin") != "" {
			w.WriteHeader(stdhttp.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
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

func (s *Server) Drain(context.Context) error {
	return nil
}

func (s *Server) CloseSessions(context.Context) error {
	return nil
}

func (s *Server) Addr() net.Addr {
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

func (s *Server) handleWithSession(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if s.opts.MaxBodyBytes > 0 {
		r.Body = stdhttp.MaxBytesReader(w, r.Body, s.opts.MaxBodyBytes)
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		stdhttp.Error(w, stdhttp.StatusText(stdhttp.StatusRequestEntityTooLarge), stdhttp.StatusRequestEntityTooLarge)
		return
	}
	id := s.rt.Sessions().NextID()
	recorder := &responseRecorder{ResponseWriter: w, status: stdhttp.StatusOK}
	sess := newSession(id, recorder, r)
	if err := s.rt.Sessions().Register(sess); err != nil {
		stdhttp.Error(w, err.Error(), stdhttp.StatusServiceUnavailable)
		return
	}
	defer func() {
		s.rt.Sessions().Unregister(sess.ID())
		_ = sess.Close(context.Background())
		s.rt.Plugins().OnClose(sess)
	}()
	if err := s.rt.Plugins().OnAccept(sess); err != nil {
		stdhttp.Error(w, stdhttp.StatusText(stdhttp.StatusForbidden), stdhttp.StatusForbidden)
		return
	}
	body, err = s.rt.Plugins().OnMessage(sess, body)
	if err != nil {
		if err == core.ErrPluginDrop {
			w.WriteHeader(stdhttp.StatusNoContent)
			return
		}
		stdhttp.Error(w, err.Error(), stdhttp.StatusBadRequest)
		return
	}
	msg := core.Message{SessionID: sess.ID(), Protocol: core.ProtocolHTTP, Payload: body}
	if err := s.opts.Handler(sess, msg); err != nil {
		stdhttp.Error(w, err.Error(), stdhttp.StatusInternalServerError)
		return
	}
}

var (
	_ core.Server              = (*Server)(nil)
	_ core.RuntimeConfigurable = (*Server)(nil)
	_ core.StagedServer        = (*Server)(nil)
)
