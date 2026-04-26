package plugin

import "time"

// Common option helpers for plugin construction.

// BlacklistOption is a functional option for BlacklistPlugin.
type BlacklistOption func(*BlacklistPlugin)

// RateLimitOption is a functional option for RateLimitPlugin.
// (defined in ratelimit.go)

// HeartbeatOption is a functional option for HeartbeatPlugin.
type HeartbeatOption func(*HeartbeatPlugin)

// WithHeartbeatTimeout overrides the heartbeat timeout.
func WithHeartbeatTimeout(d time.Duration) HeartbeatOption {
	return func(p *HeartbeatPlugin) { p.timeout = d }
}

// WithHeartbeatInterval overrides the heartbeat interval.
func WithHeartbeatInterval(d time.Duration) HeartbeatOption {
	return func(p *HeartbeatPlugin) { p.interval = d }
}
