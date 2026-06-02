package websocket

import (
	"crypto/tls"
	"net/http"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
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
	MaxConnections int64
	AcceptRate     float64
}

type Option func(*Options)

func defaultOptions() Options {
	return Options{
		Addr:           "127.0.0.1:18700",
		Path:           "/ws",
		PingInterval:   30 * time.Second,
		PongTimeout:    60 * time.Second,
		MaxMessageSize: 1024 * 1024,
		CheckOrigin: func(*http.Request) bool {
			return true
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
