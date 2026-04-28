package plugin

import (
	"net"
	"sync"
	"time"

	"github.com/X1aSheng/shark-socket/internal/errs"
	"github.com/X1aSheng/shark-socket/internal/infra/cache"
	"github.com/X1aSheng/shark-socket/internal/types"
	"github.com/X1aSheng/shark-socket/internal/utils"
)

type expireEntry struct {
	expiry time.Time
}

func (e expireEntry) isExpired() bool {
	return !e.expiry.IsZero() && time.Now().After(e.expiry)
}

// BlacklistPlugin blocks connections from blacklisted IPs/CIDRs.
type BlacklistPlugin struct {
	mu       sync.RWMutex
	exactMap map[string]expireEntry
	cidrList []net.IPNet
	extCache cache.Cache
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewBlacklistPlugin creates a new BlacklistPlugin with optional initial IPs.
func NewBlacklistPlugin(ips ...string) *BlacklistPlugin {
	p := &BlacklistPlugin{
		exactMap: make(map[string]expireEntry),
		stopCh:   make(chan struct{}),
	}
	for _, ip := range ips {
		p.Add(ip, 0)
	}
	p.wg.Add(1)
	go p.cleanupLoop()
	return p
}

// WithCache sets an external cache for distributed blacklist lookups.
func (p *BlacklistPlugin) WithCache(c cache.Cache) *BlacklistPlugin {
	p.extCache = c
	return p
}

func (p *BlacklistPlugin) Name() string             { return "blacklist" }
func (p *BlacklistPlugin) Priority() int            { return 0 }
func (p *BlacklistPlugin) OnClose(types.RawSession) {}

func (p *BlacklistPlugin) OnAccept(sess types.RawSession) error {
	ip := utils.ExtractIPFromAddr(sess.RemoteAddr())
	key := utils.IPToKey(ip)
	if p.isBlocked(key) {
		return errs.ErrBlock
	}
	return nil
}

func (p *BlacklistPlugin) OnMessage(sess types.RawSession, data []byte) ([]byte, error) {
	return data, nil
}

// Add adds an IP or CIDR to the blacklist with an optional TTL.
func (p *BlacklistPlugin) Add(ipStr string, ttl time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()

	_, ipnet, err := net.ParseCIDR(ipStr)
	if err == nil {
		p.cidrList = append(p.cidrList, *ipnet)
		return
	}

	ip := net.ParseIP(ipStr)
	if ip != nil {
		key := utils.IPToKey(ip)
		var entry expireEntry
		if ttl > 0 {
			entry.expiry = time.Now().Add(ttl)
		}
		p.exactMap[key] = entry
	}
}

// Remove removes an IP from the blacklist.
func (p *BlacklistPlugin) Remove(ipStr string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ip := net.ParseIP(ipStr)
	if ip != nil {
		delete(p.exactMap, utils.IPToKey(ip))
	}
}

// Reload replaces the blacklist with a new list.
func (p *BlacklistPlugin) Reload(ips []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exactMap = make(map[string]expireEntry)
	p.cidrList = p.cidrList[:0]
	for _, ip := range ips {
		_, ipnet, err := net.ParseCIDR(ip)
		if err == nil {
			p.cidrList = append(p.cidrList, *ipnet)
			continue
		}
		if parsed := net.ParseIP(ip); parsed != nil {
			p.exactMap[utils.IPToKey(parsed)] = expireEntry{}
		}
	}
}

func (p *BlacklistPlugin) isBlocked(key string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if e, ok := p.exactMap[key]; ok && !e.isExpired() {
		return true
	}

	ip := net.ParseIP(key)
	for _, cidr := range p.cidrList {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

func (p *BlacklistPlugin) cleanupLoop() {
	defer p.wg.Done()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.mu.Lock()
			now := time.Now()
			for k, v := range p.exactMap {
				if !v.expiry.IsZero() && now.After(v.expiry) {
					delete(p.exactMap, k)
				}
			}
			p.mu.Unlock()
		case <-p.stopCh:
			return
		}
	}
}

// Close stops the background cleanup goroutine and waits for it to exit.
func (p *BlacklistPlugin) Close() {
	close(p.stopCh)
	p.wg.Wait()
}
