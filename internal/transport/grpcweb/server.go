package grpcweb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"

	"github.com/X1aSheng/shark-socket-new/internal/core"
	"github.com/X1aSheng/shark-socket-new/internal/runtime"
)

type Server struct {
	opts     Options
	rt       core.Runtime
	listener net.Listener
	server   *http.Server
	closed   atomic.Bool
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

func (s *Server) Drain(context.Context) error         { return nil }
func (s *Server) CloseSessions(context.Context) error { return nil }

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
	id := s.rt.Sessions().NextID()
	sess := newSession(id, w, r)
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
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
}

var (
	_ core.Server              = (*Server)(nil)
	_ core.RuntimeConfigurable = (*Server)(nil)
	_ core.StagedServer        = (*Server)(nil)
)
