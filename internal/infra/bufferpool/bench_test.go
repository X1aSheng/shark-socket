package bufferpool

import (
	"testing"
)

// BenchmarkPoolGetPutByLevel benchmarks Get+Put for each pool level.
// This should be much faster than direct allocation because sync.Pool
// recycles buffers across calls.
// Sizes map to levels: 64->Micro, 1024->Tiny, 4096->Small, 16384->Medium, 131072->Large.
func BenchmarkPoolGetPutByLevel(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"Micro_64", 64},
		{"Tiny_1024", 1024},
		{"Small_4096", 4096},
		{"Medium_16384", 16384},
		{"Large_131072", 131072},
	}

	for _, s := range sizes {
		b.Run(s.name, func(b *testing.B) {
			pool := New()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf := pool.Get(s.size)
				pool.Put(buf)
			}
		})
	}
}

// BenchmarkBufferPoolDirectAlloc benchmarks direct make([]byte, n) for comparison
// with the pooled Get+Put path. This is the baseline the pool should beat.
func BenchmarkBufferPoolDirectAlloc(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"Micro_64", 64},
		{"Tiny_1024", 1024},
		{"Small_4096", 4096},
		{"Medium_16384", 16384},
		{"Large_131072", 131072},
	}

	for _, s := range sizes {
		b.Run(s.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = make([]byte, s.size)
			}
		})
	}
}

// BenchmarkBufferPoolParallel benchmarks concurrent Get+Put to measure
// lock contention across the sharded sync.Pool levels.
func BenchmarkBufferPoolParallel(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"Micro_64", 64},
		{"Small_4096", 4096},
		{"Large_131072", 131072},
	}

	for _, s := range sizes {
		b.Run(s.name, func(b *testing.B) {
			pool := New()
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					buf := pool.Get(s.size)
					pool.Put(buf)
				}
			})
		})
	}
}

// BenchmarkBufferPoolGetLevel benchmarks the GetLevel size-to-level mapping.
func BenchmarkBufferPoolGetLevel(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		GetLevel(64)
		GetLevel(256)
		GetLevel(2048)
		GetLevel(16384)
		GetLevel(131072)
	}
}

// BenchmarkBufferPoolDefaultSingleton benchmarks the global default pool.
func BenchmarkBufferPoolDefaultSingleton(b *testing.B) {
	_ = Default() // ensure initialized
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := GetDefault(256)
		PutDefault(buf)
	}
}

// BenchmarkBufferPoolHugeAlloc benchmarks allocation at sizes above the
// largest pool level (direct allocation, no pooling).
func BenchmarkBufferPoolHugeAlloc(b *testing.B) {
	pool := New()
	size := 300000 // above largeThreshold (262144)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pool.Get(size)
	}
}

// TestBenchmarkLevelsExist is a sanity check that the benchmark sizes
// map to the expected pool levels.
func TestBenchmarkLevelsExist(t *testing.T) {
	cases := []struct {
		size     int
		expected int
	}{
		{64, levelMicro},
		{1024, levelTiny},
		{4096, levelSmall},
		{16384, levelMedium},
		{131072, levelLarge},
	}
	for _, tc := range cases {
		got := GetLevel(tc.size)
		if got != tc.expected {
			t.Errorf("GetLevel(%d) = %d, want %d", tc.size, got, tc.expected)
		}
	}
}

// TestBenchmarkPoolGetPutCorrectness verifies the pool returns usable buffers.
func TestBenchmarkPoolGetPutCorrectness(t *testing.T) {
	pool := New()
	for _, size := range []int{64, 1024, 4096, 16384, 131072} {
		buf := pool.Get(size)
		if buf == nil {
			t.Errorf("Get(%d) returned nil", size)
			continue
		}
		if len(buf.Bytes()) != size {
			t.Errorf("Get(%d) returned buffer with len=%d, want %d", size, len(buf.Bytes()), size)
		}
		pool.Put(buf)
	}
}
