package cache

import (
	"sync"
	"time"
)

type Cache interface {
	Get(string) ([]byte, bool)
	Set(string, []byte, time.Duration)
	Delete(string)
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
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if !item.expiresAt.IsZero() && time.Now().After(item.expiresAt) {
		m.Delete(key)
		return nil, false
	}
	return append([]byte(nil), item.value...), true
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

var _ Cache = (*Memory)(nil)
