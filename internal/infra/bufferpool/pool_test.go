package bufferpool

import (
	"sync"
	"testing"
)

func TestGetLevel(t *testing.T) {
	tests := []struct {
		name      string
		size      int
		wantLevel int
	}{
		{"zero maps to micro", 0, levelMicro},
		{"1 maps to micro", 1, levelMicro},
		{"128 maps to micro", 128, levelMicro},
		{"256 maps to micro", 256, levelMicro},
		{"512 boundary maps to micro", 512, levelMicro},

		{"513 maps to tiny", 513, levelTiny},
		{"1024 maps to tiny", 1024, levelTiny},
		{"2048 boundary maps to tiny", 2048, levelTiny},

		{"2049 maps to small", 2049, levelSmall},
		{"3000 maps to small", 3000, levelSmall},
		{"4096 boundary maps to small", 4096, levelSmall},

		{"4097 maps to medium", 4097, levelMedium},
		{"16384 maps to medium", 16384, levelMedium},
		{"32768 boundary maps to medium", 32768, levelMedium},

		{"32769 maps to large", 32769, levelLarge},
		{"131072 maps to large", 131072, levelLarge},
		{"262144 boundary maps to large", 262144, levelLarge},

		{"262145 maps to huge", 262145, levelHuge},
		{"1000000 maps to huge", 1000000, levelHuge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetLevel(tt.size)
			if got != tt.wantLevel {
				t.Errorf("GetLevel(%d) = %d, want %d", tt.size, got, tt.wantLevel)
			}
		})
	}
}

func TestGet_ReturnsBufferAtLeastRequestedSize(t *testing.T) {
	bp := New()

	sizes := []int{1, 100, 512, 1024, 2048, 4096, 10000, 32768, 100000, 262144, 300000}
	for _, size := range sizes {
		buf := bp.Get(size)
		if buf == nil {
			t.Errorf("Get(%d) returned nil", size)
			continue
		}
		if len(buf.Bytes()) != size {
			t.Errorf("Get(%d) returned buffer with len=%d, want %d", size, len(buf.Bytes()), size)
		}
		if cap(buf.Bytes()) < size {
			t.Errorf("Get(%d) returned buffer with cap=%d, which is less than requested %d", size, cap(buf.Bytes()), size)
		}
	}
}

func TestPut_ReturnsBufferToPool(t *testing.T) {
	bp := New()

	// Get a buffer, return it, get again -- should be a pool hit on second get.
	buf := bp.Get(256)
	if buf == nil {
		t.Fatal("Get(256) returned nil")
	}
	bp.Put(buf)

	// Record stats snapshot after Put.
	statsBefore := bp.Stats()

	buf2 := bp.Get(256)
	_ = buf2

	statsAfter := bp.Stats()

	// Hits should have increased by at least 1 after the second Get.
	hitsDelta := statsAfter.Hits[levelMicro] - statsBefore.Hits[levelMicro]
	if hitsDelta == 0 {
		t.Error("expected hit count to increase after Put then Get, but it did not")
	}
}

func TestPut_ZeroesData(t *testing.T) {
	bp := New()

	buf := bp.Get(64)
	copy(buf.Bytes(), []byte("sensitive-data-here"))
	bp.Put(buf)

	// After Put the buffer data should have been zeroed.
	// We cannot inspect the buffer directly after return (it may be reused),
	// but we can verify by getting again and checking it is all zeros.
	buf2 := bp.Get(64)
	allZero := true
	for _, b := range buf2.Bytes() {
		if b != 0 {
			allZero = false
			break
		}
	}
	if !allZero {
		t.Error("expected recycled buffer data to be zeroed")
	}
	bp.Put(buf2)
}

func TestPut_DiscardsHugeBuffers(t *testing.T) {
	bp := New()

	buf := bp.Get(300000) // > largeThreshold, maps to levelHuge
	if buf == nil {
		t.Fatal("Get(300000) returned nil")
	}

	// Put should silently discard without panic.
	bp.Put(buf)
}

func TestGet_PutsHugeAllocCounter(t *testing.T) {
	bp := New()

	bp.Get(300000)
	bp.Get(500000)

	stats := bp.Stats()
	if stats.HugeAlloc != 2 {
		t.Errorf("expected HugeAlloc=2, got %d", stats.HugeAlloc)
	}
}

func TestBufferPool_ConcurrentGetPut(t *testing.T) {
	bp := New()
	const goroutines = 100
	const iterations = 500

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				size := ((id+i)%6)*100 + 100 // sizes from 100 to 600
				buf := bp.Get(size)
				if buf == nil {
					t.Errorf("goroutine %d: Get(%d) returned nil", id, size)
					return
				}
				if len(buf.Bytes()) < size {
					t.Errorf("goroutine %d: Get(%d) returned buffer len=%d", id, size, len(buf.Bytes()))
					return
				}
				// Write some data to detect races.
				for j := range buf.Bytes() {
					buf.Bytes()[j] = byte(id)
				}
				bp.Put(buf)
			}
		}(g)
	}

	wg.Wait()
}

func TestBuffer_Cap(t *testing.T) {
	bp := New()
	buf := bp.Get(10)
	// Cap should be set.
	if buf.Cap() == 0 {
		t.Error("expected Cap() > 0 for pooled buffer")
	}
	bp.Put(buf)
}

func TestBuffer_Len(t *testing.T) {
	bp := New()
	size := 42
	buf := bp.Get(size)
	if buf.Len() != size {
		t.Errorf("Len() = %d, want %d", buf.Len(), size)
	}
	bp.Put(buf)
}

func TestDefaultPool(t *testing.T) {
	dp1 := Default()
	dp2 := Default()
	if dp1 != dp2 {
		t.Error("Default() should return the same singleton instance")
	}

	buf := GetDefault(128)
	if buf == nil {
		t.Error("GetDefault(128) returned nil")
	}
	PutDefault(buf)
}

func BenchmarkBufferPoolGetPut(b *testing.B) {
	bp := New()
	sizes := []int{64, 256, 1024, 4096, 32768, 262144}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := sizes[i%len(sizes)]
		buf := bp.Get(s)
		bp.Put(buf)
	}
}
