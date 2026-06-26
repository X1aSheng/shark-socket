package plugin

import (
	"net"
	"sync"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type AutoBan struct {
	core.BasePlugin
	mu        sync.Mutex
	threshold int
	counts    map[string]int
	banned    map[string]struct{}
	stopCh    chan struct{}
	stopOnce  sync.Once
	wg        sync.WaitGroup
}

func NewAutoBan(threshold int) *AutoBan {
	if threshold <= 0 {
		threshold = 3
	}
	return &AutoBan{threshold: threshold, counts: make(map[string]int), banned: make(map[string]struct{}), stopCh: make(chan struct{})}
}

func (p *AutoBan) Name() string  { return "autoban" }
func (p *AutoBan) Priority() int { return 5 }

// Start begins periodic cleanup of stale banned entries.
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
		ticker := time.NewTicker(30 * time.Minute)
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

func (p *AutoBan) sweep() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.counts = make(map[string]int)
	p.banned = make(map[string]struct{})
}

func (p *AutoBan) OnAccept(sess core.Session) error {
	key := remoteKey(sess)
	p.mu.Lock()
	_, banned := p.banned[key]
	p.mu.Unlock()
	if banned {
		return core.ErrPluginBlock
	}
	return nil
}

func (p *AutoBan) Record(sess core.Session) bool {
	key := remoteKey(sess)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.counts[key]++
	if p.counts[key] >= p.threshold {
		p.banned[key] = struct{}{}
		return true
	}
	return false
}

func (p *AutoBan) Banned(addr string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.banned[addr]
	return ok
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
