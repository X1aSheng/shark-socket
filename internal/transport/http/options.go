package http

import (
	stdhttp "net/http"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/transport/shared"
)

type Options struct {
	Addr         string
	Handler      core.Handler
	Mux          *stdhttp.ServeMux
	MaxBodyBytes int64
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
	CORSOrigins  []string
	// Listen configures socket-level listener behaviour (SO_REUSEPORT, TCP
	// keepalive). The zero value produces exactly what net.Listen does.
	Listen shared.ListenOptions
}

type Option func(*Options)

func defaultOptions() Options {
	return Options{
		Addr:         "127.0.0.1:18400",
		Mux:          stdhttp.NewServeMux(),
		MaxBodyBytes: 8 * 1024 * 1024,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
}

func WithAddr(addr string) Option {
	return func(o *Options) {
		o.Addr = addr
	}
}

func WithHandler(handler core.Handler) Option {
	return func(o *Options) {
		o.Handler = handler
	}
}

func WithMaxBodyBytes(max int64) Option {
	return func(o *Options) {
		o.MaxBodyBytes = max
	}
}

func WithCORSAllowedOrigins(origins []string) Option {
	return func(o *Options) {
		o.CORSOrigins = append([]string(nil), origins...)
	}
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
