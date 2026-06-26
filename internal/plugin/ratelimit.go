package plugin

import (
	"net"
	"sync"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type RateLimit struct {
	core.BasePlugin
	mu       sync.Mutex
	rate     int
	window   time.Duration
	counters map[string]counter
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

type counter struct {
	start time.Time
	count int
}

func NewRateLimit(rate int, window time.Duration) *RateLimit {
	if rate <= 0 {
		rate = 1
	}
	if window <= 0 {
		window = time.Second
	}
	return &RateLimit{rate: rate, window: window, counters: make(map[string]counter), stopCh: make(chan struct{})}
}

func (p *RateLimit) Name() string  { return "ratelimit" }
func (p *RateLimit) Priority() int { return 10 }

// Start begins periodic cleanup of stale counter entries.
func (p *RateLimit) Start() error {
	// Recreate stop channel if previously closed (supports restart after Stop).
	select {
	case <-p.stopCh:
		p.stopCh = make(chan struct{})
		p.stopOnce = sync.Once{}
	default:
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				p.sweep()
			case <-p.stopCh:
				return
			}
		}
	}()
	return nil
}

// Stop terminates the cleanup goroutine.
func (p *RateLimit) Stop() error {
	p.stopOnce.Do(func() { close(p.stopCh) })
	p.wg.Wait()
	return nil
}

func (p *RateLimit) sweep() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, c := range p.counters {
		if time.Since(c.start) >= p.window*2 {
			delete(p.counters, k)
		}
	}
}

func (p *RateLimit) OnMessage(sess core.Session, data []byte) ([]byte, error) {
	key := "unknown"
	if sess.RemoteAddr() != nil {
		host, _, err := net.SplitHostPort(sess.RemoteAddr().String())
		if err != nil {
			host = sess.RemoteAddr().String()
		}
		key = host
	}
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	c := p.counters[key]
	if c.start.IsZero() || now.Sub(c.start) >= p.window {
		c = counter{start: now}
	}
	c.count++
	p.counters[key] = c
	if c.count > p.rate {
		return nil, core.ErrPluginDrop
	}
	return data, nil
}
