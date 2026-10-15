package plugin

import (
	"context"
	"errors"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type Heartbeat struct {
	core.BasePlugin
	manager  core.SessionManager
	timeout  time.Duration
	interval time.Duration
	// closeTimeout bounds a single session Close from the sweep goroutine.
	closeTimeout time.Duration
	lc           lifecycle
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
	return &Heartbeat{manager: manager, timeout: timeout, interval: interval, closeTimeout: defaultSweepCloseTimeout}
}

// WithSweepCloseTimeout overrides how long a single session Close may take
// during a sweep. Values <= 0 leave the default in place.
func (p *Heartbeat) WithSweepCloseTimeout(d time.Duration) *Heartbeat {
	if d > 0 {
		p.closeTimeout = d
	}
	return p
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
			if p.safeSessionClose(sess) == nil {
				closed++
			}
			p.manager.Unregister(sess.ID())
		}
	}
	return closed
}

// defaultSweepCloseTimeout bounds a single session Close during a sweep. Close
// runs on the sweep goroutine and Stop waits for that goroutine, so an
// unbounded Close would hang plugin shutdown and, through it, Gateway.Stop
// (which holds the gateway mutex for the whole shutdown).
const defaultSweepCloseTimeout = 5 * time.Second

// errSweepCloseTimeout reports that a session Close exceeded the sweep budget.
var errSweepCloseTimeout = errors.New("heartbeat: session close timed out")

// safeSessionClose recovers a panic from a user session's Close method and
// bounds how long it may take.
func (p *Heartbeat) safeSessionClose(sess core.Session) (err error) {
	timeout := p.closeTimeout
	if timeout <= 0 {
		timeout = defaultSweepCloseTimeout
	}
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- core.ErrHandlerPanic
			}
		}()
		done <- sess.Close(context.Background())
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err = <-done:
		return err
	case <-timer.C:
		// The session implementation ignored cancellation; abandon the wait so
		// the sweep (and shutdown) can proceed. The abandoned goroutine exits
		// whenever the session implementation eventually returns.
		return errSweepCloseTimeout
	}
}
