package grpcweb

import (
	"crypto/tls"
	"net/http"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type Options struct {
	Addr            string
	Path            string
	Handler         core.Handler
	TLSConfig       *tls.Config
	MaxMessageBytes int64
	MaxConnections  int64
	AcceptRate      float64
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	WebSocket       bool
	WebSocketPath   string
	CheckOrigin     func(*http.Request) bool
}

type Option func(*Options)

func defaultOptions() Options {
	return Options{
		Addr:            "127.0.0.1:18900",
		Path:            "/grpc",
		MaxMessageBytes: 4 * 1024 * 1024,
		ReadTimeout:     10 * time.Second,
		WriteTimeout:    10 * time.Second,
		WebSocketPath:   "/grpc/ws",
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

func WithMaxMessageBytes(max int64) Option {
	return func(o *Options) { o.MaxMessageBytes = max }
}

func WithWebSocketMode(path string) Option {
	return func(o *Options) {
		o.WebSocket = true
		if path != "" {
			o.WebSocketPath = path
		}
	}
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
