package plugin

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket/internal/types"
	"github.com/X1aSheng/shark-socket/internal/utils"
)

type ipCounters struct {
	rateLimitHits  atomic.Int64
	protocolErrors atomic.Int64
	totalConns     atomic.Int64
	lastUpdate     atomic.Int64 // UnixNano
}

// AutoBanThresholds configures when an IP gets auto-banned.
type AutoBanThresholds struct {
	RateLimitThreshold     int64
	ProtocolErrorThreshold int64
	EmptyConnThreshold     int64
	BanTTL                 time.Duration
}

// DefaultAutoBanThresholds returns sensible defaults.
func DefaultAutoBanThresholds() AutoBanThresholds {
	return AutoBanThresholds{
		RateLimitThreshold:     10,
		ProtocolErrorThreshold: 5,
		EmptyConnThreshold:     1000, // connections, not just empty ones
		BanTTL:                 30 * time.Minute,
	}
}

// AutoBanPlugin automatically bans IPs that exceed violation thresholds.
type AutoBanPlugin struct {
	blacklist  *BlacklistPlugin
	thresholds AutoBanThresholds
	counters   sync.Map // string -> *ipCounters
	stopCh     chan struct{}
	wg         sync.WaitGroup
}

// NewAutoBanPlugin creates a new auto-ban plugin.
func NewAutoBanPlugin(blacklist *BlacklistPlugin, opts ...AutoBanOption) *AutoBanPlugin {
	p := &AutoBanPlugin{
		blacklist:  blacklist,
		thresholds: DefaultAutoBanThresholds(),
		stopCh:     make(chan struct{}),
	}
	for _, opt := range opts {
		opt(p)
	}
	p.wg.Add(1)
	go p.cleanupLoop()
	return p
}

// AutoBanOption configures the AutoBanPlugin.
type AutoBanOption func(*AutoBanPlugin)

// WithAutoBanThresholds sets custom thresholds.
func WithAutoBanThresholds(t AutoBanThresholds) AutoBanOption {
	return func(p *AutoBanPlugin) { p.thresholds = t }
}

func (p *AutoBanPlugin) Name() string  { return "autoban" }
func (p *AutoBanPlugin) Priority() int { return 20 }

func (p *AutoBanPlugin) OnAccept(sess types.RawSession) error {
	ip := utils.ExtractIPFromAddr(sess.RemoteAddr())
	key := utils.IPToKey(ip)
	c := p.getCounters(key)
	c.totalConns.Add(1)
	c.lastUpdate.Store(time.Now().UnixNano())
	p.checkAndBan(key, c)
	return nil
}

func (p *AutoBanPlugin) OnMessage(sess types.RawSession, data []byte) ([]byte, error) {
	ip := utils.ExtractIPFromAddr(sess.RemoteAddr())
	key := utils.IPToKey(ip)
	c := p.getCounters(key)
	c.lastUpdate.Store(time.Now().UnixNano())
	p.checkAndBan(key, c)
	return data, nil
}

func (p *AutoBanPlugin) OnClose(types.RawSession) {}

func (p *AutoBanPlugin) getCounters(key string) *ipCounters {
	val, _ := p.counters.LoadOrStore(key, &ipCounters{})
	return val.(*ipCounters)
}

func (p *AutoBanPlugin) checkAndBan(key string, c *ipCounters) {
	if c.rateLimitHits.Load() >= p.thresholds.RateLimitThreshold ||
		c.protocolErrors.Load() >= p.thresholds.ProtocolErrorThreshold ||
		c.totalConns.Load() >= p.thresholds.EmptyConnThreshold {
		p.blacklist.Add(key, p.thresholds.BanTTL)
	}
}

// RecordRateLimit records a rate limit violation for an IP.
func (p *AutoBanPlugin) RecordRateLimit(ip string) {
	c := p.getCounters(ip)
	c.rateLimitHits.Add(1)
	c.lastUpdate.Store(time.Now().UnixNano())
}

// RecordProtocolError records a protocol error for an IP.
func (p *AutoBanPlugin) RecordProtocolError(ip string) {
	c := p.getCounters(ip)
	c.protocolErrors.Add(1)
	c.lastUpdate.Store(time.Now().UnixNano())
}

// cleanupLoop periodically removes stale counter entries to prevent unbounded map growth.
func (p *AutoBanPlugin) cleanupLoop() {
	defer p.wg.Done()
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-10 * time.Minute).UnixNano()
			p.counters.Range(func(key, val any) bool {
				c := val.(*ipCounters)
				if c.lastUpdate.Load() < cutoff {
					p.counters.Delete(key)
				}
				return true
			})
		case <-p.stopCh:
			return
		}
	}
}

// Close stops the cleanup goroutine.
func (p *AutoBanPlugin) Close() {
	close(p.stopCh)
	p.wg.Wait()
}
