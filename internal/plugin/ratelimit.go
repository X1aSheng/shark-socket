package plugin

import (
	"net"
	"sync"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type RateLimit struct {
	core.BasePlugin
	mu       sync.Mutex // guards counters
	rate     int
	window   time.Duration
	counters map[string][]time.Time // sliding window: timestamps of recent requests
	lc       lifecycle
}

func NewRateLimit(rate int, window time.Duration) *RateLimit {
	if rate <= 0 {
		rate = 1
	}
	if window <= 0 {
		window = time.Second
	}
	return &RateLimit{rate: rate, window: window, counters: make(map[string][]time.Time)}
}

func (p *RateLimit) Name() string  { return "ratelimit" }
func (p *RateLimit) Priority() int { return 10 }

// Start begins periodic cleanup of stale counter entries.
func (p *RateLimit) Start() error {
	stop, ok := p.lc.begin()
	if !ok {
		return nil
	}
	go func() {
		defer p.lc.done()
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				p.sweep()
			case <-stop:
				return
			}
		}
	}()
	return nil
}

// Stop terminates the cleanup goroutine.
func (p *RateLimit) Stop() error {
	p.lc.shutdown()
	return nil
}

func (p *RateLimit) sweep() {
	p.mu.Lock()
	defer p.mu.Unlock()
	cutoff := time.Now().Add(-p.window)
	for k, stamps := range p.counters {
		// Remove timestamps older than one window (matches OnMessage pruning)
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
	// Only accepted requests are recorded, so a per-key slice stays bounded by
	// rate: a flooding peer cannot grow it without bound (previously every
	// request, including those over the limit, appended a timestamp).
	if len(stamps) >= p.rate {
		p.counters[key] = stamps
		return nil, core.ErrPluginDrop
	}
	stamps = append(stamps, now)
	p.counters[key] = stamps
	return data, nil
}
