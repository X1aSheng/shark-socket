package shared

import (
	"context"
	"errors"
	"net"
	"time"
)

// ErrReusePortUnsupported is returned when ReusePort is requested on a platform
// where SO_REUSEPORT is not available.
var ErrReusePortUnsupported = errors.New("shared: SO_REUSEPORT is not supported on this platform")

// ListenOptions configures how a transport creates its TCP listener. The zero
// value reproduces exactly what net.Listen does, so adopting this helper does
// not change behaviour until an option is set.
type ListenOptions struct {
	// ReusePort sets SO_REUSEPORT, letting several processes (or several
	// servers in one process) bind the same address while the kernel spreads
	// incoming connections across them. Without it, a single listener and its
	// single accept goroutine are a hard per-port scaling ceiling: adding cores
	// does not add accept capacity.
	//
	// It changes the failure mode of a duplicate bind from "address in use" to
	// "both start, traffic is split". That is the point, but it also means a
	// misconfigured deployment stops failing loudly, so it stays off by
	// default.
	ReusePort bool
	// KeepAliveIdle is how long a connection may sit idle before the first
	// keepalive probe. 0 keeps the platform default (15s via net.ListenConfig);
	// a negative value disables keepalive probes entirely.
	//
	// This matters for IoT: the default closes nothing, but a device behind a
	// NAT whose mapping expires needs a probe to notice a dead path, and a
	// device that is merely quiet must not be probed so aggressively that the
	// radio wakes up. Neither is expressible with one global default.
	KeepAliveIdle time.Duration
	// KeepAliveInterval is the gap between probes once they start, and
	// KeepAliveCount is how many unanswered probes close the connection.
	// Zero keeps the platform default.
	KeepAliveInterval time.Duration
	KeepAliveCount    int
}

// keepAliveConfig translates the fields above into the net package's form.
// Returns nil when every field is zero, so the listener keeps behaving exactly
// like net.Listen rather than depending on what a zero KeepAliveConfig means.
func (o ListenOptions) keepAliveConfig() *net.KeepAliveConfig {
	if o.KeepAliveIdle == 0 && o.KeepAliveInterval == 0 && o.KeepAliveCount == 0 {
		return nil
	}
	ka := &net.KeepAliveConfig{Enable: o.KeepAliveIdle >= 0}
	if o.KeepAliveIdle > 0 {
		ka.Idle = o.KeepAliveIdle
	}
	if o.KeepAliveInterval > 0 {
		ka.Interval = o.KeepAliveInterval
	}
	if o.KeepAliveCount > 0 {
		ka.Count = o.KeepAliveCount
	}
	return ka
}

// Listen creates a TCP listener with the given options.
func Listen(network, addr string, opts ListenOptions) (net.Listener, error) {
	lc := net.ListenConfig{Control: listenControl(opts.ReusePort)}
	if ka := opts.keepAliveConfig(); ka != nil {
		lc.KeepAliveConfig = *ka
	}
	return lc.Listen(context.Background(), network, addr)
}
