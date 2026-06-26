package runtime

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type SessionManager struct {
	mu       sync.RWMutex
	nextID   atomic.Uint64
	count    atomic.Int64
	max      int64
	sessions map[uint64]core.Session
}

type SessionManagerOption func(*SessionManager)

func WithMaxSessions(max int64) SessionManagerOption {
	return func(m *SessionManager) {
		m.max = max
	}
}

func NewSessionManager(opts ...SessionManagerOption) *SessionManager {
	m := &SessionManager{max: 1_000_000, sessions: make(map[uint64]core.Session)}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

func (m *SessionManager) NextID() uint64 {
	return m.nextID.Add(1)
}

func (m *SessionManager) Register(sess core.Session) error {
	if sess == nil {
		return core.ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[sess.ID()]; ok {
		return core.ErrDuplicateSession
	}
	if m.max > 0 && int64(len(m.sessions)) >= m.max {
		return core.ErrSessionCapacity
	}
	m.sessions[sess.ID()] = sess
	m.count.Add(1)
	return nil
}

func (m *SessionManager) Unregister(id uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[id]; ok {
		delete(m.sessions, id)
		m.count.Add(-1)
	}
}

func (m *SessionManager) Get(id uint64) (core.Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sess, ok := m.sessions[id]
	return sess, ok
}

func (m *SessionManager) Count() int64 {
	return m.count.Load()
}

func (m *SessionManager) Snapshot() []core.Session {
	m.mu.RLock()
	snapshot := make([]core.Session, 0, len(m.sessions))
	for _, sess := range m.sessions {
		snapshot = append(snapshot, sess)
	}
	m.mu.RUnlock()
	return snapshot
}

// Range iterates over sessions inline under read lock, avoiding a full
// snapshot allocation. fn must not call methods that acquire the write
// lock (Register, Unregister) or a deadlock will occur.
func (m *SessionManager) Range(fn func(core.Session) bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, sess := range m.sessions {
		if !fn(sess) {
			return
		}
	}
}

func (m *SessionManager) Broadcast(data []byte) error {
	var firstErr error
	m.Range(func(sess core.Session) bool {
		if err := sess.Send(data); err != nil && firstErr == nil {
			firstErr = err
		}
		return true
	})
	return firstErr
}

// CloseAll closes and unregisters every session. Uses a snapshot to avoid
// holding the write lock during session.Close() which may block on I/O.
func (m *SessionManager) CloseAll(ctx context.Context) error {
	sessions := m.Snapshot()
	var firstErr error
	for _, sess := range sessions {
		if err := sess.Close(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
		m.Unregister(sess.ID())
	}
	return firstErr
}

var _ core.SessionManager = (*SessionManager)(nil)
