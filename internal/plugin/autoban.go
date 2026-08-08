package plugin

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type autoBanCount struct {
	count int
	last  time.Time
}

type AutoBan struct {
	core.BasePlugin
	mu          sync.Mutex // guards counts/banned
	threshold   int
	banDuration time.Duration
	counts      map[string]autoBanCount
	banned      map[string]time.Time // key → ban expiry time
	lc          lifecycle
}

func NewAutoBan(threshold int) *AutoBan {
	if threshold <= 0 {
		threshold = 3
	}
	return &AutoBan{
		threshold:   threshold,
		banDuration: 30 * time.Minute,
		counts:      make(map[string]autoBanCount),
		banned:      make(map[string]time.Time),
	}
}

func (p *AutoBan) Name() string  { return "autoban" }
func (p *AutoBan) Priority() int { return 5 }

// Start begins periodic cleanup of expired bans and stale counters.
func (p *AutoBan) Start() error {
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
func (p *AutoBan) Stop() error {
	p.lc.shutdown()
	return nil
}

// sweep removes expired bans and stale counters.
// Bans expire after banDuration; a counter is removed only when it has seen no
// activity for banDuration, so a slow client counting toward the threshold is
// not silently forgiven every sweep cycle.
func (p *AutoBan) sweep() {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for k, expiry := range p.banned {
		if now.After(expiry) {
			delete(p.banned, k)
		}
	}
	for k, c := range p.counts {
		if now.Sub(c.last) > p.banDuration {
			delete(p.counts, k)
		}
	}
}

// OnMessage counts every accepted message per remote IP and bans the address
// once the threshold is reached. This is the production call site for Record;
// without it AutoBan never accumulated counts and could not ban anyone.
func (p *AutoBan) OnMessage(sess core.Session, data []byte) ([]byte, error) {
	if p.Record(sess) {
		// Terminate the offending session too: a live connection that just
		// tripped the threshold would otherwise keep its resources until it
		// disconnects on its own (OnAccept only blocks new connections).
		if sess != nil {
			_ = sess.Close(context.Background())
		}
		return nil, core.ErrPluginDrop
	}
	return data, nil
}

func (p *AutoBan) OnAccept(sess core.Session) error {
	key := remoteKey(sess)
	p.mu.Lock()
	expiry, banned := p.banned[key]
	if banned {
		if time.Now().Before(expiry) {
			p.mu.Unlock()
			return core.ErrPluginBlock
		}
		// Ban expired — remove and allow
		delete(p.banned, key)
	}
	p.mu.Unlock()
	return nil
}

func (p *AutoBan) Record(sess core.Session) bool {
	key := remoteKey(sess)
	p.mu.Lock()
	defer p.mu.Unlock()
	c := p.counts[key]
	c.count++
	c.last = time.Now()
	p.counts[key] = c
	if c.count >= p.threshold {
		p.banned[key] = time.Now().Add(p.banDuration)
		delete(p.counts, key)
		return true
	}
	return false
}

func (p *AutoBan) Banned(addr string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	expiry, ok := p.banned[addr]
	if !ok {
		return false
	}
	if time.Now().After(expiry) {
		delete(p.banned, addr)
		return false
	}
	return true
}

func remoteKey(sess core.Session) string {
	if sess == nil || sess.RemoteAddr() == nil {
		return "unknown"
	}
	host, _, err := net.SplitHostPort(sess.RemoteAddr().String())
	if err != nil {
		return sess.RemoteAddr().String()
	}
	return host
}
