// Package session provides session lifecycle management with sharded locking and LRU eviction.
//
// This package implements the core session management system, including BaseSession
// (the base session implementation), SessionManager (with 32 shards), and LRUList
// (for efficient session eviction).
//
// # Session Hierarchy
//
//	┌─────────────────────────────────────────────────────────────────┐
//	│                     Session Architecture                           │
//	├─────────────────────────────────────────────────────────────────┤
//	│                                                                  │
//	│  ┌──────────────────────────────────────────────────────────┐   │
//	│  │                      BaseSession                          │   │
//	│  │  - id, protocol, remoteAddr, localAddr (immutable)       │   │
//	│  │  - state (atomic Int32)                                  │   │
//	│  │  - lastActive (atomic Int64)                             │   │
//	│  │  - ctx, cancel (context for lifecycle)                    │   │
//	│  │  - meta (sync.Map for arbitrary data)                    │   │
//	│  └──────────────────────────────────────────────────────────┘   │
//	│                              │                                  │
//	│          ┌───────────────────┼───────────────────┐              │
//	│          ▼                   ▼                   ▼              │
//	│  ┌─────────────┐     ┌─────────────┐     ┌─────────────┐      │
//	│  │ TCPSession  │     │ WSSession   │     │ UDPSession  │      │
//	│  │ + writeQueue│     │ + writeMu   │     │ + addr      │      │
//	│  └─────────────┘     └─────────────┘     └─────────────┘      │
//	│                                                                  │
//	└─────────────────────────────────────────────────────────────────┘
//
// # Session State Machine
//
// Sessions transition through states atomically using CAS:
//
//	┌─────────────────────────────────────────────────────────────────┐
//	│                      Session State Machine                         │
//	├─────────────────────────────────────────────────────────────────┤
//	│                                                                  │
//	│   Connecting ──[accept]──────────────────────────────┐          │
//	│      │                                              │          │
//	│      │ (error/closed)                               ▼          │
//	│      └──────────────────────────→ Closed ◄─────────┤          │
//	│                                                 │    │          │
//	│                                                 │    │          │
//	│   Active ◄──────────[Active!]──────┌───────────┘    │          │
//	│      │                              │                │          │
//	│      │ (Close())                    │                │          │
//	│      ▼                              │                │          │
//	│   Closing ──[drain writeQueue]──────┘                │          │
//	│      │                                                 │          │
//	│      │ (drain complete)                                │          │
//	│      ▼                                                 │          │
//	│   Closed ◄─────────────────────────────────────────────┘          │
//	│                                                                  │
//	└─────────────────────────────────────────────────────────────────┘
//
// State transitions are protected by Compare-And-Swap (CAS) to ensure
// only one goroutine performs each transition.
//
// # SessionManager
//
// SessionManager manages all active sessions with 32 shards for parallelism:
//
//	type Manager struct {
//	    shards   [32]shard  // Sharded maps for parallelism
//	    idGen    atomic.Uint64  // Global unique ID generator
//	    maxSess  int64
//	    totalAct atomic.Int64
//	}
//
//	type shard struct {
//	    mu       sync.RWMutex
//	    sessions map[uint64]RawSession
//	    lru      *LRUList
//	}
//
// # Shard Selection
//
// Sessions are distributed across shards by session ID:
//
//	shardIndex(id uint64) = id & 31  // Bitwise AND is faster than modulo
//
// This ensures:
//   - Load balancing across 32 locks
//   - Same session always maps to same shard (consistent lookup)
//   - Minimal lock contention under high concurrency
//
// # Core Operations
//
//	// Register new session
//	id := manager.NextID()  // Atomic, globally unique
//	manager.Register(sess)
//
//	// Lookup session
//	sess, ok := manager.Get(id)
//
//	// Iterate all sessions (read-only, non-blocking)
//	manager.Range(func(sess RawSession) bool {
//	    // Process session
//	    return true  // false to stop iteration
//	})
//
//	// Broadcast to all sessions
//	manager.Broadcast(data)
//
//	// Unregister (cleanup)
//	manager.Unregister(id)
//
// # LRU Eviction
//
// When session count exceeds capacity:
//
//  1. Calculate target shard from session ID
//  2. Acquire shard write lock (TryLock to avoid deadlock)
//  3. Get LRU tail (least recently used)
//  4. Close and unregister evicted session
//  5. Register new session
//
// Cross-shard LRU uses TryLock to prevent circular wait.
//
// # Session Metadata
//
// Use sync.Map for arbitrary key-value storage:
//
//	// Store data
//	sess.SetMeta("user_id", userID)
//	sess.SetMeta("auth_token", token)
//
//	// Retrieve data
//	userID, ok := sess.GetMeta("user_id")
//
//	// Delete data
//	sess.DelMeta("auth_token")
//
// Metadata is useful for:
//   - Per-session application state
//   - Plugin-specific data
//   - Session attributes
//
// # Lifecycle Context
//
// Each session has a context for coordinated shutdown:
//
//	ctx, cancel := context.WithCancel(context.Background())
//	sess := &BaseSession{ctx: ctx, cancel: cancel}
//
//	// On close, cancel context
//	cancel()  // All goroutines listening on ctx.Done() will exit
//
// Typical usage in protocol sessions:
//
//	// readLoop
//	select {
//	case <-sess.Context().Done():
//	    return  // Session closed
//	case data := <-sess.readCh:
//	    process(data)
//	}
//
// # Send and WriteQueue
//
// TCP sessions use a writeQueue channel for non-blocking sends:
//
//	// Send (non-blocking)
//	err := sess.Send(data)
//	// Returns ErrWriteQueueFull if queue is full
//
//	// Background goroutine drains queue
//	for data := range sess.writeQueue {
//	    conn.Write(data)
//	}
//
// This architecture:
//   - Prevents slow writers from blocking message processing
//   - Allows controlled queue overflow handling
//   - Enables graceful drain on close
//
// # Go 1.26 Iterator Support
//
// SessionManager.All() returns a Go 1.26 iterator:
//
//	for sess := range manager.All() {
//	    process(sess)
//	}
//
// This is more ergonomic than Range while maintaining lazy evaluation.
//
// # Thread Safety
//
// All operations are thread-safe:
//   - atomic operations for state, counters
//   - shard RWMutex for session maps
//   - sync.Map for metadata
//   - sync.Once for close idempotency
//
// # Metrics
//
// SessionManager emits Prometheus metrics:
//
//	shark_sessions_total             // Total registered sessions
//	shark_sessions_active           // Current active sessions
//	shark_sessions_lru_evictions    // LRU eviction count
//	shark_sessions_by_protocol{protocol}  // Sessions per protocol
//
// # Performance Characteristics
//
// Operation performance:
//
//	manager.NextID()     → atomic.AddUint64 → ~5ns
//	manager.Get(id)      → RLock + map lookup → ~50ns
//	manager.Register(sess) → Lock + map insert → ~100ns
//	manager.Count()      → atomic.Load → ~5ns
//
// 32 shards reduce lock contention by ~32x compared to single lock.
//
// # Compilation Verification
//
// Implementations should verify interface compliance:
//
//	var _ types.RawSession = (*TCPSession)(nil)
//	var _ types.SessionManager = (*Manager)(nil)
//
package session
