// Package bufferpool provides a six-level buffer pool for zero-allocation critical paths.
//
// This package implements a high-performance buffer management system using sync.Pool
// to minimize memory allocations on hot code paths. It uses six distinct pool levels
// to efficiently handle messages from small IoT packets to large file transfers.
//
// # Architecture
//
//	┌──────────────────────────────────────────────────────────────┐
//	│                 Six-Level BufferPool                           │
//	├──────────────────────────────────────────────────────────────┤
//	│  Level   │ Size Limit │ Pool Index │ Typical Use Case         │
//	├──────────────────────────────────────────────────────────────┤
//	│  Micro   │  ≤ 512 B   │     0      │ CoAP ACK, heartbeat     │
//	│  Tiny    │  ≤ 2 KB    │     1      │ Short text, commands    │
//	│  Small   │  ≤ 4 KB    │     2      │ Normal business JSON    │
//	│  Medium  │  ≤ 32 KB   │     3      │ HTTP body, batch data   │
//	│  Large   │  ≤ 256 KB  │     4      │ Large messages, chunks  │
//	│  Huge    │  > 256 KB  │    N/A     │ Direct allocation       │
//	└──────────────────────────────────────────────────────────────┘
//
// # Usage
//
//	import "github.com/X1aSheng/shark-socket/internal/infra/bufferpool"
//
//	// Get buffer from pool
//	buf := bufferpool.Default().Get(1024)  // Request 1KB
//	defer bufferpool.Default().Put(buf)    // Return to pool
//
//	// Work with buffer data
//	copy(buf.Bytes(), data)
//	process(buf.Bytes())
//
// # Buffer Structure
//
//	Buffer struct {
//	    data []byte  // The actual byte slice
//	    cap_ int     // Original capacity for level matching
//	}
//
//	// Methods
//	buf.Bytes()  // Get underlying []byte
//	buf.Len()    // Length of valid data
//	buf.Cap()    // Original capacity
//
// # Pool Level Selection
//
// The GetLevel() function selects the appropriate pool based on size:
//
//	GetLevel(size) → int {
//	    size <= 512      → 0 (Micro)
//	    size <= 2048     → 1 (Tiny)
//	    size <= 4096     → 2 (Small)
//	    size <= 32768    → 3 (Medium)
//	    size <= 262144   → 4 (Large)
//	    size > 262144    → 5 (Huge, direct allocation)
//	}
//
// # Zero-Allocation Design
//
// Critical path optimizations:
//  1. sync.Pool reuses buffers across goroutines
//  2. Buffer wrapping preserves capacity for size checks
//  3. Compiler can inline simple accessor methods
//
// Benchmark comparison (per Get+Put cycle):
//
//	Baseline (make+GC):  ~200 ns, heap allocation
//	BufferPool (hit):    ~5 ns,   pooled reuse
//	BufferPool (miss):   ~100 ns, first allocation
//
// # Memory Management
//
// TotalMemoryCap limits pool memory usage:
//
//	pool := bufferpool.New()
//	pool.SetTotalMemoryCap(100 * 1024 * 1024)  // 100 MB limit
//
// When limit is exceeded, Put() discards buffers instead of returning them.
// This prevents unbounded memory growth under load.
//
// # Huge Buffer Handling
//
// Buffers larger than 256 KB are NOT pooled:
//  - Direct allocation with make()
//  - Tracked separately in HugeAlloc counter
//  - Returned to caller without pooling overhead
//
// This prevents large messages from polluting the pool.
// Large buffers have different lifecycle patterns and don't benefit from reuse.
//
// # Security Considerations
//
// Put() zeros buffer contents before returning to pool:
//
//	func (bp *BufferPool) Put(buf *Buffer) {
//	    for i := range buf.data {
//	        buf.data[i] = 0  // Zero sensitive data
//	    }
//	    // ... return to pool
//	}
//
// This prevents data leakage between sessions.
//
// # Statistics and Observability
//
// PoolStats provides detailed metrics:
//
//	stats := pool.Stats()
//	stats.Hits[0]     // Micro level cache hits
//	stats.Misses[0]   // Micro level cache misses
//	stats.Allocs[0]   // Micro level allocations
//	stats.TotalMem    // Total pooled memory
//	stats.HugeAlloc   // Huge buffer count
//
// Metrics are also exposed via Prometheus:
//  - shark_bufferpool_hits_total{level="level0..4"}
//  - shark_bufferpool_misses_total{level="level0..4"}
//
// # Multi-Level Pooling Strategy
//
// Why six levels instead of one?
//
//  1. Fragmentation prevention
//     A single pool would create buffers of varying sizes, causing memory waste
//
//  2. Cache line efficiency
//     Smaller buffers fit in CPU cache more easily
//
//  3. IoT optimization
//     Micro level (512B) handles CoAP/UDP small packets efficiently
//
//  4. Large message isolation
//     Huge buffers bypass pooling to prevent pool pollution
//
// Size distribution example (typical web server):
//
//	┌────────┬────────────────────────────────────┐
//	│ Level  │ Message Size Distribution          │
//	├────────┼────────────────────────────────────┤
//	│ Micro  │ 30%  (ACK, ping, control frames)   │
//	│ Tiny   │ 25%  (short JSON, commands)         │
//	│ Small  │ 35%  (normal business messages)     │
//	│ Medium │ 8%   (HTTP bodies, batch data)      │
//	│ Large  │ 2%   (file uploads, exports)        │
//	│ Huge   │ <1%  (large file transfers)         │
//	└────────┴────────────────────────────────────┘
//
// # Thread Safety
//
// All operations are thread-safe:
//  - sync.Pool handles concurrent access
//  - Atomic counters for statistics
//  - No external synchronization required
//
// # Integration Points
//
// Used by:
//   - TCP readLoop: Get(4KB) for frame data
//   - UDP readLoop: Get(65535) for datagrams
//   - WorkerPool: Task buffer reuse
//   - Protocol framers: Per-frame allocation
//
// Returned by:
//   - TCPSession Send: async write from pool
//   - Plugin chain: buffer passing between plugins
//
// # Best Practices
//
//  1. Always return buffers
//     defer pool.Put(buf) to prevent leaks
//
//  2. Use appropriate pool
//     bufferpool.Default() for global, custom pools for isolation
//
//  3. Set memory cap in production
//     Prevents OOM under adversarial load
//
//  4. Monitor hit rate
//     Low hits indicate size misprediction
//
//  5. Don't hold buffers indefinitely
//     Return promptly to maintain availability
//
package bufferpool