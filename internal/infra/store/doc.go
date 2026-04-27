// Package store provides key-value storage interfaces and in-memory implementations.
//
// This package defines the Store interface and built-in MemoryStore implementation.
// Store is used for session state persistence, configuration storage, and durable data.
//
// # Store Interface
//
//	type Store interface {
//	    Save(ctx context.Context, key string, val []byte) error
//	    Load(ctx context.Context, key string) ([]byte, error)
//	    Delete(ctx context.Context, key string) error
//	    Query(ctx context.Context, prefix string) ([][]byte, error)
//	}
//
// # MemoryStore Implementation
//
// MemoryStore is a thread-safe in-memory key-value store:
//
//	store := NewMemoryStore()
//
// # Usage Examples
//
// Basic operations:
//
//	// Save (upsert)
//	err := store.Save(ctx, "config:app", []byte(`{"key":"value"}`))
//
//	// Load
//	data, err := store.Load(ctx, "config:app")
//	if errors.Is(err, errs.ErrStoreNotFound) {
//	    // not found
//	}
//
//	// Delete
//	err = store.Delete(ctx, "config:app")
//
//	// Query by prefix
//	results, _ := store.Query(ctx, "config:")
//	// Returns all values with keys starting with "config:"
//
// # Query Patterns
//
// Query enables prefix-based iteration:
//
//	// Session state persistence
//	store.Save(ctx, "session:"+sessID, state)
//	allSessions, _ := store.Query(ctx, "session:")
//
//	// Configuration namespaces
//	store.Save(ctx, "cfg:tcp", tcpConfig)
//	store.Save(ctx, "cfg:ws", wsConfig)
//	allConfigs, _ := store.Query(ctx, "cfg:")
//
//	// User data partitioning
//	store.Save(ctx, "user:123:profile", profileData)
//	userData, _ := store.Query(ctx, "user:123:")
//
// # Error Handling
//
//	ErrStoreNotFound is returned when key does not exist:
//
//	import "github.com/X1aSheng/shark-socket/internal/errs"
//
//	data, err := store.Load(ctx, "key")
//	if errors.Is(err, errs.ErrStoreNotFound) {
//	    // handle miss
//	}
//
// # Persistence Guarantees
//
// MemoryStore is volatile (data lost on restart):
//
//	┌──────────────────────────────────────────────────────┐
//	│  MemoryStore                                        │
//	│  - In-process only                                 │
//	│  - Lost on process restart                         │
//	│  - Fast (no I/O overhead)                          │
//	│  - Suitable for: caching, ephemeral state         │
//	└──────────────────────────────────────────────────────┘
//
// For durability, use adapters:
//
//	// Redis Store
//	import "github.com/X1aSheng/shark-socket/internal/infra/store/redis"
//
//	// BoltDB Store (embedded)
//	import "github.com/X1aSheng/shark-socket/internal/infra/store/boltdb"
//
//	// SQL Store
//	import "github.com/X1aSheng/shark-socket/internal/infra/store/sql"
//
// # Thread Safety
//
// All operations are thread-safe:
//   - sync.RWMutex for concurrent access
//   - Atomic operations for statistics
//   - Safe for concurrent reads and writes
//
// # Use Cases
//
// Session state persistence (PersistencePlugin):
//
//	// On accept: restore session state
//	data, err := store.Load(ctx, "session:"+sessID)
//	if err == nil {
//	    sess.SetMeta("history", data)
//	}
//
//	// On close: save final state
//	history := sess.GetMeta("history")
//	store.Save(ctx, "session:"+sessID, history.([]byte))
//
// Configuration storage:
//
//	store.Save(ctx, "cfg:tcp", marshal(config))
//	cfg, _ := store.Load(ctx, "cfg:tcp")
//	unmarshal(cfg, &config)
//
// Feature flags:
//
//	store.Save(ctx, "feature:darkmode", []byte("true"))
//	enabled, _ := store.Exists(ctx, "feature:darkmode")
//
// # Performance
//
// Performance characteristics:
//
//	Store.Load  → ~50ns (in-memory, no I/O)
//	Store.Save  → ~80ns
//	Store.Query → ~100μs for 1000 entries (full scan)
//
// For high-frequency writes, consider batching.
//
// # Metrics
//
// Store emits Prometheus metrics:
//
//	shark_store_operations_total{operation, status}
//	shark_store_memory_bytes
//	shark_store_entries_total
//
// Operations: save, load, delete, query
// Status: success, not_found, error
//
// # Design Considerations
//
// Compared to Cache:
//
//	┌──────────────┬────────────┬────────────────────────────────┐
//	│ Aspect       │ Cache      │ Store                          │
//	├──────────────┼────────────┼────────────────────────────────┤
//	│ TTL          │ Required   │ Optional (none = permanent)   │
//	│ Expiration   │ Auto       │ Manual (Delete or never)       │
//	│ Use Case     │ Ephemeral  │ Semi-durable                   │
//	│ Persistence  │ No         │ Optional (adapters)            │
//	└──────────────┴────────────┴────────────────────────────────┘
//
// Cache: Redis for blacklists, session routing (short TTL)
// Store: BoltDB for session persistence (durable)
//
// # Configuration Options
//
//	NewMemoryStore(
//	    WithMaxEntries(100000),     // Entry limit
//	    WithMaxMemory(100*1024*1024), // Memory limit
//	)
//
package store