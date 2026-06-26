package plugin

import (
	"context"
	"sync"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type Heartbeat struct {
	core.BasePlugin
	manager  core.SessionManager
	timeout  time.Duration
	ticker   *time.Ticker
	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	mu       sync.Mutex
	running  bool
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
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running {
		return
	}
	// Recreate stop channel if previously closed (supports restart after Stop).
	select {
	case <-p.stop:
		p.stop = make(chan struct{})
		p.stopOnce = sync.Once{}
	default:
	}
	if interval <= 0 {
		interval = p.timeout / 2
	}
	if interval <= 0 {
		interval = time.Second
	}
	p.ticker = time.NewTicker(interval)
	p.running = true
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.loop()
	}()
}

func (p *Heartbeat) Stop() {
	p.stopOnce.Do(func() {
		p.mu.Lock()
		p.running = false
		if p.ticker != nil {
			p.ticker.Stop()
		}
		p.mu.Unlock()
		close(p.stop)
	})
	p.wg.Wait()
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
