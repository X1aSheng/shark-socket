package http

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	stdhttp "net/http"
	"strings"
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
	server   *stdhttp.Server
	acceptor *shared.Acceptor
	closed   atomic.Bool
	started  atomic.Bool
	wg       sync.WaitGroup
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
	if !s.started.CompareAndSwap(false, true) {
		return fmt.Errorf("http server already started")
	}
	s.closed.Store(false)
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
	ln, err := shared.Listen("tcp", s.opts.Addr, s.opts.Listen)
	if err != nil {
		s.started.Store(false)
		return fmt.Errorf("http listen %s: %w", s.opts.Addr, err)
	}
	if s.opts.Proxy.Mode != shared.ProxyProtocolOff {
		ln = shared.NewProxyListener(ln, s.opts.Proxy.WithRejectHook(s.rt.Logger(), s.rt.Metrics()))
	}
	// The gate goes above the PROXY listener, so the peer it counts is the
	// client behind the balancer rather than the balancer: the header has
	// already been read by the time Accept returns.
	//
	// This transport is plaintext-only, which is what lets a refusal be a real
	// HTTP 503 rather than a closed socket.
	if s.opts.MaxConnections > 0 || s.opts.MaxConnectionsPerIP > 0 || s.opts.AcceptRate > 0 {
		s.acceptor = shared.NewAcceptor(s.opts.MaxConnections, s.opts.AcceptRate, s.opts.MaxConnectionsPerIP)
		ln = shared.NewGateListener(ln, s.acceptor, shared.GateOptions{
			Reject: rejectWithServiceUnavailable,
			OnReject: func(shared.RejectReason) {
				s.rt.Metrics().IncCounter("sessions_rejected_total")
			},
		})
	}
	s.listener = ln
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
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
	s.started.Store(false)
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
	// OnClose fires only for sessions that were actually accepted; on OnAccept
	// failure the plugin chain already rolled back partial accepts.
	accepted := false
	defer func() {
		s.rt.Sessions().Unregister(sess.ID())
		_ = sess.Close(context.Background())
		if accepted {
			s.rt.Plugins().OnClose(sess)
		}
	}()
	if err := s.rt.Plugins().OnAccept(sess); err != nil {
		stdhttp.Error(w, stdhttp.StatusText(stdhttp.StatusForbidden), stdhttp.StatusForbidden)
		return
	}
	accepted = true
	body, err = s.rt.Plugins().OnMessage(sess, body)
	if err != nil {
		if errors.Is(err, core.ErrPluginDrop) {
			w.WriteHeader(stdhttp.StatusNoContent)
			return
		}
		// Never leak internal/plugin error details to the client: log them
		// and answer with a generic status text.
		s.rt.Logger().Warn("http plugin message error", "session", sess.ID(), "error", err)
		stdhttp.Error(w, stdhttp.StatusText(stdhttp.StatusBadRequest), stdhttp.StatusBadRequest)
		return
	}
	msg := core.Message{SessionID: sess.ID(), Protocol: core.ProtocolHTTP, Payload: body}
	// Isolate handler panics like every other transport: a panicking handler
	// must not propagate out of the request goroutine (net/http would then
	// abort the connection with no controlled response).
	if err := shared.CallHandler(func() error { return s.opts.Handler(sess, msg) }, s.rt.Logger()); err != nil {
		s.rt.Logger().Error("http handler error", "session", sess.ID(), "error", err)
		stdhttp.Error(w, stdhttp.StatusText(stdhttp.StatusInternalServerError), stdhttp.StatusInternalServerError)
		return
	}
}

var (
	_ core.Server              = (*Server)(nil)
	_ core.RuntimeConfigurable = (*Server)(nil)
	_ core.StagedServer        = (*Server)(nil)
)

// rejectWithServiceUnavailable answers a refused connection before closing it.
//
// A gate listener sits below the HTTP layer, so this writes the status line by
// hand; there is no ResponseWriter in sight.
//
// The request the peer already sent is drained first, briefly. Closing a socket
// that still has unread data earns an RST (RFC 1122), and an RST makes the
// client discard the response it just received - it then reports a connection
// reset instead of "service unavailable", which is strictly less useful. The
// drain is bounded by both a 5ms deadline and eight reads: a peer that sends
// nothing must not turn the accept path into the bottleneck.
func rejectWithServiceUnavailable(conn net.Conn, _ shared.RejectReason) {
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Millisecond))
	var scratch [512]byte
	for i := 0; i < 8; i++ {
		if _, err := conn.Read(scratch[:]); err != nil {
			break
		}
	}
	_ = conn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
	_, _ = io.WriteString(conn, "HTTP/1.1 503 Service Unavailable\r\nConnection: close\r\nContent-Length: 0\r\n\r\n")
	// Half-close so the response is flushed before the socket is torn down.
	if cw, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
}
