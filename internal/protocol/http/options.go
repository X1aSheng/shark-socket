package http

import (
	"crypto/tls"
	"fmt"

	"github.com/X1aSheng/shark-socket/internal/types"
)

// Options holds HTTP server configuration.
type Options struct {
	Host         string
	Port         int
	ReadTimeout  int // seconds
	WriteTimeout int // seconds
	IdleTimeout  int // seconds
	MaxBodySize  int64
	TLSConfig    *tls.Config
	Plugins      []types.Plugin
}

func defaultOptions() Options {
	return Options{
		Host:         "0.0.0.0",
		Port:         18400,
		ReadTimeout:  30,
		WriteTimeout: 30,
		IdleTimeout:  120,
		MaxBodySize:  10 * 1024 * 1024, // 10MB default
	}
}

// Addr returns the listen address.
func (o Options) Addr() string {
	return fmt.Sprintf("%s:%d", o.Host, o.Port)
}

// Option is a functional option for HTTP server.
type Option func(*Options)

// WithAddr sets the listen address.
func WithAddr(host string, port int) Option {
	return func(o *Options) { o.Host = host; o.Port = port }
}

// WithTLS enables TLS.
func WithTLS(cfg *tls.Config) Option {
	return func(o *Options) { o.TLSConfig = cfg }
}

// WithTimeouts configures timeouts in seconds.
func WithTimeouts(read, write, idle int) Option {
	return func(o *Options) {
		o.ReadTimeout = read
		o.WriteTimeout = write
		o.IdleTimeout = idle
	}
}

// WithPlugins adds protocol-level plugins.
func WithPlugins(p ...types.Plugin) Option {
	return func(o *Options) { o.Plugins = append(o.Plugins, p...) }
}

// WithMaxBodySize sets the maximum request body size in bytes (0 = unlimited).
func WithMaxBodySize(n int64) Option {
	return func(o *Options) { o.MaxBodySize = n }
}

func (o Options) validate() error {
	if o.Port < 0 || o.Port > 65535 {
		return fmt.Errorf("http config: port must be 0-65535")
	}
	if o.ReadTimeout < 0 {
		return fmt.Errorf("http config: read timeout must be >= 0")
	}
	if o.WriteTimeout < 0 {
		return fmt.Errorf("http config: write timeout must be >= 0")
	}
	if o.IdleTimeout < 0 {
		return fmt.Errorf("http config: idle timeout must be >= 0")
	}
	return nil
}
