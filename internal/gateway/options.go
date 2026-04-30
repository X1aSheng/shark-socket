package gateway

import (
	"time"

	"github.com/X1aSheng/shark-socket/internal/types"
)

// Options holds gateway configuration.
type Options struct {
	// StageTimeouts defines per-stage shutdown timeouts.
	// Stages (in order): StopAccept → Drain → SessionClose → MetricsClose → Finalize
	StageTimeouts StageTimeouts

	MetricsAddr   string
	EnableMetrics bool
	GlobalPlugins []types.Plugin

	// ConfigPath enables configuration hot reload from a JSON file.
	ConfigPath string
	// ConfigReloadInterval is the interval for checking config file changes.
	ConfigReloadInterval time.Duration
}

// StageTimeouts defines timeouts for each shutdown stage.
type StageTimeouts struct {
	// StopAccept is the timeout for stopping new connections (stage 1).
	StopAccept time.Duration
	// Drain is the timeout for draining in-flight messages (stage 2).
	Drain time.Duration
	// SessionClose is the timeout for closing active sessions (stage 3).
	SessionClose time.Duration
	// ManagerClose is the timeout for closing session manager (stage 4).
	ManagerClose time.Duration
	// MetricsClose is the timeout for closing metrics server (stage 5).
	MetricsClose time.Duration
	// Finalize is the timeout for final cleanup (stage 6).
	Finalize time.Duration
}

func defaultOptions() Options {
	return Options{
		StageTimeouts: StageTimeouts{
			StopAccept:   5 * time.Second,
			Drain:        5 * time.Second,
			SessionClose: 10 * time.Second,
			ManagerClose: 5 * time.Second,
			MetricsClose: 5 * time.Second,
			Finalize:     2 * time.Second,
		},
		MetricsAddr:   ":9090",
		EnableMetrics: true,
	}
}

// Option is a functional option for Gateway.
type Option func(*Options)

// WithShutdownTimeout sets the overall graceful shutdown timeout.
// This sets all stage timeouts to the same value as a convenience.
func WithShutdownTimeout(d time.Duration) Option {
	return func(o *Options) {
		o.StageTimeouts = StageTimeouts{
			StopAccept:   d / 3,
			Drain:        d / 3,
			SessionClose: d / 3,
			ManagerClose: d / 6,
			MetricsClose: d / 6,
			Finalize:     d / 6,
		}
	}
}

// WithStageTimeouts sets per-stage shutdown timeouts.
func WithStageTimeouts(st StageTimeouts) Option {
	return func(o *Options) { o.StageTimeouts = st }
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
	return func(o *Options) {
		o.GlobalPlugins = append(append([]types.Plugin{}, o.GlobalPlugins...), p...)
	}
}
