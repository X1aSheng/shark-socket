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
	counters map[string][]time.Time // sliding window: timestamps of recent requests
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func NewRateLimit(rate int, window time.Duration) *RateLimit {
	if rate <= 0 {
		rate = 1
	}
	if window <= 0 {
		window = time.Second
	}
	return &RateLimit{rate: rate, window: window, counters: make(map[string][]time.Time), stopCh: make(chan struct{})}
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
	cutoff := time.Now().Add(-p.window * 2)
	for k, stamps := range p.counters {
		// Remove timestamps older than 2 windows
		i := 0
		for i < len(stamps) && stamps[i].Before(cutoff) {
			i++
		}
		if i >= len(stamps) {
			delete(p.counters, k)
		} else if i > 0 {
			p.counters[k] = stamps[i:]
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
	cutoff := now.Add(-p.window)
	p.mu.Lock()
	defer p.mu.Unlock()

	stamps := p.counters[key]
	// Remove expired timestamps (true sliding window)
	i := 0
	for i < len(stamps) && stamps[i].Before(cutoff) {
		i++
	}
	stamps = stamps[i:]
	// Add current request timestamp (sorted by time, so append is correct)
	stamps = append(stamps, now)
	p.counters[key] = stamps

	if len(stamps) > p.rate {
		return nil, core.ErrPluginDrop
	}
	return data, nil
}
