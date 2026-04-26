package utils

import (
	"fmt"
	"hash/fnv"
	"sync"
)

// ShardedMap provides a sharded concurrent map for reduced lock contention.
type ShardedMap[K comparable, V any] struct {
	shards    []mapShard[K, V]
	numShards uint64
}

type mapShard[K comparable, V any] struct {
	mu   sync.RWMutex
	data map[K]V
}

// NewShardedMap creates a new ShardedMap with the given number of shards.
func NewShardedMap[K comparable, V any](numShards int) *ShardedMap[K, V] {
	sm := &ShardedMap[K, V]{
		shards:    make([]mapShard[K, V], numShards),
		numShards: uint64(numShards),
	}
	for i := range sm.shards {
		sm.shards[i].data = make(map[K]V)
	}
	return sm
}

func (sm *ShardedMap[K, V]) shardIndex(key K) uint64 {
	h := hashAny(key)
	return h % sm.numShards
}

func (sm *ShardedMap[K, V]) Get(key K) (V, bool) {
	s := &sm.shards[sm.shardIndex(key)]
	s.mu.RLock()
	v, ok := s.data[key]
	s.mu.RUnlock()
	return v, ok
}

func (sm *ShardedMap[K, V]) Set(key K, val V) {
	s := &sm.shards[sm.shardIndex(key)]
	s.mu.Lock()
	s.data[key] = val
	s.mu.Unlock()
}

func (sm *ShardedMap[K, V]) Delete(key K) {
	s := &sm.shards[sm.shardIndex(key)]
	s.mu.Lock()
	delete(s.data, key)
	s.mu.Unlock()
}

func (sm *ShardedMap[K, V]) Len() int {
	total := 0
	for i := range sm.shards {
		sm.shards[i].mu.RLock()
		total += len(sm.shards[i].data)
		sm.shards[i].mu.RUnlock()
	}
	return total
}

// hashAny provides a hash for any comparable key using FNV-1a.
func hashAny[K comparable](key K) uint64 {
	switch v := any(key).(type) {
	case string:
		h := fnv.New64a()
		_, _ = h.Write([]byte(v))
		return h.Sum64()
	case uint64:
		return v
	case int:
		return uint64(v)
	case uint32:
		return uint64(v)
	default:
		h := fnv.New64a()
		s := fmt.Sprintf("%v", key)
		_, _ = h.Write([]byte(s))
		return h.Sum64()
	}
}
