package http

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

	"github.com/X1aSheng/shark-socket/internal/infra/logger"
	"github.com/X1aSheng/shark-socket/internal/infra/tracing"
	"github.com/X1aSheng/shark-socket/internal/plugin"
	"github.com/X1aSheng/shark-socket/internal/types"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// Server is an HTTP protocol server with two modes:
// Mode A (default): thin net/http wrapper, no session/plugin
// Mode B (optional): per-request session with plugin integration
type Server struct {
	opts     Options
	server   *stdhttp.Server
	listener net.Listener
	handler  types.RawHandler
	chain    *plugin.Chain
	wg       sync.WaitGroup
	closed   atomic.Bool
	idGen    atomic.Uint64
	mux      *stdhttp.ServeMux
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
	if err := s.opts.validate(); err != nil {
		return err
	}

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

	var ln net.Listener
	var err error
	if s.opts.TLSConfig != nil {
		ln, err = tls.Listen("tcp", s.opts.Addr(), s.opts.TLSConfig)
	} else {
		ln, err = net.Listen("tcp", s.opts.Addr())
	}
	if err != nil {
		return fmt.Errorf("http: listen on %s: %w", s.opts.Addr(), err)
	}
	s.listener = ln

	// Configure HTTP/2 if enabled
	if s.opts.EnableHTTP2 {
		if err := s.configureHTTP2(); err != nil {
			return fmt.Errorf("http: configure HTTP/2: %w", err)
		}
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.server.Serve(s.listener); err != nil && err != stdhttp.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	log.Printf("HTTP server listening on %s (HTTP/2: %v)", s.listener.Addr(), s.opts.EnableHTTP2)
	return nil
}

// configureHTTP2 sets up HTTP/2 support.
// For TLS connections, HTTP/2 is negotiated via ALPN.
// For cleartext connections (h2c), we use golang.org/x/net/http2/h2c.
func (s *Server) configureHTTP2() error {
	h2Server := &http2.Server{
		MaxConcurrentStreams: uint32(s.opts.MaxConcurrentStreams),
	}

	if s.opts.TLSConfig != nil {
		// HTTPS mode: HTTP/2 is negotiated via ALPN
		s.opts.TLSConfig.NextProtos = append(s.opts.TLSConfig.NextProtos, "h2")

		if err := http2.ConfigureServer(s.server, h2Server); err != nil {
			return fmt.Errorf("http: configure HTTP/2: %w", err)
		}
		log.Printf("HTTP/2 enabled for HTTPS on %s", s.opts.Addr())
	} else {
		// Cleartext h2c mode: wrap handler with h2c handler
		h2Handler := h2c.NewHandler(s.mux, h2Server)
		s.server.Handler = h2Handler
		log.Printf("HTTP/2 over cleartext (h2c) enabled on %s", s.opts.Addr())
	}
	return nil
}

func (s *Server) handleWithSession(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	start := time.Now()

	// Start tracing span if tracer is configured
	var span tracing.Span
	if s.opts.Tracer != nil {
		span, _ = s.opts.Tracer.StartSpan(r.Context(), "http.request",
			tracing.WithAttribute("method", r.Method),
			tracing.WithAttribute("path", r.URL.Path),
			tracing.WithAttribute("remote_addr", r.RemoteAddr),
		)
		defer func() {
			if span != nil {
				span.End()
			}
		}()
	}

	// Check connection rate limit if configured
	var rateLimitHost string
	if s.opts.ConnRateLimit != nil {
		// r.RemoteAddr is a string like "192.168.1.1:12345"
		// Parse it to extract just the IP for rate limiting
		host, _, err := splitHostPort(r.RemoteAddr)
		if err != nil {
			// If we can't parse, allow by default
			host = r.RemoteAddr
		}
		rateLimitHost = host
		if !s.opts.ConnRateLimit.Allow(host) {
			if s.opts.AccessLogger != nil {
				s.opts.AccessLogger.Log(logger.AccessLogEntry{
					Protocol:   "http",
					Method:     r.Method,
					Path:       r.URL.Path,
					StatusCode: 429,
					Duration:   time.Since(start),
					ClientIP:   host,
					UserAgent:  r.UserAgent(),
				})
			}
			stdhttp.Error(w, "Too Many Requests", stdhttp.StatusTooManyRequests)
			return
		}
		defer func() {
			if rateLimitHost != "" {
				s.opts.ConnRateLimit.Remove(rateLimitHost)
			}
		}()
	}

	id := s.idGen.Add(1)
	sess := NewHTTPSession(id, w, r)

	if s.chain != nil {
		if err := s.chain.OnAccept(sess); err != nil {
			if s.opts.AccessLogger != nil {
				host, _, _ := splitHostPort(r.RemoteAddr)
				s.opts.AccessLogger.Log(logger.AccessLogEntry{
					Protocol:   "http",
					Method:     r.Method,
					Path:       r.URL.Path,
					StatusCode: 403,
					Duration:   time.Since(start),
					ClientIP:   host,
					UserAgent:  r.UserAgent(),
				})
			}
			stdhttp.Error(w, "Forbidden", stdhttp.StatusForbidden)
			return
		}
	}

	var body []byte
	if r.Body != nil {
		if s.opts.MaxBodySize > 0 {
			r.Body = stdhttp.MaxBytesReader(w, r.Body, s.opts.MaxBodySize)
		}
		readBody, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			status := stdhttp.StatusBadRequest
			if _, ok := readErr.(*stdhttp.MaxBytesError); ok {
				status = stdhttp.StatusRequestEntityTooLarge
			}
			if s.opts.AccessLogger != nil {
				host, _, _ := splitHostPort(r.RemoteAddr)
				s.opts.AccessLogger.Log(logger.AccessLogEntry{
					Protocol:   "http",
					Method:     r.Method,
					Path:       r.URL.Path,
					StatusCode: status,
					Duration:   time.Since(start),
					ClientIP:   host,
					UserAgent:  r.UserAgent(),
					Error:      readErr,
				})
			}
			stdhttp.Error(w, stdhttp.StatusText(status), status)
			return
		}
		body = readBody
	}

		if s.chain != nil && len(body) > 0 {
			var err error
			body, err = s.chain.OnMessage(sess, body)
			if err != nil {
				if s.opts.AccessLogger != nil {
					host, _, _ := splitHostPort(r.RemoteAddr)
					s.opts.AccessLogger.Log(logger.AccessLogEntry{
						Protocol:   "http",
						Method:     r.Method,
						Path:       r.URL.Path,
						StatusCode: 400,
						Duration:   time.Since(start),
						ClientIP:   host,
						UserAgent:  r.UserAgent(),
						Error:      err,
					})
				}
				stdhttp.Error(w, "Bad Request", stdhttp.StatusBadRequest)
				return
			}
		}

	status := 200
	if s.handler != nil {
		msg := types.NewRawMessage(sess.ID(), types.HTTP, body)
		_ = s.handler(sess, msg)
		if v, ok := sess.GetMeta("http_status"); ok {
			if code, ok := v.(int); ok {
				status = code
			}
		}
	}
	w.WriteHeader(status)

	if s.chain != nil {
		s.chain.OnClose(sess)
	}

	// Log access after request is processed
	if s.opts.AccessLogger != nil {
		host, _, _ := splitHostPort(r.RemoteAddr)
		s.opts.AccessLogger.Log(logger.AccessLogEntry{
			Protocol:    "http",
			Method:      r.Method,
			Path:        r.URL.Path,
			StatusCode:  status,
			Duration:    time.Since(start),
			ClientIP:    host,
			UserAgent:   r.UserAgent(),
			BytesIn:     int64(len(body)),
		})
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

// Addr returns the listen address. Only valid after Start().
func (s *Server) Addr() net.Addr {
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// SetHandler sets the raw handler for mode B.
func (s *Server) SetHandler(h types.RawHandler) {
	s.handler = h
}

// splitHostPort splits a host:port string into host and port.
// Returns host as-is if parsing fails.
func splitHostPort(addr string) (host, port string, err error) {
	host, port, err = net.SplitHostPort(addr)
	if err != nil {
		return addr, "", err
	}
	return host, port, nil
}
