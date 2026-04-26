package udp

import (
	"fmt"
	"time"

	"github.com/X1aSheng/shark-socket/internal/types"
)

// Options holds UDP server configuration.
type Options struct {
	Host        string
	Port        int
	SessionTTL  time.Duration
	MaxSessions int64
	Plugins     []types.Plugin
}

func defaultOptions() Options {
	return Options{
		Host:        "0.0.0.0",
		Port:        18200,
		SessionTTL:  60 * time.Second,
		MaxSessions: 100000,
	}
}

// Addr returns the listen address.
func (o Options) Addr() string {
	return fmt.Sprintf("%s:%d", o.Host, o.Port)
}

// Option is a functional option for UDP server.
type Option func(*Options)

// WithAddr sets the listen address.
func WithAddr(host string, port int) Option {
	return func(o *Options) { o.Host = host; o.Port = port }
}

// WithSessionTTL sets the session idle TTL.
func WithSessionTTL(ttl time.Duration) Option {
	return func(o *Options) { o.SessionTTL = ttl }
}

// WithMaxSessions sets the maximum session count.
func WithMaxSessions(max int64) Option {
	return func(o *Options) { o.MaxSessions = max }
}

// WithPlugins adds protocol-level plugins.
func WithPlugins(p ...types.Plugin) Option {
	return func(o *Options) { o.Plugins = append(o.Plugins, p...) }
}
