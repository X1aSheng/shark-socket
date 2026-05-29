package plugin

import (
	"context"
	"sync"
	"time"

	"github.com/X1aSheng/shark-socket-new/internal/core"
)

type Heartbeat struct {
	core.BasePlugin
	manager core.SessionManager
	timeout time.Duration
	ticker  *time.Ticker
	stop    chan struct{}
	once    sync.Once
}

func NewHeartbeat(manager core.SessionManager, timeout time.Duration) *Heartbeat {
	if timeout <= 0 {
		timeout = time.Minute
	}
	return &Heartbeat{manager: manager, timeout: timeout, stop: make(chan struct{})}
}

func (p *Heartbeat) Name() string  { return "heartbeat" }
func (p *Heartbeat) Priority() int { return 50 }

func (p *Heartbeat) Start(interval time.Duration) {
	if interval <= 0 {
		interval = p.timeout / 2
	}
	if interval <= 0 {
		interval = time.Second
	}
	p.once.Do(func() {
		p.ticker = time.NewTicker(interval)
		go p.loop()
	})
}

func (p *Heartbeat) Stop() {
	if p.ticker != nil {
		p.ticker.Stop()
	}
	select {
	case <-p.stop:
	default:
		close(p.stop)
	}
}

func (p *Heartbeat) Sweep(now time.Time) int {
	if p.manager == nil {
		return 0
	}
	closed := 0
	p.manager.Range(func(sess core.Session) bool {
		if now.Sub(sess.LastActiveAt()) > p.timeout {
			_ = sess.Close(context.Background())
			p.manager.Unregister(sess.ID())
			closed++
		}
		return true
	})
	return closed
}

func (p *Heartbeat) loop() {
	for {
		select {
		case <-p.ticker.C:
			p.Sweep(time.Now())
		case <-p.stop:
			return
		}
	}
}
