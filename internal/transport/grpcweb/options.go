package grpcweb

import (
	"time"

	"github.com/X1aSheng/shark-socket-new/internal/core"
)

type Options struct {
	Addr            string
	Path            string
	Handler         core.Handler
	MaxMessageBytes int64
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
}

type Option func(*Options)

func defaultOptions() Options {
	return Options{
		Addr:            "127.0.0.1:18900",
		Path:            "/grpc",
		MaxMessageBytes: 4 * 1024 * 1024,
		ReadTimeout:     10 * time.Second,
		WriteTimeout:    10 * time.Second,
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
