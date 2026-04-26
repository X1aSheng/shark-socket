package http

import (
	"context"
	"io"
	"log"
	stdhttp "net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket/internal/plugin"
	"github.com/X1aSheng/shark-socket/internal/types"
)

// Server is an HTTP protocol server with two modes:
// Mode A (default): thin net/http wrapper, no session/plugin
// Mode B (optional): per-request session with plugin integration
type Server struct {
	opts    Options
	server  *stdhttp.Server
	handler types.RawHandler
	chain   *plugin.Chain
	wg      sync.WaitGroup
	closed  atomic.Bool
	idGen   atomic.Uint64
	mux     *stdhttp.ServeMux
}

// Compile-time verification.
var _ types.Server = (*Server)(nil)

// NewServer creates a new HTTP server.
func NewServer(opts ...Option) *Server {
	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}
	return &Server{
		opts: o,
		mux:  stdhttp.NewServeMux(),
	}
}

// Handle registers a handler for a pattern (Mode A).
func (s *Server) Handle(pattern string, handler stdhttp.Handler) {
	s.mux.Handle(pattern, handler)
}

// HandleFunc registers a handler function for a pattern (Mode A).
func (s *Server) HandleFunc(pattern string, handler stdhttp.HandlerFunc) {
	s.mux.HandleFunc(pattern, handler)
}

// Start begins serving HTTP.
func (s *Server) Start() error {
	if len(s.opts.Plugins) > 0 {
		s.chain = plugin.NewChain(s.opts.Plugins...)
	}

	if s.handler != nil {
		s.mux.HandleFunc("/", s.handleWithSession)
	}

	s.server = &stdhttp.Server{
		Addr:         s.opts.Addr(),
		Handler:      s.mux,
		ReadTimeout:  time.Duration(s.opts.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(s.opts.WriteTimeout) * time.Second,
		IdleTimeout:  time.Duration(s.opts.IdleTimeout) * time.Second,
	}
	if s.opts.TLSConfig != nil {
		s.server.TLSConfig = s.opts.TLSConfig
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		var err error
		if s.opts.TLSConfig != nil {
			err = s.server.ListenAndServeTLS("", "")
		} else {
			err = s.server.ListenAndServe()
		}
		if err != nil && err != stdhttp.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	log.Printf("HTTP server listening on %s", s.opts.Addr())
	return nil
}

func (s *Server) handleWithSession(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	id := s.idGen.Add(1)
	sess := NewHTTPSession(id, w, r)

	if s.chain != nil {
		if err := s.chain.OnAccept(sess); err != nil {
			stdhttp.Error(w, "Forbidden", stdhttp.StatusForbidden)
			return
		}
	}

	body := make([]byte, 0)
	if r.Body != nil {
		var reader io.Reader = r.Body
		if s.opts.MaxBodySize > 0 {
			reader = io.LimitReader(r.Body, s.opts.MaxBodySize+1)
		}
		buf := make([]byte, 4096)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				body = append(body, buf[:n]...)
			}
			if err != nil {
				break
			}
			if s.opts.MaxBodySize > 0 && int64(len(body)) > s.opts.MaxBodySize {
				stdhttp.Error(w, "Request body too large", stdhttp.StatusRequestEntityTooLarge)
				return
			}
		}
	}

	if s.chain != nil && len(body) > 0 {
		var err error
		body, err = s.chain.OnMessage(sess, body)
		if err != nil {
			stdhttp.Error(w, "Bad Request", stdhttp.StatusBadRequest)
			return
		}
	}

	if s.handler != nil {
		msg := types.NewRawMessage(sess.ID(), types.HTTP, body)
		_ = s.handler(sess, msg)
	}

	if s.chain != nil {
		s.chain.OnClose(sess)
	}
}

// Stop gracefully shuts down the HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

// Protocol returns HTTP.
func (s *Server) Protocol() types.ProtocolType { return types.HTTP }

// SetHandler sets the raw handler for mode B.
func (s *Server) SetHandler(h types.RawHandler) {
	s.handler = h
}
