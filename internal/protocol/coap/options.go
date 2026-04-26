package coap

import (
	"fmt"
	"time"

	"github.com/X1aSheng/shark-socket/internal/types"
)

// Options holds CoAP server configuration.
type Options struct {
	Host            string
	Port            int
	MaxSessions     int64
	SessionTTL      time.Duration
	AckTimeout      time.Duration
	MaxRetransmit   int
	MessageIDCacheSize int
	Plugins         []types.Plugin
}

func defaultOptions() Options {
	return Options{
		Host:            "0.0.0.0",
		Port:            18800,
		MaxSessions:     100000,
		SessionTTL:      5 * time.Minute,
		AckTimeout:      2 * time.Second,
		MaxRetransmit:   4,
		MessageIDCacheSize: 500,
	}
}

// Addr returns the listen address.
func (o Options) Addr() string {
	return fmt.Sprintf("%s:%d", o.Host, o.Port)
}

// Option is a functional option for CoAP server.
type Option func(*Options)

// WithAddr sets the listen address.
func WithAddr(host string, port int) Option {
	return func(o *Options) { o.Host = host; o.Port = port }
}

// WithMaxSessions sets the maximum session count.
func WithMaxSessions(max int64) Option {
	return func(o *Options) { o.MaxSessions = max }
}

// WithSessionTTL sets the session TTL.
func WithSessionTTL(ttl time.Duration) Option {
	return func(o *Options) { o.SessionTTL = ttl }
}

// WithAckTimeout sets the ACK timeout and max retransmit count.
func WithAckTimeout(timeout time.Duration, maxRetransmit int) Option {
	return func(o *Options) {
		o.AckTimeout = timeout
		o.MaxRetransmit = maxRetransmit
	}
}

// WithPlugins adds protocol-level plugins.
func WithPlugins(p ...types.Plugin) Option {
	return func(o *Options) { o.Plugins = append(o.Plugins, p...) }
}
