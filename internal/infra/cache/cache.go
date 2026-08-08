package cache

import (
	"context"
	"sync"
	"time"
)

type Cache interface {
	Get(string) ([]byte, bool)
	Set(string, []byte, time.Duration)
	Delete(string)
	Has(string) bool
}

type entry struct {
	value     []byte
	expiresAt time.Time
}

type Memory struct {
	mu    sync.RWMutex
	items map[string]entry
}

func NewMemory() *Memory {
	return &Memory{items: make(map[string]entry)}
}

func (m *Memory) Get(key string) ([]byte, bool) {
	m.mu.RLock()
	item, ok := m.items[key]
	// Check expiry under the lock so a concurrent Set with a fresh value is
	// never shadowed by a stale-expiry decision made after the lock released.
	if ok && !item.expiresAt.IsZero() && time.Now().After(item.expiresAt) {
		ok = false // expired; Sweep handles cleanup
	}
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return append([]byte(nil), item.value...), true
}

// StartSweeper periodically removes expired entries until ctx is done, so a
// long-lived cache populated with short-TTL keys stays bounded. Additive:
// callers relying on Len/Sweep alone do not need to call it.
func (m *Memory) StartSweeper(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.Sweep(time.Now())
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (m *Memory) Set(key string, value []byte, ttl time.Duration) {
	item := entry{value: append([]byte(nil), value...)}
	if ttl > 0 {
		item.expiresAt = time.Now().Add(ttl)
	}
	m.mu.Lock()
	m.items[key] = item
	m.mu.Unlock()
}

func (m *Memory) Delete(key string) {
	m.mu.Lock()
	delete(m.items, key)
	m.mu.Unlock()
}

func (m *Memory) Has(key string) bool {
	_, ok := m.Get(key)
	return ok
}

func (m *Memory) Len() int {
	m.Sweep(time.Now())
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.items)
}

func (m *Memory) Sweep(now time.Time) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	removed := 0
	for key, item := range m.items {
		if !item.expiresAt.IsZero() && now.After(item.expiresAt) {
			delete(m.items, key)
			removed++
		}
	}
	return removed
}

func (m *Memory) Clear() {
	m.mu.Lock()
	m.items = make(map[string]entry)
	m.mu.Unlock()
}

var _ Cache = (*Memory)(nil)
