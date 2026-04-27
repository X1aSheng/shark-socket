package grpcgw

import (
	"crypto/tls"
	"fmt"
	stdhttp "net/http"
	"net"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/X1aSheng/shark-socket/internal/errs"
	"github.com/X1aSheng/shark-socket/internal/infra/logger"
	"github.com/X1aSheng/shark-socket/internal/infra/tracing"
	"github.com/X1aSheng/shark-socket/internal/session"
	"github.com/X1aSheng/shark-socket/internal/types"
)

// Options holds gRPC-Web gateway configuration.
type Options struct {
	Host           string
	Port           int
	Path           string
	Mode           ProtocolMode
	MaxSessions    int64
	MaxMessageSize int
	ReadTimeout    int // seconds
	WriteTimeout   int // seconds
	IdleTimeout    int // seconds
	TLSConfig      *tls.Config
	Plugins        []types.Plugin
	Handler        types.RawHandler
	Tracer         tracing.Tracer
	AccessLogger   logger.AccessLogger
	WriteQueueSize int
	AllowedOrigins []string
}

func defaultOptions() Options {
	return Options{
		Host:           "0.0.0.0",
		Port:           18650,
		Path:           "/grpc",
		Mode:           WebSocket,
		MaxSessions:    100000,
		MaxMessageSize: 1024 * 1024, // 1MB
		ReadTimeout:    30,
		WriteTimeout:   30,
		IdleTimeout:    120,
		WriteQueueSize: 128,
	}
}

// Addr returns the listen address.
func (o Options) Addr() string {
	return fmt.Sprintf("%s:%d", o.Host, o.Port)
}

// Option is a functional option for gRPC-Web gateway.
type Option func(*Options)

// WithAddr sets the listen address.
func WithAddr(host string, port int) Option {
	return func(o *Options) { o.Host = host; o.Port = port }
}

// WithPath sets the gRPC-Web endpoint path.
func WithPath(path string) Option {
	return func(o *Options) { o.Path = path }
}

// WithMode sets the gateway mode (WebSocket or Direct).
func WithMode(mode ProtocolMode) Option {
	return func(o *Options) { o.Mode = mode }
}

// WithMaxSessions sets the maximum session count.
func WithMaxSessions(max int64) Option {
	return func(o *Options) { o.MaxSessions = max }
}

// WithMaxMessageSize sets the maximum message size.
func WithMaxMessageSize(max int) Option {
	return func(o *Options) { o.MaxMessageSize = max }
}

// WithTLS enables TLS.
func WithTLS(cfg *tls.Config) Option {
	return func(o *Options) { o.TLSConfig = cfg }
}

// WithPlugins adds protocol-level plugins.
func WithPlugins(p ...types.Plugin) Option {
	return func(o *Options) { o.Plugins = append(o.Plugins, p...) }
}

// WithTimeouts configures timeouts in seconds.
func WithTimeouts(read, write, idle int) Option {
	return func(o *Options) {
		o.ReadTimeout = read
		o.WriteTimeout = write
		o.IdleTimeout = idle
	}
}

// WithTracer sets the distributed tracing implementation.
func WithTracer(t tracing.Tracer) Option {
	return func(o *Options) { o.Tracer = t }
}

// WithAccessLogger sets the access logger for request logging.
func WithAccessLogger(l logger.AccessLogger) Option {
	return func(o *Options) { o.AccessLogger = l }
}

// WithAllowedOrigins sets allowed origins for WebSocket upgrade.
func WithAllowedOrigins(origins ...string) Option {
	return func(o *Options) { o.AllowedOrigins = origins }
}

func (o Options) validate() error {
	if o.Port < 0 || o.Port > 65535 {
		return fmt.Errorf("grpcgw config: port must be 0-65535")
	}
	if o.Path == "" {
		return fmt.Errorf("grpcgw config: path must not be empty")
	}
	if o.MaxSessions < 0 {
		return fmt.Errorf("grpcgw config: max sessions must be >= 0")
	}
	if o.MaxMessageSize < 1 {
		return fmt.Errorf("grpcgw config: max message size must be >= 1")
	}
	return nil
}

// --- Session Types ---

// GRPCWebSession represents a WebSocket-based gRPC-Web session.
type GRPCWebSession struct {
	*session.BaseSession
	conn      *websocket.Conn
	closeOnce sync.Once
}

func newGRPCWebSession(id uint64, conn *websocket.Conn) *GRPCWebSession {
	s := &GRPCWebSession{
		BaseSession: session.NewBase(id, types.Custom, conn.RemoteAddr(), conn.LocalAddr()),
		conn:        conn,
	}
	s.SetState(types.Active)
	return s
}

// Send writes data to the WebSocket connection.
func (s *GRPCWebSession) Send(data []byte) error {
	if !s.IsAlive() {
		return errs.ErrSessionClosed
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, data)
}

// SendTyped writes typed data to the WebSocket connection.
func (s *GRPCWebSession) SendTyped(msg []byte) error {
	return s.Send(msg)
}

// Close closes the gRPC-Web session.
func (s *GRPCWebSession) Close() error {
	var err error
	s.closeOnce.Do(func() {
		s.SetState(types.Closing)
		err = s.conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		s.SetState(types.Closed)
		s.DoClose()
		_ = s.conn.Close()
	})
	return err
}

// GRPCWebSessionDirect represents a direct (unary) gRPC-Web session.
type GRPCWebSessionDirect struct {
	*session.BaseSession
	request *stdhttp.Request
	remote  *net.TCPAddr
}

func newGRPCWebSessionDirect(id uint64, r *stdhttp.Request) *GRPCWebSessionDirect {
	// Parse remote address
	host, port, _ := net.SplitHostPort(r.RemoteAddr)
	remote := &net.TCPAddr{
		IP:   net.ParseIP(host),
		Port: 0,
	}
	if port != "" {
		var p int
		fmt.Sscanf(port, "%d", &p)
		remote.Port = p
	}

	s := &GRPCWebSessionDirect{
		BaseSession: session.NewBase(id, types.Custom, remote, nil),
		request:     r,
		remote:      remote,
	}
	s.SetState(types.Active)
	return s
}

// Send is a no-op for direct mode (response is sent immediately).
func (s *GRPCWebSessionDirect) Send(data []byte) error {
	return nil
}

// SendTyped is a no-op for direct mode.
func (s *GRPCWebSessionDirect) SendTyped(msg []byte) error {
	return nil
}

// Close is a no-op for direct mode.
func (s *GRPCWebSessionDirect) Close() error {
	s.SetState(types.Closed)
	s.DoClose()
	return nil
}

// Request returns the underlying HTTP request.
func (s *GRPCWebSessionDirect) Request() *stdhttp.Request {
	return s.request
}
