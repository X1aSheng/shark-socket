package coap

import (
	"time"

	"github.com/X1aSheng/shark-socket-new/internal/core"
)

type Options struct {
	Addr          string
	Handler       core.Handler
	Responder     func(core.Session, core.Message) ([]byte, error)
	SessionTTL    time.Duration
	SweepInterval time.Duration
	MaxDatagram   int
}

type Option func(*Options)

func defaultOptions() Options {
	return Options{
		Addr:          "127.0.0.1:18500",
		SessionTTL:    2 * time.Minute,
		SweepInterval: 30 * time.Second,
		MaxDatagram:   64 * 1024,
	}
}

func WithAddr(addr string) Option {
	return func(o *Options) { o.Addr = addr }
}

func WithHandler(handler core.Handler) Option {
	return func(o *Options) { o.Handler = handler }
}

func WithResponder(responder func(core.Session, core.Message) ([]byte, error)) Option {
	return func(o *Options) { o.Responder = responder }
}

func WithSessionTTL(ttl time.Duration) Option {
	return func(o *Options) {
		if ttl > 0 {
			o.SessionTTL = ttl
		}
	}
}

func WithSweepInterval(interval time.Duration) Option {
	return func(o *Options) {
		if interval > 0 {
			o.SweepInterval = interval
		}
	}
}
