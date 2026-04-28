package cache

import (
	"context"
	"sync"
	"time"
)

// Cache provides a key-value caching interface with TTL support.
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
	Del(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
	TTL(ctx context.Context, key string) (time.Duration, error)
	MGet(ctx context.Context, keys []string) (map[string][]byte, error)
	Close() error
}

type cacheEntry struct {
	data   []byte
	expiry time.Time
}

func (e cacheEntry) isExpired() bool {
	return !e.expiry.IsZero() && time.Now().After(e.expiry)
}

// MemoryCache implements Cache with an in-memory map and lazy TTL expiration.
type MemoryCache struct {
	mu            sync.RWMutex
	entries       map[string]cacheEntry
	stopCh        chan struct{}
	closeOnce     sync.Once
	cleanInterval time.Duration
}

// CacheOption configures a MemoryCache.
type CacheOption func(*MemoryCache)

// WithCleanInterval sets the background cleanup interval.
func WithCleanInterval(d time.Duration) CacheOption {
	return func(c *MemoryCache) { c.cleanInterval = d }
}

// NewMemoryCache creates a new in-memory cache.
func NewMemoryCache(opts ...CacheOption) *MemoryCache {
	c := &MemoryCache{
		entries:       make(map[string]cacheEntry),
		stopCh:        make(chan struct{}),
		cleanInterval: time.Minute,
	}
	for _, opt := range opts {
		opt(c)
	}
	go c.cleanExpired()
	return c
}

func (c *MemoryCache) cleanExpired() {
	ticker := time.NewTicker(c.cleanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.mu.Lock()
			now := time.Now()
			for k, v := range c.entries {
				if !v.expiry.IsZero() && now.After(v.expiry) {
					delete(c.entries, k)
				}
			}
			c.mu.Unlock()
		case <-c.stopCh:
			return
		}
	}
}

func (c *MemoryCache) Get(_ context.Context, key string) ([]byte, error) {
	c.mu.RLock()
	e, ok := c.entries[key]
	if !ok {
		c.mu.RUnlock()
		return nil, ErrCacheMiss
	}
	if !e.isExpired() {
		cp := make([]byte, len(e.data))
		copy(cp, e.data)
		c.mu.RUnlock()
		return cp, nil
	}
	c.mu.RUnlock()

	// Expired — acquire write lock for deletion.
	c.mu.Lock()
	if e, ok := c.entries[key]; ok && e.isExpired() {
		delete(c.entries, key)
	}
	c.mu.Unlock()
	return nil, ErrCacheMiss
}

func (c *MemoryCache) Set(_ context.Context, key string, val []byte, ttl time.Duration) error {
	c.mu.Lock()
	var expiry time.Time
	if ttl > 0 {
		expiry = time.Now().Add(ttl)
	}
	c.entries[key] = cacheEntry{data: val, expiry: expiry}
	c.mu.Unlock()
	return nil
}

func (c *MemoryCache) Del(_ context.Context, key string) error {
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
	return nil
}

func (c *MemoryCache) Exists(_ context.Context, key string) (bool, error) {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	return ok && !e.isExpired(), nil
}

func (c *MemoryCache) TTL(_ context.Context, key string) (time.Duration, error) {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || e.isExpired() {
		return 0, ErrCacheMiss
	}
	if e.expiry.IsZero() {
		return 0, nil
	}
	return time.Until(e.expiry), nil
}

func (c *MemoryCache) MGet(_ context.Context, keys []string) (map[string][]byte, error) {
	result := make(map[string][]byte, len(keys))
	c.mu.RLock()
	for _, k := range keys {
		if e, ok := c.entries[k]; ok && !e.isExpired() {
			cp := make([]byte, len(e.data))
			copy(cp, e.data)
			result[k] = cp
		}
	}
	c.mu.RUnlock()
	return result, nil
}

// Close stops the background cleanup goroutine.
// Safe to call multiple times.
func (c *MemoryCache) Close() error {
	c.closeOnce.Do(func() { close(c.stopCh) })
	return nil
}
