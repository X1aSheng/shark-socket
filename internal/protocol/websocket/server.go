package websocket

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	stdhttp "net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket/internal/errs"
	"github.com/X1aSheng/shark-socket/internal/infra/logger"
	"github.com/X1aSheng/shark-socket/internal/plugin"
	"github.com/X1aSheng/shark-socket/internal/session"
	"github.com/X1aSheng/shark-socket/internal/types"
	"github.com/gorilla/websocket"
)

// Server is a WebSocket protocol server.
type Server struct {
	opts     Options
	upgrader websocket.Upgrader
	server   *stdhttp.Server
	listener net.Listener
	manager  *session.Manager
	chain    *plugin.Chain
	handler  types.RawHandler
	wg       sync.WaitGroup
	closed   atomic.Bool
}

// Compile-time verification.
var _ types.Server = (*Server)(nil)

// NewServer creates a new WebSocket server.
func NewServer(handler types.RawHandler, opts ...Option) *Server {
	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}
	allowedOrigins := make(map[string]bool)
	for _, origin := range o.AllowedOrigins {
		allowedOrigins[origin] = true
	}
	return &Server{
		opts:    o,
		handler: handler,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin: func(r *stdhttp.Request) bool {
				// When no origins are configured, allow all (developer-friendly default).
				// In production, explicitly configure AllowedOrigins to restrict access.
				if len(allowedOrigins) == 0 {
					return true
				}
				if allowedOrigins["*"] {
					return true
				}
				return allowedOrigins[r.Header.Get("Origin")]
			},
		},
	}
}

// Start begins serving WebSocket connections.
func (s *Server) Start() error {
	if err := s.opts.validate(); err != nil {
		return err
	}

	if s.manager == nil {
		s.manager = session.NewManager(session.WithMaxSessions(s.opts.MaxSessions))
	}

	if len(s.opts.Plugins) > 0 {
		s.chain = plugin.NewChain(s.opts.Plugins...)
	}

	mux := stdhttp.NewServeMux()
	mux.HandleFunc(s.opts.Path, s.handleUpgrade)

	s.server = &stdhttp.Server{
		Addr:    s.opts.Addr(),
		Handler: mux,
	}

	var ln net.Listener
	var err error
	if s.opts.TLSConfig != nil {
		ln, err = tls.Listen("tcp", s.opts.Addr(), s.opts.TLSConfig)
	} else {
		ln, err = net.Listen("tcp", s.opts.Addr())
	}
	if err != nil {
		return fmt.Errorf("websocket: listen on %s: %w", s.opts.Addr(), err)
	}
	s.listener = ln

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.server.Serve(s.listener); err != nil && err != stdhttp.ErrServerClosed {
			log.Printf("WebSocket server error: %v", err)
		}
	}()

	log.Printf("WebSocket server listening on %s%s", s.listener.Addr(), s.opts.Path)
	return nil
}

func (s *Server) handleUpgrade(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	start := time.Now()
	host, _, _ := net.SplitHostPort(r.RemoteAddr)

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		if s.opts.AccessLogger != nil {
			s.opts.AccessLogger.Log(logger.AccessLogEntry{
				Protocol:   "websocket",
				Method:     r.Method,
				Path:       r.URL.Path,
				StatusCode: 400,
				Duration:   time.Since(start),
				ClientIP:   host,
				UserAgent:  r.UserAgent(),
				Error:      err,
			})
		}
		return
	}

	id := s.manager.NextID()
	sess := NewWSSession(id, conn)

	if err := s.manager.Register(sess); err != nil {
		if s.opts.AccessLogger != nil {
			s.opts.AccessLogger.Log(logger.AccessLogEntry{
				Protocol:   "websocket",
				Method:     r.Method,
				Path:       r.URL.Path,
				StatusCode: 503,
				Duration:   time.Since(start),
				ClientIP:   host,
				UserAgent:  r.UserAgent(),
				Error:      err,
			})
		}
		sess.Close()
		return
	}

	if s.chain != nil {
		if err := s.chain.OnAccept(sess); err != nil {
			if s.opts.AccessLogger != nil {
				s.opts.AccessLogger.Log(logger.AccessLogEntry{
					Protocol:   "websocket",
					Method:     r.Method,
					Path:       r.URL.Path,
					StatusCode: 403,
					Duration:   time.Since(start),
					ClientIP:   host,
					UserAgent:  r.UserAgent(),
				})
			}
			s.manager.Unregister(id)
			sess.Close()
			return
		}
	}

	// Log successful upgrade
	if s.opts.AccessLogger != nil {
		s.opts.AccessLogger.Log(logger.AccessLogEntry{
			Protocol:   "websocket",
			Method:     r.Method,
			Path:       r.URL.Path,
			StatusCode: 101,
			Duration:   time.Since(start),
			ClientIP:   host,
			UserAgent:  r.UserAgent(),
		})
	}

	conn.SetReadLimit(int64(s.opts.MaxMessageSize))
	conn.SetPongHandler(func(appData string) error {
		sess.TouchActive()
		_ = conn.SetReadDeadline(time.Now().Add(s.opts.PingInterval + s.opts.PongTimeout))
		return nil
	})
	_ = conn.SetReadDeadline(time.Now().Add(s.opts.PingInterval + s.opts.PongTimeout))

	s.wg.Add(3)
	go func() {
		defer s.wg.Done()
		s.pingLoop(sess)
	}()
	go func() {
		defer s.wg.Done()
		s.readLoop(sess)
	}()
	go func() {
		defer s.wg.Done()
		<-sess.Context().Done()
		s.manager.Unregister(id)
		if s.chain != nil {
			s.chain.OnClose(sess)
		}
	}()
}

func (s *Server) readLoop(sess *WSSession) {
	defer sess.Close()

	for {
		_, msgData, err := sess.conn.ReadMessage()
		if err != nil {
			return
		}
		sess.TouchActive()

		data := msgData
		if s.chain != nil {
			data, err = s.chain.OnMessage(sess, msgData)
			if err != nil {
				if errors.Is(err, errs.ErrDrop) {
					continue
				}
				return
			}
		}

		if s.handler != nil {
			msg := types.NewRawMessage(sess.ID(), types.WebSocket, data)
			_ = s.handler(sess, msg)
		}
	}
}

func (s *Server) pingLoop(sess *WSSession) {
	ticker := time.NewTicker(s.opts.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if !sess.IsAlive() {
				return
			}
			if err := sess.sendPing(); err != nil {
				return
			}
		case <-sess.Context().Done():
			return
		}
	}
}

// Stop gracefully shuts down the WebSocket server.
func (s *Server) Stop(ctx context.Context) error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	if s.server != nil {
		_ = s.server.Shutdown(ctx)
	}
	if s.manager != nil {
		s.manager.Close()
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

// Protocol returns WebSocket.
func (s *Server) Protocol() types.ProtocolType { return types.WebSocket }

// Addr returns the listen address. Only valid after Start().
func (s *Server) Addr() net.Addr {
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// SetManager sets the session manager from outside (e.g., from Gateway).
func (s *Server) SetManager(m *session.Manager) {
	s.manager = m
}

// Manager returns the session manager.
func (s *Server) Manager() *session.Manager { return s.manager }
