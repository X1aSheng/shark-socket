package grpcgw

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	stdhttp "net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/X1aSheng/shark-socket/internal/errs"
	"github.com/X1aSheng/shark-socket/internal/infra/logger"
	"github.com/X1aSheng/shark-socket/internal/plugin"
	"github.com/X1aSheng/shark-socket/internal/session"
	"github.com/X1aSheng/shark-socket/internal/types"
)

// ProtocolMode defines how the gateway handles gRPC-Web requests.
type ProtocolMode int

const (
	// WebSocket mode forwards requests to WebSocket connections
	WebSocket ProtocolMode = iota
	// Direct mode processes requests directly (for unary calls)
	Direct
)

// Server is a gRPC-Web gateway server.
type Server struct {
	opts      Options
	server    *stdhttp.Server
	listener  net.Listener
	manager   *session.Manager
	chain     *plugin.Chain
	upgrader  websocket.Upgrader
	wg        sync.WaitGroup
	closed    atomic.Bool
	idGen     atomic.Uint64
	serveHTTP bool // serve plain HTTP for health checks
}

// Compile-time verification.
var _ types.Server = (*Server)(nil)

// NewServer creates a new gRPC-Web gateway server.
func NewServer(opts ...Option) *Server {
	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}
	return &Server{
		opts:   o,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
		},
	}
}

// Start begins serving gRPC-Web requests.
func (s *Server) Start() error {
	if err := s.opts.validate(); err != nil {
		return err
	}

	s.manager = session.NewManager(session.WithMaxSessions(s.opts.MaxSessions))

	if len(s.opts.Plugins) > 0 {
		s.chain = plugin.NewChain(s.opts.Plugins...)
	}

	mux := stdhttp.NewServeMux()

	// gRPC-Web handler
	mux.HandleFunc(s.opts.Path, s.handleGRPCWeb)

	// Health check endpoint
	mux.HandleFunc("/health", s.handleHealth)

	s.server = &stdhttp.Server{
		Addr:         s.opts.Addr(),
		Handler:      mux,
		ReadTimeout:  time.Duration(s.opts.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(s.opts.WriteTimeout) * time.Second,
		IdleTimeout:  time.Duration(s.opts.IdleTimeout) * time.Second,
	}

	var ln net.Listener
	var err error
	if s.opts.TLSConfig != nil {
		ln, err = tls.Listen("tcp", s.opts.Addr(), s.opts.TLSConfig)
	} else {
		ln, err = net.Listen("tcp", s.opts.Addr())
	}
	if err != nil {
		return fmt.Errorf("grpcgw: listen on %s: %w", s.opts.Addr(), err)
	}
	s.listener = ln

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.server.Serve(s.listener); err != nil && err != stdhttp.ErrServerClosed {
			log.Printf("gRPC-Web gateway error: %v", err)
		}
	}()

	modeStr := "WebSocket"
	if s.opts.Mode == Direct {
		modeStr = "Direct"
	}
	log.Printf("gRPC-Web gateway listening on %s%s (mode: %s)",
		s.listener.Addr(), s.opts.Path, modeStr)
	return nil
}

