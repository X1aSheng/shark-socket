package plugin

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket/internal/errs"
	"github.com/X1aSheng/shark-socket/internal/types"
	"github.com/X1aSheng/shark-socket/internal/utils"
)

type tokenBucket struct {
	mu       sync.Mutex
	tokens   atomic.Int64
	lastFill time.Time // protected by mu
	rate     float64
	maxBurst int64
	lastUsed atomic.Int64
}

func newTokenBucket(rate float64, burst int64) *tokenBucket {
	now := time.Now()
	tb := &tokenBucket{
		rate:     rate,
		maxBurst: burst,
		lastFill: now,
	}
	tb.tokens.Store(burst)
	tb.lastUsed.Store(now.UnixNano())
	return tb
}

func (tb *tokenBucket) allow() bool {
	now := time.Now()

	// Refill under mutex to prevent concurrent replenishment from exceeding maxBurst.
	tb.mu.Lock()
	elapsed := now.Sub(tb.lastFill).Seconds()
	if elapsed > 0 {
		newTokens := int64(elapsed * tb.rate)
		if newTokens > 0 {
			refilled := tb.tokens.Load() + newTokens
			if refilled > tb.maxBurst {
				refilled = tb.maxBurst
			}
			tb.tokens.Store(refilled)
			tb.lastFill = now
		}
	}
	tb.mu.Unlock()

	tb.lastUsed.Store(now.UnixNano())

	for {
		current := tb.tokens.Load()
		if current <= 0 {
			return false
		}
		if tb.tokens.CompareAndSwap(current, current-1) {
			return true
		}
	}
}

// RateLimitPlugin provides dual-layer token bucket rate limiting.
type RateLimitPlugin struct {
	globalBucket *tokenBucket
	perIPBuckets sync.Map // string -> *tokenBucket
	rate         float64
	burst        float64
	msgRate      float64
	msgBurst     float64
	stopCh       chan struct{}
	wg           sync.WaitGroup

	violations sync.Map // string -> *atomic.Int64
}

// RateLimitOption configures the RateLimitPlugin.
type RateLimitOption func(*RateLimitPlugin)

// WithRateLimitMessageRate sets per-IP message rate limits.
func WithRateLimitMessageRate(rate, burst float64) RateLimitOption {
	return func(p *RateLimitPlugin) { p.msgRate = rate; p.msgBurst = burst }
}

// NewRateLimitPlugin creates a new rate limiter.
func NewRateLimitPlugin(rate, burst float64, opts ...RateLimitOption) *RateLimitPlugin {
	p := &RateLimitPlugin{
		rate:    rate,
		burst:   burst,
		msgRate: rate * 10, // default message rate = 10x connection rate
		msgBurst: burst * 10,
		stopCh:  make(chan struct{}),
	}
	p.globalBucket = newTokenBucket(rate, int64(burst))
	for _, opt := range opts {
		opt(p)
	}
	p.wg.Add(1)
	go p.cleanupLoop()
	return p
}

func (p *RateLimitPlugin) Name() string  { return "ratelimit" }
func (p *RateLimitPlugin) Priority() int { return 10 }

func (p *RateLimitPlugin) OnAccept(sess types.RawSession) error {
	if !p.globalBucket.allow() {
		p.recordViolation(sess)
		return errs.ErrBlock
	}
	ip := utils.ExtractIPFromAddr(sess.RemoteAddr())
	key := utils.IPToKey(ip)
	tb := p.getPerIPBucket(key)
	if !tb.allow() {
		p.recordViolation(sess)
		return errs.ErrBlock
	}
	return nil
}

func (p *RateLimitPlugin) OnMessage(sess types.RawSession, data []byte) ([]byte, error) {
	ip := utils.ExtractIPFromAddr(sess.RemoteAddr())
	key := utils.IPToKey(ip)
	tb := p.getPerIPBucket(key)
	if !tb.allow() {
		p.recordViolation(sess)
		return nil, errs.ErrDrop
	}
	return data, nil
}

func (p *RateLimitPlugin) OnClose(types.RawSession) {}

func (p *RateLimitPlugin) getPerIPBucket(key string) *tokenBucket {
	if val, ok := p.perIPBuckets.Load(key); ok {
		return val.(*tokenBucket)
	}
	tb := newTokenBucket(p.msgRate, int64(p.msgBurst))
	actual, loaded := p.perIPBuckets.LoadOrStore(key, tb)
	if loaded {
		return actual.(*tokenBucket)
	}
	return tb
}

func (p *RateLimitPlugin) recordViolation(sess types.RawSession) {
	ip := utils.ExtractIPFromAddr(sess.RemoteAddr())
	key := utils.IPToKey(ip)
	val, _ := p.violations.LoadOrStore(key, &atomic.Int64{})
	val.(*atomic.Int64).Add(1)
}

// GetViolations returns the violation count for an IP.
func (p *RateLimitPlugin) GetViolations(ip string) int64 {
	val, ok := p.violations.Load(ip)
	if !ok {
		return 0
	}
	return val.(*atomic.Int64).Load()
}

func (p *RateLimitPlugin) cleanupLoop() {
	defer p.wg.Done()
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now().UnixNano()
			idleThreshold := int64(5 * time.Minute)
			p.perIPBuckets.Range(func(key, val any) bool {
				tb := val.(*tokenBucket)
				if now-tb.lastUsed.Load() > idleThreshold {
					p.perIPBuckets.Delete(key)
				}
				return true
			})
			p.violations.Range(func(key, val any) bool {
				p.violations.Delete(key)
				return true
			})
		case <-p.stopCh:
			return
		}
	}
}

// Close stops the cleanup goroutine and waits for it to exit.
func (p *RateLimitPlugin) Close() {
	close(p.stopCh)
	p.wg.Wait()
}
