// Package cache provides caching interfaces and in-memory implementations.
//
// This package defines the Cache interface and built-in MemoryCache implementation.
// Cache is used for distributed blacklists, session routing, and frequently accessed data.
//
// # Cache Interface
//
//	type Cache interface {
//	    Get(ctx context.Context, key string) ([]byte, error)
//	    Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
//	    Del(ctx context.Context, key string) error
//	    Exists(ctx context.Context, key string) (bool, error)
//	    TTL(ctx context.Context, key string) (time.Duration, error)
//	    MGet(ctx context.Context, keys []string) (map[string][]byte, error)
//	}
//
// # MemoryCache Implementation
//
// MemoryCache is a thread-safe in-memory cache with TTL support:
//
//	cache := NewMemoryCache(
//	    WithMaxSize(10000),
//	    WithCleanInterval(5*time.Minute),
//	)
//
// # Usage Examples
//
// Basic operations:
//
//	// Set with TTL
//	err := cache.Set(ctx, "user:123", []byte("data"), 10*time.Minute)
//
//	// Get
//	data, err := cache.Get(ctx, "user:123")
//	if errors.Is(err, ErrCacheMiss) {
//	    // not found, fetch from source
//	}
//
//	// Delete
//	err = cache.Delete(ctx, "user:123")
//
//	// Check existence
//	exists, _ := cache.Exists(ctx, "user:123")
//
//	// Bulk get (for efficiency)
//	results, _ := cache.MGet(ctx, []string{"key1", "key2", "key3"})
//
// # TTL Behavior
//
// Entries expire after TTL duration:
//   - Get() returns ErrCacheMiss for expired entries
//   - Expired entries are lazily cleaned (not immediately removed)
//   - Background goroutine periodically removes expired entries
//
// # Lazy Expiration
//
// MemoryCache uses lazy expiration:
//   - Get() checks expiry time on access
//   - Expired entries deleted on access
//   - Reduces cleanup overhead for rarely accessed keys
//
// # Error Handling
//
//	ErrCacheMiss is returned when key not found or expired:
//
//	import "github.com/X1aSheng/shark-socket/internal/errs"
//
//	data, err := cache.Get(ctx, "key")
//	if errors.Is(err, errs.ErrCacheMiss) {
//	    // handle miss
//	}
//
// # Capacity Management
//
// MemoryCache has optional capacity limits:
//
//	NewMemoryCache(
//	    WithMaxSize(100000),        // Maximum entries
//	    WithMaxMemory(50*1024*1024), // Maximum bytes
//	)
//
// When capacity exceeded:
//   1. LRU eviction of least recently used entries
//   2. Eviction continues until under capacity
//
// # Thread Safety
//
// All operations are thread-safe:
//   - RWMutex for read/write balance
//   - Atomic operations for statistics
//   - No external locking required
//
// # Adapters
//
// Redis adapter available for distributed caching:
//
//	import "github.com/X1aSheng/shark-socket/internal/infra/cache/redis"
//
//	cache, _ := redis.NewRedisCache(client)
//
// Benefits:
//   - Shared cache across instances
//   - TTL sync with Redis
//   - Cluster support for scaling
//
// # Use Cases
//
// Blacklist cache (BlacklistPlugin):
//
//	cache.Set(ctx, "ip:"+ip, []byte("1"), banTTL)
//	_, err := cache.Get(ctx, "ip:"+ip)
//	if err == nil {
//	    // IP is blacklisted
//	}
//
// Session routing (ClusterPlugin):
//
//	cache.Set(ctx, "session:route:"+sessID, []byte(nodeID), sessionTTL)
//	nodeID, _ := cache.Get(ctx, "session:route:"+sessID)
//
// Rate limit state:
//
//	cache.Set(ctx, "ratelimit:"+ip, []byte(count), time.Minute)
//	count, _ := cache.Get(ctx, "ratelimit:"+ip)
//
// # Performance
//
// Performance characteristics (per operation):
//
//	Cache.Get   → ~100ns (hit)  /  ~200ns (miss)
//	Cache.Set   → ~150ns
//	Cache.MGet  → ~50ns per key (batch efficiency)
//
// Memory overhead: ~200 bytes per entry (key + value + metadata)
//
// # Key Patterns
//
// Recommended key naming conventions:
//
//	ip:blocked:{ip}           // Blacklist entries
//	session:route:{sessID}    // Session routing
//	ratelimit:{ip}:{window}  // Rate limit counters
//	user:{userID}:data        // User-specific cache
//
// Avoid:
//   - Keys larger than 256 bytes
//   - Too many distinct key prefixes (poor prefix locality)
//   - Mutable values (invalidate on update)
//
// # Metrics
//
// MemoryCache emits Prometheus metrics:
//
//	shark_cache_operations_total{operation, status}
//	shark_cache_memory_bytes
//	shark_cache_entries_total
//
// Operations: get, set, delete, mget
// Status: hit, miss, error
//
// # Configuration Options
//
//	NewMemoryCache(
//	    WithMaxSize(100000),           // Entry limit
//	    WithMaxMemory(0),              // Memory limit (0 = unlimited)
//	    WithCleanInterval(1*time.Minute), // Cleanup interval
//	    WithShardCount(32),           // Sharding for concurrency
//	)
//
package cache