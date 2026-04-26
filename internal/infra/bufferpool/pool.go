package bufferpool

import (
	"sync"
	"sync/atomic"
)

const (
	levelMicro  = 0
	levelTiny   = 1
	levelSmall  = 2
	levelMedium = 3
	levelLarge  = 4
	levelHuge   = 5

	microThreshold  = 512
	tinyThreshold   = 2048
	smallThreshold  = 4096
	mediumThreshold = 32768
	largeThreshold  = 262144

	numPools = 5 // Micro through Large; Huge is direct allocation
)

// Buffer wraps a byte slice with its capacity for correct pool return.
type Buffer struct {
	data []byte
	cap_ int
}

// Bytes returns the underlying byte slice.
func (b *Buffer) Bytes() []byte { return b.data }

// Len returns the length of valid data.
func (b *Buffer) Len() int { return len(b.data) }

// Cap returns the original capacity for pool level matching.
func (b *Buffer) Cap() int { return b.cap_ }

// PoolStats holds statistics for the buffer pool.
type PoolStats struct {
	Hits      [numPools]uint64
	Misses    [numPools]uint64
	Allocs    [numPools]uint64
	TotalMem  int64
	HugeAlloc int64
}

// BufferPool is a six-level sync.Pool-based buffer pool.
type BufferPool struct {
	pools    [numPools]sync.Pool
	stats    PoolStats
	totalMem atomic.Int64
	cap      atomic.Int64 // total memory cap in bytes
	hugeAlloc atomic.Int64
}

// GetLevel returns the pool level index for the given size.
func GetLevel(size int) int {
	switch {
	case size <= microThreshold:
		return levelMicro
	case size <= tinyThreshold:
		return levelTiny
	case size <= smallThreshold:
		return levelSmall
	case size <= mediumThreshold:
		return levelMedium
	case size <= largeThreshold:
		return levelLarge
	default:
		return levelHuge
	}
}

func levelSize(level int) int {
	switch level {
	case levelMicro:
		return microThreshold
	case levelTiny:
		return tinyThreshold
	case levelSmall:
		return smallThreshold
	case levelMedium:
		return mediumThreshold
	case levelLarge:
		return largeThreshold
	default:
		return 0
	}
}

// New creates a new BufferPool.
func New() *BufferPool {
	bp := &BufferPool{}
	for i := range numPools {
		size := levelSize(i)
		poolIdx := i
		bp.pools[poolIdx] = sync.Pool{
			New: func() any {
				b := make([]byte, size)
				return &Buffer{data: b, cap_: cap(b)}
			},
		}
	}
	return bp
}

// Get retrieves a buffer of at least the requested size.
func (bp *BufferPool) Get(size int) *Buffer {
	level := GetLevel(size)
	if level == levelHuge {
		bp.hugeAlloc.Add(1)
		b := make([]byte, size)
		return &Buffer{data: b, cap_: cap(b)}
	}

	if v := bp.pools[level].Get(); v != nil {
		buf := v.(*Buffer)
		buf.data = buf.data[:size]
		bp.stats.Hits[level]++
		return buf
	}

	bp.stats.Misses[level]++
	bp.stats.Allocs[level]++
	bp.totalMem.Add(int64(levelSize(level)))
	b := make([]byte, size)
	return &Buffer{data: b, cap_: cap(b)}
}

// Put returns a buffer to the pool. Sensitive data is zeroed.
// Buffers larger than the largest pool level are discarded.
func (bp *BufferPool) Put(buf *Buffer) {
	level := GetLevel(buf.cap_)
	if level == levelHuge {
		return // discard oversized buffers
	}

	// Zero sensitive data
	for i := range buf.data {
		buf.data[i] = 0
	}
	buf.data = buf.data[:buf.cap_]

	// Check total memory cap
	memCap := bp.cap.Load()
	if memCap > 0 && bp.totalMem.Load() > memCap {
		// Drop buffer to allow GC to reclaim memory
		return
	}

	bp.pools[level].Put(buf)
}

// Stats returns a snapshot of pool statistics.
func (bp *BufferPool) Stats() PoolStats {
	return PoolStats{
		Hits:      bp.stats.Hits,
		Misses:    bp.stats.Misses,
		Allocs:    bp.stats.Allocs,
		TotalMem:  bp.totalMem.Load(),
		HugeAlloc: bp.hugeAlloc.Load(),
	}
}

// SetTotalMemoryCap sets the total memory cap. When exceeded, Put discards buffers.
func (bp *BufferPool) SetTotalMemoryCap(capBytes int64) {
	bp.cap.Store(capBytes)
}

// Default pool singleton.
var (
	defaultPool     *BufferPool
	defaultPoolOnce sync.Once
)

// Default returns the global default BufferPool.
func Default() *BufferPool {
	defaultPoolOnce.Do(func() {
		defaultPool = New()
	})
	return defaultPool
}

// GetDefault gets a buffer from the default pool.
func GetDefault(size int) *Buffer { return Default().Get(size) }

// PutDefault returns a buffer to the default pool.
func PutDefault(buf *Buffer) { Default().Put(buf) }
