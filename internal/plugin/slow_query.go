package plugin

import (
	"log"
	"time"

	"github.com/X1aSheng/shark-socket/internal/types"
)

// SlowQueryConfig configures slow query logging behavior.
type SlowQueryConfig struct {
	// Threshold is the minimum duration to consider a query "slow".
	// Default is 100ms. Messages exceeding this are logged.
	Threshold time.Duration
	// Enabled controls whether slow query logging is active.
	Enabled bool
	// IncludePayload enables logging of message payload (may contain sensitive data).
	IncludePayload bool
}

// SlowQueryPlugin logs slow messages as they pass through the chain.
// This plugin has high priority (runs early in the chain).
type SlowQueryPlugin struct {
	types.BasePlugin
	cfg SlowQueryConfig
}

// NewSlowQueryPlugin creates a slow query logging plugin with the given config.
func NewSlowQueryPlugin(cfg SlowQueryConfig) *SlowQueryPlugin {
	if cfg.Threshold == 0 {
		cfg.Threshold = 100 * time.Millisecond
	}
	return &SlowQueryPlugin{
		cfg: cfg,
	}
}

// Name returns "slow-query".
func (p *SlowQueryPlugin) Name() string { return "slow-query" }

// Priority returns -10 so it runs early in the chain.
func (p *SlowQueryPlugin) Priority() int { return -10 }

const slowQueryStartKey = "slow_query_start"

// OnAccept records the session start time for slow query detection.
func (p *SlowQueryPlugin) OnAccept(sess types.RawSession) error {
	if p.cfg.Enabled {
		sess.SetMeta(slowQueryStartKey, time.Now())
	}
	return nil
}

// OnMessage records a per-message timestamp for idle detection.
func (p *SlowQueryPlugin) OnMessage(sess types.RawSession, data []byte) ([]byte, error) {
	if p.cfg.Enabled {
		sess.SetMeta(slowQueryStartKey, time.Now())
	}
	return data, nil
}

// OnClose calculates total session duration and logs if it exceeds the threshold.
func (p *SlowQueryPlugin) OnClose(sess types.RawSession) {
	if !p.cfg.Enabled {
		return
	}
	start, ok := sess.GetMeta(slowQueryStartKey)
	if !ok {
		return
	}
	startTime, ok := start.(time.Time)
	if !ok {
		return
	}
	elapsed := time.Since(startTime)
	if elapsed > p.cfg.Threshold {
		p.LogSlow(sess, elapsed, 0)
	}
}

// SetThreshold updates the slow query threshold for both the plugin and the chain.
func (p *SlowQueryPlugin) SetThreshold(d time.Duration) {
	p.cfg.Threshold = d
}

// IsSlowQuery returns true if the given duration exceeds the threshold.
func (p *SlowQueryPlugin) IsSlowQuery(d time.Duration) bool {
	return d > p.cfg.Threshold
}

// LogSlow logs a slow query event with the given parameters.
func (p *SlowQueryPlugin) LogSlow(sess types.RawSession, duration time.Duration, size int) {
	if !p.cfg.Enabled {
		return
	}

	if p.cfg.IncludePayload {
		log.Printf("[slow-query] session=%d proto=%v duration=%v size=%d",
			sess.ID(), sess.Protocol(), duration, size)
	} else {
		log.Printf("[slow-query] session=%d proto=%v duration=%v size=%d",
			sess.ID(), sess.Protocol(), duration, size)
	}
}
