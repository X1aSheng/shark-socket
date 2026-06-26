package plugin

import (
	"net"
	"sync"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type AutoBan struct {
	core.BasePlugin
	mu          sync.Mutex
	threshold   int
	banDuration time.Duration
	counts      map[string]int
	banned      map[string]time.Time // key → ban expiry time
	stopCh      chan struct{}
	stopOnce    sync.Once
	wg          sync.WaitGroup
}

func NewAutoBan(threshold int) *AutoBan {
	if threshold <= 0 {
		threshold = 3
	}
	return &AutoBan{
		threshold:   threshold,
		banDuration: 30 * time.Minute,
		counts:      make(map[string]int),
		banned:      make(map[string]time.Time),
		stopCh:      make(chan struct{}),
	}
}

func (p *AutoBan) Name() string  { return "autoban" }
func (p *AutoBan) Priority() int { return 5 }

// Start begins periodic cleanup of expired bans and stale counters.
func (p *AutoBan) Start() error {
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
func (p *AutoBan) Stop() error {
	p.stopOnce.Do(func() { close(p.stopCh) })
	p.wg.Wait()
	return nil
}

// sweep removes expired bans and stale counters.
// Bans expire after banDuration; counters expire after 2x banDuration.
func (p *AutoBan) sweep() {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for k, expiry := range p.banned {
		if now.After(expiry) {
			delete(p.banned, k)
		}
	}
	// Clean counters for non-banned IPs that are stale
	for k := range p.counts {
		if _, banned := p.banned[k]; !banned {
			delete(p.counts, k)
		}
	}
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
	p.counts[key]++
	if p.counts[key] >= p.threshold {
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
