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
	// MaxIdleTimeout is the quic-go connection idle timeout: a peer that
	// stops sending any packet for this long is closed by quic-go itself
	// (the last line of defense against half-open QUIC connections). 0 uses
	// the quic-go default (30s).
	MaxIdleTimeout time.Duration
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

// WithMaxConnections caps concurrent accepted connections. Values <= 0 mean
// unlimited; excess connections are closed with an application error.
func WithMaxConnections(max int64) Option {
	return func(o *Options) { o.MaxConnections = max }
}

// WithAcceptRate limits the connection acceptance rate in connections per
// second (token bucket with a burst of one). Values <= 0 mean unlimited;
// excess connections are closed with an application error.
func WithAcceptRate(rate float64) Option {
	return func(o *Options) { o.AcceptRate = rate }
}

// WithMaxIdleTimeout sets the quic-go connection idle timeout. Values <= 0
// keep the quic-go default (30s). See Options.MaxIdleTimeout.
func WithMaxIdleTimeout(timeout time.Duration) Option {
	return func(o *Options) {
		if timeout > 0 {
			o.MaxIdleTimeout = timeout
		}
	}
}
