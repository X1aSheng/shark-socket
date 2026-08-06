package plugin

import (
	"context"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type Heartbeat struct {
	core.BasePlugin
	manager core.SessionManager
	timeout time.Duration
	lc      lifecycle
}

func NewHeartbeat(manager core.SessionManager, timeout time.Duration) *Heartbeat {
	if timeout <= 0 {
		timeout = time.Minute
	}
	return &Heartbeat{manager: manager, timeout: timeout}
}

func (p *Heartbeat) Name() string  { return "heartbeat" }
func (p *Heartbeat) Priority() int { return 50 }

// Start begins the sweep loop. Repeated calls are no-ops; the plugin can be
// restarted after Stop.
func (p *Heartbeat) Start(interval time.Duration) {
	stop, ok := p.lc.begin()
	if !ok {
		return
	}
	if interval <= 0 {
		interval = p.timeout / 2
	}
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	go func() {
		defer p.lc.done()
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				p.Sweep(time.Now())
			case <-stop:
				return
			}
		}
	}()
}

// Stop terminates the sweep loop. Repeated calls are no-ops.
func (p *Heartbeat) Stop() {
	p.lc.shutdown()
}

func (p *Heartbeat) Sweep(now time.Time) int {
	if p.manager == nil {
		return 0
	}
	closed := 0
	for _, sess := range p.manager.Snapshot() {
		if now.Sub(sess.LastActiveAt()) > p.timeout {
			_ = sess.Close(context.Background())
			p.manager.Unregister(sess.ID())
			closed++
		}
	}
	return closed
}
