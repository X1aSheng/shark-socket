package runtime

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// sessionShards is the number of independent session tables. Every transport
// in the process shares one SessionManager, so a single table means every
// accept and every close across TCP, UDP, WebSocket, HTTP, QUIC, gRPC-Web and
// CoAP serialises on one mutex. Sharding on the session ID - which is handed
// out by a global counter, so consecutive IDs land in different shards -
// turns that into 64 independent locks.
//
// RWMutex is 24 bytes and the map header 8, so the pad brings a shard to 64
// bytes: without it two neighbours share a cache line and invalidating one
// lock's hot word pulls the other out of every other core's cache.
const sessionShards = 64

type sessionShard struct {
	mu       sync.RWMutex
	sessions map[uint64]core.Session
	_        [32]byte
}

type SessionManager struct {
	nextID atomic.Uint64
	count  atomic.Int64
	// max is written once, at construction, and read without a lock
	// afterwards; see WithMaxSessions.
	max    int64
	shards [sessionShards]sessionShard
}

// shard maps a session ID to its table.
func (m *SessionManager) shard(id uint64) *sessionShard {
	return &m.shards[id%sessionShards]
}

type SessionManagerOption func(*SessionManager)

func WithMaxSessions(max int64) SessionManagerOption {
	return func(m *SessionManager) {
		m.max = max
	}
}

func NewSessionManager(opts ...SessionManagerOption) *SessionManager {
	m := &SessionManager{max: 1_000_000}
	for i := range m.shards {
		m.shards[i].sessions = make(map[uint64]core.Session)
	}
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
	// The duplicate check takes the shard lock first and the capacity check
	// comes second, matching the order a single-table manager produced: a
	// transport that retries an existing session ID must be told "duplicate",
	// not "capacity", even when the ceiling is already reached.
	sh := m.shard(sess.ID())
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if _, ok := sh.sessions[sess.ID()]; ok {
		return core.ErrDuplicateSession
	}
	// Claim the slot against the global counter. There is no global lock left
	// to check the ceiling under, and two shards must not each admit a
	// session that together exceed it - so the counter is the single
	// authority and is claimed with a CAS rather than a plain increment.
	for {
		cur := m.count.Load()
		if m.max > 0 && cur >= m.max {
			return core.ErrSessionCapacity
		}
		if m.count.CompareAndSwap(cur, cur+1) {
			break
		}
	}
	sh.sessions[sess.ID()] = sess
	return nil
}

func (m *SessionManager) Unregister(id uint64) bool {
	sh := m.shard(id)
	sh.mu.Lock()
	_, ok := sh.sessions[id]
	delete(sh.sessions, id)
	sh.mu.Unlock()
	if ok {
		m.count.Add(-1)
	}
	return ok
}

func (m *SessionManager) Get(id uint64) (core.Session, bool) {
	sh := m.shard(id)
	sh.mu.RLock()
	sess, ok := sh.sessions[id]
	sh.mu.RUnlock()
	return sess, ok
}

func (m *SessionManager) Count() int64 {
	return m.count.Load()
}

// Snapshot copies the registered sessions. It walks the shards one at a time
// instead of freezing the whole table: while it runs, a session may be
// registered into a shard not yet visited (and so appear here) or removed from
// a shard already visited. Callers - Broadcast and CloseAll - only need a
// superset of what is live, and CloseAll re-loops until the manager is empty,
// so they are unaffected. It is not a point-in-time snapshot.
func (m *SessionManager) Snapshot() []core.Session {
	snapshot := make([]core.Session, 0, m.Count())
	for i := range m.shards {
		sh := &m.shards[i]
		sh.mu.RLock()
		for _, sess := range sh.sessions {
			snapshot = append(snapshot, sess)
		}
		sh.mu.RUnlock()
	}
	return snapshot
}

// Range iterates over sessions inline under each shard's read lock, avoiding a
// full snapshot allocation. fn must not call Register or Unregister for a
// session in the shard currently being walked - the read lock is held for that
// shard - or a deadlock will occur. Iteration order across shards is not
// defined, and like Snapshot it is not a point-in-time view.
func (m *SessionManager) Range(fn func(core.Session) bool) {
	for i := range m.shards {
		sh := &m.shards[i]
		sh.mu.RLock()
		for _, sess := range sh.sessions {
			if !fn(sess) {
				sh.mu.RUnlock()
				return
			}
		}
		sh.mu.RUnlock()
	}
}

func (m *SessionManager) Broadcast(data []byte) error {
	// Send outside the manager lock: a session's Send may block on I/O or
	// re-enter the manager (e.g. via Unregister), which would deadlock if
	// the read lock were held here. Snapshot copies the set under RLock.
	sessions := m.Snapshot()

	// One copy for every recipient that can share a frame, instead of one copy
	// per session. Sessions that cannot share (their Send does not copy anyway,
	// or they are not built for it) take the ordinary path.
	var shared []core.SharedSender
	var plain []core.Session
	for _, sess := range sessions {
		if ss, ok := sess.(core.SharedSender); ok {
			shared = append(shared, ss)
		} else {
			plain = append(plain, sess)
		}
	}

	var firstErr error
	if len(shared) > 1 {
		frame := core.NewSharedFrame(data, len(shared))
		for _, ss := range shared {
			err := safeSessionCall(func() error { return ss.SendShared(frame) })
			if err != nil {
				// SendShared only consumes the reference on success, so drop it
				// here: this session will never write the frame.
				frame.Release()
				if firstErr == nil {
					firstErr = err
				}
			}
		}
	} else {
		plain = append(plain, sessions...) // a single recipient is not worth a frame
	}
	for _, sess := range plain {
		if err := safeSessionCall(func() error { return sess.Send(data) }); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// CloseAll closes and unregisters every session. Uses a snapshot to avoid
// holding the write lock during session.Close() which may block on I/O, and
// loops until the manager is empty (or the context is cancelled) so a session
// registered mid-shutdown is not leaked and a stuck drain cannot spin forever.
func (m *SessionManager) CloseAll(ctx context.Context) error {
	var firstErr error
	for {
		if ctx.Err() != nil {
			return firstErr
		}
		sessions := m.Snapshot()
		if len(sessions) == 0 {
			return firstErr
		}
		for _, sess := range sessions {
			if err := safeSessionCall(func() error { return sess.Close(ctx) }); err != nil && firstErr == nil {
				firstErr = err
			}
			m.Unregister(sess.ID())
		}
	}
}

// safeSessionCall recovers a panic from a user session implementation, so a
// panicking Send/Close in Broadcast/CloseAll cannot crash the whole process.
func safeSessionCall(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = core.ErrHandlerPanic
		}
	}()
	return fn()
}

var _ core.SessionManager = (*SessionManager)(nil)
