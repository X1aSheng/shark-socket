package plugin

import (
	"net"
	"sync"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type AutoBan struct {
	core.BasePlugin
	mu        sync.Mutex
	threshold int
	counts    map[string]int
	banned    map[string]struct{}
}

func NewAutoBan(threshold int) *AutoBan {
	if threshold <= 0 {
		threshold = 3
	}
	return &AutoBan{threshold: threshold, counts: make(map[string]int), banned: make(map[string]struct{})}
}

func (p *AutoBan) Name() string  { return "autoban" }
func (p *AutoBan) Priority() int { return 5 }

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
