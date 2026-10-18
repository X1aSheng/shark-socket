package plugin

import (
	"hash/maphash"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// rateLimitShardCount is the number of independent counter shards. Sharding
// removes the single global mutex that previously serialized every message
// from every peer through one lock (a hot-spot at gateway-class throughput).
const rateLimitShardCount = 32

// rateLimitSeed randomizes shard selection per process so an attacker cannot
// pre-compute IPs that collide on one shard to amplify lock contention.
var rateLimitSeed = maphash.MakeSeed()

// rateLimitKeyMeta caches the per-session rate-limit key (remote IP) in
// session meta, so the per-message SplitHostPort string allocation is paid
// once per session instead of once per message. OnMessage calls for a single
// session are serialized by that transport's read loop, so the cache needs no
// locking; sessions whose meta is a no-op simply recompute the key.
const rateLimitKeyMeta = "plugin.ratelimit.ip"

// rateLimitShard holds the sliding-window counters for a subset of peers.
type rateLimitShard struct {
	mu       sync.Mutex // guards counters
	counters map[string][]time.Time
}

type RateLimit struct {
	core.BasePlugin
	shards [rateLimitShardCount]rateLimitShard
	// rate and window are read on every message and written by SetLimit, so
	// they are atomic: a reload must not race with the read path of eight
	// transports. Both are packed into integers rather than a mutex because the
	// read side is the hot path and needs no ordering between the two fields
	// beyond "each is a valid value".
	rate   atomic.Int64
	window atomic.Int64 // nanoseconds
	lc     lifecycle
}

func NewRateLimit(rate int, window time.Duration) *RateLimit {
	p := &RateLimit{}
	p.SetLimit(rate, window)
	for i := range p.shards {
		p.shards[i].counters = make(map[string][]time.Time)
	}
	return p
}

// SetLimit replaces the allowance. Non-positive values fall back to the same
// defaults as NewRateLimit.
//
// Counters are not reset: a tightened limit applies to the messages already
// recorded in the current window (so it takes effect on the next message), and
// a loosened one only becomes visible as old timestamps age out. Resetting the
// counters instead would let a client double its allowance by waiting for a
// reload, which is the opposite of what an operator changing the limit wants.
func (p *RateLimit) SetLimit(rate int, window time.Duration) {
	if rate <= 0 {
		rate = 1
	}
	if window <= 0 {
		window = time.Second
	}
	p.rate.Store(int64(rate))
	p.window.Store(int64(window))
}

// Limit returns the current allowance.
func (p *RateLimit) Limit() (rate int, window time.Duration) {
	return int(p.rate.Load()), time.Duration(p.window.Load())
}

func (p *RateLimit) Name() string  { return "ratelimit" }
func (p *RateLimit) Priority() int { return 10 }

// shardFor maps a peer key to its shard with a process-random seed, so shard
// selection cannot be predicted or deliberately collided by remote peers.
func (p *RateLimit) shardFor(key string) *rateLimitShard {
	var h maphash.Hash
	h.SetSeed(rateLimitSeed)
	_, _ = h.WriteString(key)
	return &p.shards[uint64(h.Sum64())%rateLimitShardCount]
}

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
	_, window := p.Limit()
	cutoff := time.Now().Add(-window)
	for i := range p.shards {
		sh := &p.shards[i]
		sh.mu.Lock()
		for k, stamps := range sh.counters {
			// Remove timestamps older than one window (matches OnMessage pruning)
			j := 0
			for j < len(stamps) && stamps[j].Before(cutoff) {
				j++
			}
			if j >= len(stamps) {
				delete(sh.counters, k)
			} else if j > 0 {
				sh.counters[k] = stamps[j:]
			}
		}
		sh.mu.Unlock()
	}
}

func (p *RateLimit) OnMessage(sess core.Session, data []byte) ([]byte, error) {
	key := p.sessionKey(sess)
	rate, window := p.Limit()
	now := time.Now()
	cutoff := now.Add(-window)
	sh := p.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	stamps := sh.counters[key]
	// Remove expired timestamps (true sliding window)
	i := 0
	for i < len(stamps) && stamps[i].Before(cutoff) {
		i++
	}
	stamps = stamps[i:]
	// Only accepted requests are recorded, so a per-key slice stays bounded by
	// rate: a flooding peer cannot grow it without bound (previously every
	// request, including those over the limit, appended a timestamp).
	if len(stamps) >= rate {
		sh.counters[key] = stamps
		return nil, core.ErrPluginDrop
	}
	stamps = append(stamps, now)
	sh.counters[key] = stamps
	return data, nil
}

// sessionKey returns the cached remote-IP key for a session, computing it
// once via remoteKey and storing it in session meta for subsequent messages.
func (p *RateLimit) sessionKey(sess core.Session) string {
	if sess == nil {
		return "unknown"
	}
	if v, ok := sess.GetMeta(rateLimitKeyMeta); ok {
		if key, ok := v.(string); ok {
			return key
		}
	}
	key := remoteKey(sess)
	sess.SetMeta(rateLimitKeyMeta, key)
	return key
}
