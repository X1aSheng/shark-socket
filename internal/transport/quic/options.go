package quic

import (
	"crypto/tls"

	"github.com/X1aSheng/shark-socket-new/internal/core"
)

type Options struct {
	Addr           string
	TLSConfig      *tls.Config
	Handler        core.Handler
	WriteQueueSize int
	MaxMessageSize int
}

type Option func(*Options)

func defaultOptions() Options {
	return Options{
		Addr:           "127.0.0.1:18800",
		WriteQueueSize: 128,
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
