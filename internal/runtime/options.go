package runtime

import (
	"time"

	"github.com/X1aSheng/shark-socket-new/internal/core"
)

type gatewayConfig struct {
	sessions core.SessionManager
	plugins  core.PluginRunner
	logger   core.Logger
	metrics  core.Metrics
	tracer   core.Tracer
	timeouts core.StageTimeouts
}

type GatewayOption func(*gatewayConfig)

func WithSessionManager(manager core.SessionManager) GatewayOption {
	return func(cfg *gatewayConfig) {
		if manager != nil {
			cfg.sessions = manager
		}
	}
}

func WithPlugins(plugins ...core.Plugin) GatewayOption {
	return func(cfg *gatewayConfig) {
		cfg.plugins = NewPluginChain(plugins...)
	}
}

func WithLogger(logger core.Logger) GatewayOption {
	return func(cfg *gatewayConfig) {
		cfg.logger = logger
	}
}

func WithMetrics(metrics core.Metrics) GatewayOption {
	return func(cfg *gatewayConfig) {
		cfg.metrics = metrics
	}
}

func WithTracer(tracer core.Tracer) GatewayOption {
	return func(cfg *gatewayConfig) {
		cfg.tracer = tracer
	}
}

func WithStageTimeouts(timeouts core.StageTimeouts) GatewayOption {
	return func(cfg *gatewayConfig) {
		cfg.timeouts = normalizeTimeouts(timeouts)
	}
}

func defaultStageTimeouts() core.StageTimeouts {
	return core.StageTimeouts{
		StopAccept:    5 * time.Second,
		Drain:         5 * time.Second,
		CloseSessions: 10 * time.Second,
		Finalize:      2 * time.Second,
	}
}

func normalizeTimeouts(timeouts core.StageTimeouts) core.StageTimeouts {
	defaults := defaultStageTimeouts()
	if timeouts.StopAccept <= 0 {
		timeouts.StopAccept = defaults.StopAccept
	}
	if timeouts.Drain <= 0 {
		timeouts.Drain = defaults.Drain
	}
	if timeouts.CloseSessions <= 0 {
		timeouts.CloseSessions = defaults.CloseSessions
	}
	if timeouts.Finalize <= 0 {
		timeouts.Finalize = defaults.Finalize
	}
	return timeouts
}
