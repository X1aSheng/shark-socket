package quic

import (
	"crypto/tls"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type Options struct {
	Addr           string
	TLSConfig      *tls.Config
	Handler        core.Handler
	WriteQueueSize int
	WriteTimeout   time.Duration
	ReadTimeout    time.Duration // stream read deadline; 0 disables
	MaxMessageSize int
	MaxConnections int64
	AcceptRate     float64
}

type Option func(*Options)

func defaultOptions() Options {
	return Options{
		Addr:           "127.0.0.1:18800",
		WriteQueueSize: 128,
		WriteTimeout:   30 * time.Second,
		ReadTimeout:    5 * time.Minute, // bounds idle streams (anti-slowloris)
		MaxMessageSize: 1024 * 1024,
	}
}

func WithAddr(addr string) Option {
	return func(o *Options) { o.Addr = addr }
}

func WithTLS(config *tls.Config) Option {
	return func(o *Options) { o.TLSConfig = config }
}

func WithHandler(handler core.Handler) Option {
	return func(o *Options) { o.Handler = handler }
}
