package gateway

import (
	"time"

	"github.com/X1aSheng/shark-socket/internal/types"
)

// Options holds gateway configuration.
type Options struct {
	ShutdownTimeout time.Duration
	MetricsAddr     string
	EnableMetrics   bool
	GlobalPlugins   []types.Plugin
}

func defaultOptions() Options {
	return Options{
		ShutdownTimeout: 15 * time.Second,
		MetricsAddr:     ":9090",
		EnableMetrics:   true,
	}
}

// Option is a functional option for Gateway.
type Option func(*Options)

// WithShutdownTimeout sets the graceful shutdown timeout.
func WithShutdownTimeout(d time.Duration) Option {
	return func(o *Options) { o.ShutdownTimeout = d }
}

// WithMetricsAddr sets the metrics HTTP server address.
func WithMetricsAddr(addr string) Option {
	return func(o *Options) { o.MetricsAddr = addr }
}

// WithMetricsEnabled enables or disables the metrics server.
func WithMetricsEnabled(enabled bool) Option {
	return func(o *Options) { o.EnableMetrics = enabled }
}

// WithGlobalPlugins sets plugins applied to all protocols.
func WithGlobalPlugins(p ...types.Plugin) Option {
	return func(o *Options) { o.GlobalPlugins = append(o.GlobalPlugins, p...) }
}
