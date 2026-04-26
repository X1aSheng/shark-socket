package store

import (
	"context"
	"strings"
	"sync"
)

// Store provides a key-value storage interface.
type Store interface {
	Save(ctx context.Context, key string, val []byte) error
	Load(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
	Query(ctx context.Context, prefix string) ([][]byte, error)
}

// MemoryStore implements Store with an in-memory map.
type MemoryStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewMemoryStore creates a new in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: make(map[string][]byte)}
}

func (s *MemoryStore) Save(_ context.Context, key string, val []byte) error {
	s.mu.Lock()
	s.data[key] = val
	s.mu.Unlock()
	return nil
}

func (s *MemoryStore) Load(_ context.Context, key string) ([]byte, error) {
	s.mu.RLock()
	val, ok := s.data[key]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	return val, nil
}

func (s *MemoryStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	delete(s.data, key)
	s.mu.Unlock()
	return nil
}

func (s *MemoryStore) Query(_ context.Context, prefix string) ([][]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result [][]byte
	for k, v := range s.data {
		if strings.HasPrefix(k, prefix) {
			cp := make([]byte, len(v))
			copy(cp, v)
			result = append(result, cp)
		}
	}
	return result, nil
}
