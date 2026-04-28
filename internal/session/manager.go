package session

import (
	"iter"
	"log"
	"sync"
	"sync/atomic"

	"github.com/X1aSheng/shark-socket/internal/errs"
	"github.com/X1aSheng/shark-socket/internal/infra/metrics"
	"github.com/X1aSheng/shark-socket/internal/types"
)

const numShards = 32

type shard struct {
	mu       sync.RWMutex
	sessions map[uint64]types.RawSession
	lru      *LRUList
}

// Manager is a sharded SessionManager with LRU eviction.
type Manager struct {
	shards  [numShards]shard
	idGen   atomic.Uint64
	maxSess int64
	total   atomic.Int64
	closed  atomic.Bool
}

// ManagerOption configures a Manager.
type ManagerOption func(*Manager)

// WithMaxSessions sets the maximum number of concurrent sessions.
func WithMaxSessions(max int64) ManagerOption {
	return func(m *Manager) { m.maxSess = max }
}

// NewManager creates a new sharded SessionManager.
func NewManager(opts ...ManagerOption) *Manager {
	m := &Manager{
		maxSess: 1000000, // default 1M
	}
	for _, opt := range opts {
		opt(m)
	}
	for i := range numShards {
		m.shards[i].sessions = make(map[uint64]types.RawSession)
		m.shards[i].lru = NewLRUList()
	}
	return m
}

func (m *Manager) shardIndex(id uint64) int {
	return int(id & (numShards - 1))
}

// NextID generates a globally unique, monotonically increasing session ID.
func (m *Manager) NextID() uint64 {
	return m.idGen.Add(1)
}

// Register adds a session. If at capacity, evicts the least recently used.
func (m *Manager) Register(sess types.RawSession) error {
	if m.closed.Load() {
		return errs.ErrSessionClosed
	}
	id := sess.ID()
	si := m.shardIndex(id)
	s := &m.shards[si]

	s.mu.Lock()
	if _, exists := s.sessions[id]; exists {
		s.mu.Unlock()
		return errs.ErrDuplicateSession
	}
	s.sessions[id] = sess
	s.lru.Touch(id)
	if m.total.Add(1) > m.maxSess {
		m.evictGlobal(s, si)
	}
	s.mu.Unlock()
	return nil
}

// evictGlobal tries local shard first, then searches other shards for LRU victim.
// Uses TryLock to avoid deadlock when multiple shards compete.
func (m *Manager) evictGlobal(localShard *shard, localIdx int) {
	// Try local shard first
	evicted := localShard.lru.Evict(1)
	for _, eid := range evicted {
		if old, ok := localShard.sessions[eid]; ok {
			delete(localShard.sessions, eid)
			m.total.Add(-1)
			metrics.IncCounter("shark_session_lru_evictions_total")
			go old.Close()
			return
		}
	}

	// Local shard empty — search other shards for a victim (non-blocking)
	for i := 0; i < numShards; i++ {
		if i == localIdx {
			continue
		}
		other := &m.shards[i]
		if !other.mu.TryLock() {
			continue
		}
		evicted := other.lru.Evict(1)
		for _, eid := range evicted {
			if old, ok := other.sessions[eid]; ok {
				delete(other.sessions, eid)
				m.total.Add(-1)
				metrics.IncCounter("shark_session_lru_evictions_total")
				go old.Close()
				other.mu.Unlock()
				return
			}
		}
		other.mu.Unlock()
	}
	log.Printf("[shark-socket] session manager: eviction failed for session %d, all shards contended", localIdx)
	metrics.IncCounter("shark_session_eviction_failures_total")
}

// Unregister removes a session.
func (m *Manager) Unregister(id uint64) {
	si := m.shardIndex(id)
	s := &m.shards[si]
	s.mu.Lock()
	if _, ok := s.sessions[id]; ok {
		delete(s.sessions, id)
		s.lru.Remove(id)
		m.total.Add(-1)
	}
	s.mu.Unlock()
}

// Get retrieves a session by ID.
func (m *Manager) Get(id uint64) (types.RawSession, bool) {
	si := m.shardIndex(id)
	s := &m.shards[si]
	s.mu.RLock()
	sess, ok := s.sessions[id]
	s.mu.RUnlock()
	return sess, ok
}

// Count returns the total number of active sessions (lock-free).
func (m *Manager) Count() int64 {
	return m.total.Load()
}

// All returns an iterator over all sessions.
func (m *Manager) All() iter.Seq[types.RawSession] {
	return func(yield func(types.RawSession) bool) {
		for i := range numShards {
			s := &m.shards[i]
			s.mu.RLock()
			for _, sess := range s.sessions {
				if !yield(sess) {
					s.mu.RUnlock()
					return
				}
			}
			s.mu.RUnlock()
		}
	}
}

// Range iterates over all sessions with a callback.
func (m *Manager) Range(fn func(types.RawSession) bool) {
	for i := range numShards {
		s := &m.shards[i]
		s.mu.RLock()
		for _, sess := range s.sessions {
			if !fn(sess) {
				s.mu.RUnlock()
				return
			}
		}
		s.mu.RUnlock()
	}
}

// Broadcast sends data to all active sessions.
func (m *Manager) Broadcast(data []byte) {
	m.Range(func(sess types.RawSession) bool {
		if sess.IsAlive() {
			if err := sess.Send(data); err != nil {
				metrics.IncCounter("shark_session_broadcast_errors_total", sess.Protocol().String())
			}
		}
		return true
	})
}

// Close shuts down the manager, closing all sessions.
func (m *Manager) Close() error {
	if !m.closed.CompareAndSwap(false, true) {
		return nil
	}
	for i := range numShards {
		s := &m.shards[i]
		s.mu.Lock()
		for id, sess := range s.sessions {
			_ = sess.Close()
			delete(s.sessions, id)
		}
		s.lru.Stop()
		s.mu.Unlock()
	}
	m.total.Store(0)
	return nil
}

// Compile-time verification.
var _ types.SessionManager = (*Manager)(nil)
