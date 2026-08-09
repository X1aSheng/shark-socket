package plugin

import (
	"context"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type Heartbeat struct {
	core.BasePlugin
	manager  core.SessionManager
	timeout  time.Duration
	interval time.Duration
	lc       lifecycle
}

func NewHeartbeat(manager core.SessionManager, timeout time.Duration, interval time.Duration) *Heartbeat {
	if timeout <= 0 {
		timeout = time.Minute
	}
	if interval <= 0 {
		interval = timeout / 2
	}
	if interval <= 0 {
		interval = time.Second
	}
	return &Heartbeat{manager: manager, timeout: timeout, interval: interval}
}

func (p *Heartbeat) Name() string  { return "heartbeat" }
func (p *Heartbeat) Priority() int { return 50 }

// Start begins the sweep loop. Repeated calls are no-ops; the plugin can be
// restarted after Stop.
func (p *Heartbeat) Start() error {
	stop, ok := p.lc.begin()
	if !ok {
		return nil
	}
	ticker := time.NewTicker(p.interval)
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
	return nil
}

// Stop terminates the sweep loop. Repeated calls are no-ops.
func (p *Heartbeat) Stop() error {
	p.lc.shutdown()
	return nil
}

func (p *Heartbeat) Sweep(now time.Time) int {
	if p.manager == nil {
		return 0
	}
	closed := 0
	for _, sess := range p.manager.Snapshot() {
		if now.Sub(sess.LastActiveAt()) > p.timeout {
			// Panic-isolate session Close so a broken session implementation
			// cannot crash the sweep goroutine (and the whole process).
			if safeSessionClose(sess) == nil {
				closed++
			}
			p.manager.Unregister(sess.ID())
		}
	}
	return closed
}

// safeSessionClose recovers a panic from a user session's Close method.
func safeSessionClose(sess core.Session) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = core.ErrHandlerPanic
		}
	}()
	return sess.Close(context.Background())
}
