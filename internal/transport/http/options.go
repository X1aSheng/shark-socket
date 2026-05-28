package http

import (
	stdhttp "net/http"
	"time"

	"github.com/X1aSheng/shark-socket-new/internal/core"
)

type Options struct {
	Addr         string
	Handler      core.Handler
	Mux          *stdhttp.ServeMux
	MaxBodyBytes int64
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
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
