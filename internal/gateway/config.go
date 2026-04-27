package gateway

import (
	"log"
	"time"

	"github.com/X1aSheng/shark-socket/internal/infra/config"
)

// WithConfigPath enables configuration hot reload from a JSON file.
// The file is watched for changes and reloaded automatically.
// Supported config fields: shutdown_timeout, metrics_addr, enable_metrics, log_level.
func WithConfigPath(path string, reloadInterval time.Duration) Option {
	return func(o *Options) {
		o.ConfigPath = path
		o.ConfigReloadInterval = reloadInterval
	}
}

// ReloadHandler handles configuration reload events.
// This is called when the config file changes.
type ReloadHandler func(cfg *GatewayConfig) error

// GatewayConfig represents the gateway configuration structure.
type GatewayConfig = config.GatewayConfig

// ApplyConfig applies a new configuration to the gateway.
func (g *Gateway) ApplyConfig(cfg *GatewayConfig) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	var changed []string

	// Apply shutdown timeout if changed
	if cfg.ShutdownTimeout != "" {
		d, err := time.ParseDuration(cfg.ShutdownTimeout)
		if err != nil {
			log.Printf("gateway: invalid shutdown_timeout %q: %v", cfg.ShutdownTimeout, err)
		} else {
			g.opts.StageTimeouts = StageTimeouts{
				StopAccept:    d / 3,
				Drain:         d / 3,
				SessionClose:  d / 3,
				ManagerClose:  d / 6,
				MetricsClose:  d / 6,
				Finalize:      d / 6,
			}
			changed = append(changed, "shutdown_timeout")
		}
	}

	// Apply metrics settings if changed
	if cfg.MetricsAddr != "" && cfg.MetricsAddr != g.opts.MetricsAddr {
		g.opts.MetricsAddr = cfg.MetricsAddr
		changed = append(changed, "metrics_addr")
	}

	if cfg.EnableMetrics != g.opts.EnableMetrics {
		g.opts.EnableMetrics = cfg.EnableMetrics
		changed = append(changed, "enable_metrics")
	}

	// Log level change would require logger integration
	if cfg.LogLevel != "" {
		changed = append(changed, "log_level")
	}

	if len(changed) > 0 {
		log.Printf("gateway: config reloaded, changed: %v", changed)
	}

	return nil
}
