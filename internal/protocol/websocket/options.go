package websocket

import (
	"crypto/tls"
	"fmt"
	"time"

	"github.com/X1aSheng/shark-socket/internal/types"
)

var errConfig = func(msg string) error { return fmt.Errorf("websocket config: %s", msg) }

// Options holds WebSocket server configuration.
type Options struct {
	Host           string
	Port           int
	Path           string
	MaxSessions    int64
	MaxMessageSize int
	PingInterval   time.Duration
	PongTimeout    time.Duration
	AllowedOrigins []string
	TLSConfig      *tls.Config
	Plugins        []types.Plugin
}

func defaultOptions() Options {
	return Options{
		Host:           "0.0.0.0",
		Port:           18600,
		Path:           "/ws",
		MaxSessions:    100000,
		MaxMessageSize: 1024 * 1024, // 1MB
		PingInterval:   30 * time.Second,
		PongTimeout:    10 * time.Second,
	}
}

// Addr returns the listen address.
func (o Options) Addr() string {
	return fmt.Sprintf("%s:%d", o.Host, o.Port)
}

// Option is a functional option for WebSocket server.
type Option func(*Options)

// WithAddr sets the listen address.
func WithAddr(host string, port int) Option {
	return func(o *Options) { o.Host = host; o.Port = port }
}

// WithPath sets the WebSocket upgrade path.
func WithPath(path string) Option {
	return func(o *Options) { o.Path = path }
}

// WithMaxSessions sets the maximum session count.
func WithMaxSessions(max int64) Option {
	return func(o *Options) { o.MaxSessions = max }
}

// WithMaxMessageSize sets the maximum message size.
func WithMaxMessageSize(max int) Option {
	return func(o *Options) { o.MaxMessageSize = max }
}

// WithPingPong configures ping interval and pong timeout.
func WithPingPong(interval, timeout time.Duration) Option {
	return func(o *Options) {
		o.PingInterval = interval
		o.PongTimeout = timeout
	}
}

// WithAllowedOrigins sets allowed origins.
func WithAllowedOrigins(origins ...string) Option {
	return func(o *Options) { o.AllowedOrigins = origins }
}

// WithTLS enables TLS.
func WithTLS(cfg *tls.Config) Option {
	return func(o *Options) { o.TLSConfig = cfg }
}

// WithPlugins adds protocol-level plugins.
func WithPlugins(p ...types.Plugin) Option {
	return func(o *Options) { o.Plugins = append(o.Plugins, p...) }
}

func (o Options) validate() error {
	if o.Port < 0 || o.Port > 65535 {
		return errConfig("port must be 0-65535")
	}
	if o.Path == "" {
		return errConfig("path must not be empty")
	}
	if o.MaxSessions < 0 {
		return errConfig("max sessions must be >= 0")
	}
	if o.MaxMessageSize < 1 {
		return errConfig("max message size must be >= 1")
	}
	if o.PingInterval < time.Second {
		return errConfig("ping interval must be >= 1s")
	}
	if o.PongTimeout < time.Second {
		return errConfig("pong timeout must be >= 1s")
	}
	return nil
}
