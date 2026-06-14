package store

import "sync"

// Store is the legacy interface for backward compatibility.
type Store interface {
	Save(bucket, key string, value []byte)
	Load(bucket, key string) ([]byte, bool)
	Delete(bucket, key string)
}

// StoreV2 is the durable store interface with error returns and lifecycle.
type StoreV2 interface {
	Store
	SaveV2(bucket, key string, value []byte) error
	LoadV2(bucket, key string) ([]byte, bool, error)
	DeleteV2(bucket, key string) error
	List(bucket string) ([]string, error)
	Close() error
}

// BulkDeleter is an optional interface for stores that can batch-delete
// multiple keys in a single transaction (e.g., BoltDB).
type BulkDeleter interface {
	DeleteBatch(bucket string, keys []string) error
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

func (m *Memory) SaveV2(bucket, key string, value []byte) error {
	m.Save(bucket, key, value)
	return nil
}

func (m *Memory) LoadV2(bucket, key string) ([]byte, bool, error) {
	v, ok := m.Load(bucket, key)
	return v, ok, nil
}

func (m *Memory) DeleteV2(bucket, key string) error {
	m.Delete(bucket, key)
	return nil
}

func (m *Memory) List(bucket string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := make([]string, 0, len(m.buckets[bucket]))
	for k := range m.buckets[bucket] {
		keys = append(keys, k)
	}
	return keys, nil
}

func (m *Memory) Close() error { return nil }

var (
	_ Store   = (*Memory)(nil)
	_ StoreV2 = (*Memory)(nil)
)
