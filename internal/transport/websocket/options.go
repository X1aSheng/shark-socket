package websocket

import (
	"crypto/tls"
	"net/http"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/transport/shared"
)

type Options struct {
	Addr           string
	Path           string
	Handler        core.Handler
	TLSConfig      *tls.Config
	CheckOrigin    func(*http.Request) bool
	PingInterval   time.Duration
	PongTimeout    time.Duration
	MaxMessageSize int64
	WriteTimeout   time.Duration
	ReadTimeout    time.Duration
	IdleTimeout    time.Duration
	MaxConnections int64
	AcceptRate     float64
	// Listen configures socket-level listener behaviour (SO_REUSEPORT, TCP
	// keepalive). The zero value produces exactly what net.Listen does.
	Listen shared.ListenOptions
	// Proxy makes the listener expect a PROXY protocol header ahead of each
	// connection, so RemoteAddr reports the client the load balancer saw.
	// Zero value means off.
	Proxy shared.ProxyConfig
}

type Option func(*Options)

func defaultOptions() Options {
	return Options{
		Addr:           "127.0.0.1:18700",
		Path:           "/ws",
		PingInterval:   30 * time.Second,
		PongTimeout:    60 * time.Second,
		MaxMessageSize: 1024 * 1024,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		IdleTimeout:    120 * time.Second,
		CheckOrigin: func(*http.Request) bool {
			return false // reject by default; use WithCheckOrigin to allow origins
		},
	}
}

func WithAddr(addr string) Option {
	return func(o *Options) { o.Addr = addr }
}

func WithPath(path string) Option {
	return func(o *Options) {
		if path != "" {
			o.Path = path
		}
	}
}

func WithHandler(handler core.Handler) Option {
	return func(o *Options) { o.Handler = handler }
}

func WithTLSConfig(cfg *tls.Config) Option {
	return func(o *Options) { o.TLSConfig = cfg }
}

func WithCheckOrigin(fn func(*http.Request) bool) Option {
	return func(o *Options) {
		if fn != nil {
			o.CheckOrigin = fn
		}
	}
}

func WithPingInterval(interval time.Duration) Option {
	return func(o *Options) {
		if interval > 0 {
			o.PingInterval = interval
		}
	}
}

func WithPongTimeout(timeout time.Duration) Option {
	return func(o *Options) {
		if timeout > 0 {
			o.PongTimeout = timeout
		}
	}
}

// WithMaxConnections caps concurrent accepted connections. Values <= 0 mean
// unlimited; excess upgrade attempts are rejected with 503.
func WithMaxConnections(max int64) Option {
	return func(o *Options) { o.MaxConnections = max }
}

// WithAcceptRate limits the connection acceptance rate in connections per
// second (token bucket with a burst of one). Values <= 0 mean unlimited;
// excess upgrade attempts are rejected with 503.
func WithAcceptRate(rate float64) Option {
	return func(o *Options) { o.AcceptRate = rate }
}

// WithReusePort lets several processes bind the same address, with the kernel
// distributing connections across them. Linux and Darwin only; elsewhere the
// listener reports shared.ErrReusePortUnsupported rather than silently ignoring
// the request. See tcp.WithReusePort for the full trade-off.
func WithReusePort() Option {
	return func(o *Options) { o.Listen.ReusePort = true }
}

// WithKeepAlive overrides the TCP keepalive schedule; a negative idle disables
// probes. Zero values keep the platform default. See tcp.WithKeepAlive.
func WithKeepAlive(idle, interval time.Duration, count int) Option {
	return func(o *Options) {
		o.Listen.KeepAliveIdle = idle
		o.Listen.KeepAliveInterval = interval
		o.Listen.KeepAliveCount = count
	}
}

// WithProxyProtocol requires a PROXY protocol header on every connection.
// The trust requirements are the same as tcp.WithProxyProtocol: the port must
// be reachable only from the balancer, because any client that reaches it can
// claim any source address. See tcp.WithProxyProtocol.
func WithProxyProtocol(mode shared.ProxyProtocolMode) Option {
	return func(o *Options) { o.Proxy.Mode = mode }
}

// WithProxyHeaderTimeout bounds how long a connection may take to send its
// header. Zero or negative keeps shared.DefaultProxyHeaderTimeout (5s).
func WithProxyHeaderTimeout(timeout time.Duration) Option {
	return func(o *Options) {
		if timeout > 0 {
			o.Proxy.Timeout = timeout
		}
	}
}
