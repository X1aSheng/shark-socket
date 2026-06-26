package store

import "sync"

// Store is the durable store interface with error returns and lifecycle.
type Store interface {
	Save(bucket, key string, value []byte) error
	Load(bucket, key string) ([]byte, bool, error)
	Delete(bucket, key string) error
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

func (m *Memory) Save(bucket, key string, value []byte) error {
	m.mu.Lock()
	if _, ok := m.buckets[bucket]; !ok {
		m.buckets[bucket] = make(map[string][]byte)
	}
	m.buckets[bucket][key] = append([]byte(nil), value...)
	m.mu.Unlock()
	return nil
}

func (m *Memory) Load(bucket, key string) ([]byte, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	values, ok := m.buckets[bucket]
	if !ok {
		return nil, false, nil
	}
	value, ok := values[key]
	return append([]byte(nil), value...), ok, nil
}

func (m *Memory) Delete(bucket, key string) error {
	m.mu.Lock()
	if values, ok := m.buckets[bucket]; ok {
		delete(values, key)
	}
	m.mu.Unlock()
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
	_ Store = (*Memory)(nil)
)
