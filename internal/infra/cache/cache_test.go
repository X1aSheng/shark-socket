package cache

import (
	"context"
	"sync"
	"testing"
	"time"
)

func newTestCache(opts ...CacheOption) *MemoryCache {
	return NewMemoryCache(append([]CacheOption{WithCleanInterval(50 * time.Millisecond)}, opts...)...)
}

func TestMemoryCache_SetGetDel(t *testing.T) {
	c := newTestCache()
	defer c.Close()
	ctx := context.Background()

	key := "hello"
	val := []byte("world")

	// Set
	if err := c.Set(ctx, key, val, 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Get
	got, err := c.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != string(val) {
		t.Errorf("Get(%q) = %q, want %q", key, got, val)
	}

	// Del
	if err := c.Del(ctx, key); err != nil {
		t.Fatalf("Del: %v", err)
	}

	// Get after delete
	_, err = c.Get(ctx, key)
	if err != ErrCacheMiss {
		t.Errorf("Get after Del: err=%v, want ErrCacheMiss", err)
	}
}

func TestMemoryCache_GetMissingKey(t *testing.T) {
	c := newTestCache()
	defer c.Close()
	ctx := context.Background()

	_, err := c.Get(ctx, "nonexistent")
	if err != ErrCacheMiss {
		t.Errorf("Get(nonexistent): err=%v, want ErrCacheMiss", err)
	}
}

func TestMemoryCache_Exists(t *testing.T) {
	c := newTestCache()
	defer c.Close()
	ctx := context.Background()

	ok, err := c.Exists(ctx, "missing")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if ok {
		t.Error("Exists(missing) = true, want false")
	}

	if err := c.Set(ctx, "present", []byte("data"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	ok, err = c.Exists(ctx, "present")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !ok {
		t.Error("Exists(present) = false, want true")
	}
}

func TestMemoryCache_TTLExpiry(t *testing.T) {
	c := newTestCache()
	defer c.Close()
	ctx := context.Background()

	key := "expiring"
	val := []byte("soon-gone")

	if err := c.Set(ctx, key, val, 200*time.Millisecond); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Immediately available.
	got, err := c.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get before expiry: %v", err)
	}
	if string(got) != string(val) {
		t.Errorf("Get before expiry = %q, want %q", got, val)
	}

	// Wait for TTL to pass.
	time.Sleep(300 * time.Millisecond)

	_, err = c.Get(ctx, key)
	if err != ErrCacheMiss {
		t.Errorf("Get after expiry: err=%v, want ErrCacheMiss", err)
	}
}

func TestMemoryCache_TTLMethod(t *testing.T) {
	c := newTestCache()
	defer c.Close()
	ctx := context.Background()

	// Non-existent key.
	_, err := c.TTL(ctx, "missing")
	if err != ErrCacheMiss {
		t.Errorf("TTL(missing): err=%v, want ErrCacheMiss", err)
	}

	// Key with no TTL (zero duration passed to Set).
	if err := c.Set(ctx, "noexpiry", []byte("val"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	ttl, err := c.TTL(ctx, "noexpiry")
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if ttl != 0 {
		t.Errorf("TTL(noexpiry) = %v, want 0 (no expiry)", ttl)
	}

	// Key with TTL.
	if err := c.Set(ctx, "withttl", []byte("val"), 5*time.Second); err != nil {
		t.Fatalf("Set: %v", err)
	}
	ttl, err = c.TTL(ctx, "withttl")
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if ttl <= 0 || ttl > 5*time.Second {
		t.Errorf("TTL(withttl) = %v, want roughly 5s remaining", ttl)
	}
}

func TestMemoryCache_Exists_Expired(t *testing.T) {
	c := newTestCache()
	defer c.Close()
	ctx := context.Background()

	if err := c.Set(ctx, "ephemeral", []byte("val"), 100*time.Millisecond); err != nil {
		t.Fatalf("Set: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	ok, err := c.Exists(ctx, "ephemeral")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if ok {
		t.Error("Exists(ephemeral) = true after expiry, want false")
	}
}

func TestMemoryCache_MGet(t *testing.T) {
	c := newTestCache()
	defer c.Close()
	ctx := context.Background()

	if err := c.Set(ctx, "a", []byte("1"), 0); err != nil {
		t.Fatalf("Set a: %v", err)
	}
	if err := c.Set(ctx, "b", []byte("2"), 0); err != nil {
		t.Fatalf("Set b: %v", err)
	}
	// key "c" is intentionally not set.

	result, err := c.MGet(ctx, []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("MGet: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("MGet returned %d entries, want 2", len(result))
	}
	if string(result["a"]) != "1" {
		t.Errorf("MGet[a] = %q, want %q", result["a"], "1")
	}
	if string(result["b"]) != "2" {
		t.Errorf("MGet[b] = %q, want %q", result["b"], "2")
	}
	if _, ok := result["c"]; ok {
		t.Error("MGet[c] should not be present")
	}
}

func TestMemoryCache_MGet_EmptyKeys(t *testing.T) {
	c := newTestCache()
	defer c.Close()
	ctx := context.Background()

	result, err := c.MGet(ctx, []string{})
	if err != nil {
		t.Fatalf("MGet: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("MGet(empty) returned %d entries, want 0", len(result))
	}
}

func TestMemoryCache_ConcurrentReadWrite(t *testing.T) {
	c := newTestCache()
	defer c.Close()
	ctx := context.Background()

	const goroutines = 50
	const iterations = 200

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			key := []string{"k0", "k1", "k2", "k3", "k4"}[id%5]
			for i := 0; i < iterations; i++ {
				switch i % 3 {
				case 0:
					_ = c.Set(ctx, key, []byte{byte(id)}, 0)
				case 1:
					_, _ = c.Get(ctx, key)
				case 2:
					_ = c.Del(ctx, key)
				}
			}
		}(g)
	}

	wg.Wait()
}

func TestMemoryCache_ConcurrentExistsAndTTL(t *testing.T) {
	c := newTestCache()
	defer c.Close()
	ctx := context.Background()

	_ = c.Set(ctx, "shared", []byte("v"), 5*time.Second)

	const goroutines = 30
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				switch id % 3 {
				case 0:
					_, _ = c.Exists(ctx, "shared")
				case 1:
					_, _ = c.TTL(ctx, "shared")
				case 2:
					_ = c.Set(ctx, "shared", []byte{byte(i)}, 5*time.Second)
				}
			}
		}(g)
	}

	wg.Wait()
}

func TestMemoryCache_Close_StopsCleanup(t *testing.T) {
	c := NewMemoryCache(WithCleanInterval(10 * time.Millisecond))
	ctx := context.Background()

	// Set an entry with a short TTL.
	_ = c.Set(ctx, "temp", []byte("val"), 50*time.Millisecond)

	// Close immediately.
	c.Close()

	// Wait longer than the TTL + clean interval. The cleanup goroutine
	// should have stopped, so the entry will NOT be proactively cleaned
	// (lazy expiry still applies on Get).
	time.Sleep(100 * time.Millisecond)

	// Lazy expiry should still work: Get detects the expired entry.
	_, err := c.Get(ctx, "temp")
	if err != ErrCacheMiss {
		t.Errorf("Get(expired after close): err=%v, want ErrCacheMiss", err)
	}
}

func TestMemoryCache_CleanupRemovesExpired(t *testing.T) {
	c := newTestCache(WithCleanInterval(50 * time.Millisecond))
	defer c.Close()
	ctx := context.Background()

	_ = c.Set(ctx, "short", []byte("val"), 80*time.Millisecond)

	// Wait for TTL to pass and cleanup to run.
	time.Sleep(200 * time.Millisecond)

	// The cleanup goroutine should have removed the entry from the map.
	c.mu.RLock()
	_, exists := c.entries["short"]
	c.mu.RUnlock()

	if exists {
		t.Error("expected 'short' entry to be removed by cleanup goroutine")
	}
}
