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
	return &RateLimit{rate: rate, window: window, counters: make(map[string]counter)}
}

func (p *RateLimit) Name() string  { return "ratelimit" }
func (p *RateLimit) Priority() int { return 10 }

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