// Stop gracefully shuts down the gateway.
func (s *Server) Stop(ctx context.Context) error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	if s.server != nil {
		_ = s.server.Shutdown(ctx)
	}
	if s.manager != nil {
		_ = s.manager.Close()
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

// Protocol returns Custom for gateway.
func (s *Server) Protocol() types.ProtocolType { return types.Custom }

// Addr returns the listen address. Only valid after Start().
func (s *Server) Addr() net.Addr {
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// Manager returns the session manager.
func (s *Server) Manager() *session.Manager { return s.manager }

// handleGRPCWeb handles gRPC-Web requests.
func (s *Server) handleGRPCWeb(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	start := time.Now()

	switch s.opts.Mode {
	case WebSocket:
		s.handleWebSocketMode(w, r, start)
	case Direct:
		s.handleDirectMode(w, r, start)
	}
}

// handleWebSocketMode upgrades gRPC-Web to WebSocket for bidirectional streaming.
func (s *Server) handleWebSocketMode(w stdhttp.ResponseWriter, r *stdhttp.Request, start time.Time) {
	// gRPC-Web over WebSocket upgrade
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logAccess(r, 400, start)
		return
	}
	defer conn.Close()

	id := s.idGen.Add(1)
	sess := newGRPCWebSession(id, conn)

	if err := s.manager.Register(sess); err != nil {
		s.logAccess(r, 503, start)
		return
	}

	if s.chain != nil {
		if err := s.chain.OnAccept(sess); err != nil {
			s.manager.Unregister(id)
			s.logAccess(r, 403, start)
			return
		}
	}

	s.logAccess(r, 101, start) // 101 = Switching Protocols

	// gRPC-Web message loop over WebSocket
	s.handleWebSocketMessages(sess)
}

// handleDirectMode handles unary gRPC-Web calls directly.
func (s *Server) handleDirectMode(w stdhttp.ResponseWriter, r *stdhttp.Request, start time.Time) {
	// Read gRPC-Web message from body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.logAccess(r, 400, start)
		stdhttp.Error(w, "Bad Request", stdhttp.StatusBadRequest)
		return
	}

	id := s.idGen.Add(1)
	sess := newGRPCWebSessionDirect(id, r)

	if s.chain != nil {
		if err := s.chain.OnAccept(sess); err != nil {
			s.logAccess(r, 403, start)
			stdhttp.Error(w, "Forbidden", stdhttp.StatusForbidden)
			return
		}
	}

	if s.chain != nil && len(body) > 0 {
		var err error
		body, err = s.chain.OnMessage(sess, body)
		if err != nil {
			s.logAccess(r, 400, start)
			stdhttp.Error(w, "Bad Request", stdhttp.StatusBadRequest)
			return
		}
	}

	// Process gRPC-Web request
	response, status := s.processGRPCWebRequest(body, sess)

	// Write response
	w.Header().Set("Content-Type", "application/grpc-web+proto")
	w.WriteHeader(status)
	if response != nil {
		w.Write(response)
	}

	s.logAccess(r, status, start)

	if s.chain != nil {
		s.chain.OnClose(sess)
	}
}

// handleWebSocketMessages reads gRPC-Web messages from WebSocket.
func (s *Server) handleWebSocketMessages(sess *GRPCWebSession) {
	for {
		msgType, data, err := sess.conn.ReadMessage()
		if err != nil {
			return
		}

		sess.TouchActive()

		// Handle WebSocket message types
		switch msgType {
		case websocket.BinaryMessage:
			// gRPC-Web binary message
			if s.chain != nil {
				data, err = s.chain.OnMessage(sess, data)
				if err != nil {
					if err == errs.ErrDrop {
						continue
					}
					return
				}
			}
			if s.opts.Handler != nil {
				msg := types.NewRawMessage(sess.ID(), types.Custom, data)
				_ = s.opts.Handler(sess, msg)
			}
		case websocket.TextMessage:
			// gRPC-Web JSON message (alternative format)
			if s.chain != nil {
				data, err = s.chain.OnMessage(sess, data)
				if err != nil {
					if err == errs.ErrDrop {
						continue
					}
					return
				}
			}
		case websocket.CloseMessage:
			return
		}
	}
}

// processGRPCWebRequest processes a gRPC-Web request and returns response data.
func (s *Server) processGRPCWebRequest(data []byte, sess *GRPCWebSessionDirect) ([]byte, int) {
	// For unary calls, process the message
	if s.opts.Handler != nil {
		msg := types.NewRawMessage(sess.ID(), types.Custom, data)
		if err := s.opts.Handler(sess, msg); err != nil {
			return nil, 500
		}
	}
	return data, 200
}

// SetHandler sets the message handler for the gateway.
func (s *Server) SetHandler(h types.RawHandler) {
	s.opts.Handler = h
}

// handleHealth returns the health status.
func (s *Server) handleHealth(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"status":"healthy"}`)
}

// logAccess logs access for the gateway.
func (s *Server) logAccess(r *stdhttp.Request, status int, start time.Time) {
	if s.opts.AccessLogger == nil {
		return
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if host == "" {
		host = r.RemoteAddr
	}
	s.opts.AccessLogger.Log(logger.AccessLogEntry{
		Protocol:   "grpc-web",
		Method:     r.Method,
		Path:       r.URL.Path,
		StatusCode: status,
		Duration:   time.Since(start),
		ClientIP:   host,
		UserAgent:  r.UserAgent(),
	})
}