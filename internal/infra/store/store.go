package store

import "sync"

type Store interface {
	Save(bucket, key string, value []byte)
	Load(bucket, key string) ([]byte, bool)
	Delete(bucket, key string)
}

type Memory struct {
	mu      sync.RWMutex
	buckets map[string]map[string][]byte
}

func NewMemory() *Memory {
	return &Memory{buckets: make(map[string]map[string][]byte)}
}

func (m *Memory) Save(bucket, key string, value []byte) {
	m.mu.Lock()
	if _, ok := m.buckets[bucket]; !ok {
		m.buckets[bucket] = make(map[string][]byte)
	}
	m.buckets[bucket][key] = append([]byte(nil), value...)
	m.mu.Unlock()
}

func (m *Memory) Load(bucket, key string) ([]byte, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	values, ok := m.buckets[bucket]
	if !ok {
		return nil, false
	}
	value, ok := values[key]
	return append([]byte(nil), value...), ok
}

func (m *Memory) Delete(bucket, key string) {
	m.mu.Lock()
	if values, ok := m.buckets[bucket]; ok {
		delete(values, key)
	}
	m.mu.Unlock()
}

var _ Store = (*Memory)(nil)
