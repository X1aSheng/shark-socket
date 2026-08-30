package cache

import (
	"context"
	"testing"
	"time"
)

func TestMemoryCacheSetGetAndExpire(t *testing.T) {
	c := NewMemory()
	c.Set("k", []byte("v"), 10*time.Millisecond)
	if got, ok := c.Get("k"); !ok || string(got) != "v" {
		t.Fatalf("got %q ok=%v", got, ok)
	}
	time.Sleep(20 * time.Millisecond)
	if _, ok := c.Get("k"); ok {
		t.Fatal("value did not expire")
	}
}

func TestMemoryCacheMaintenance(t *testing.T) {
	c := NewMemory()
	c.Set("a", []byte("1"), 0)
	c.Set("b", []byte("2"), time.Millisecond)
	if !c.Has("a") {
		t.Fatal("expected key a")
	}
	time.Sleep(5 * time.Millisecond)
	if removed := c.Sweep(time.Now()); removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if got := c.Len(); got != 1 {
		t.Fatalf("len = %d, want 1", got)
	}
	c.Clear()
	if got := c.Len(); got != 0 {
		t.Fatalf("len after clear = %d, want 0", got)
	}
}

// TestMemoryCacheStartSweeper covers the background sweeper (previously 0%):
// a short-TTL entry is removed by the periodic goroutine without any caller
// touching the cache.
func TestMemoryCacheStartSweeper(t *testing.T) {
	c := NewMemory()
	c.Set("k", []byte("v"), 50*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.StartSweeper(ctx, 10*time.Millisecond)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, ok := c.Get("k"); !ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("background sweeper did not remove the expired entry")
}

// TestMemoryCacheDelete covers Delete (previously 0%): existing keys are
// removed and deleting a missing key is a no-op.
func TestMemoryCacheDelete(t *testing.T) {
	c := NewMemory()
	c.Set("k", []byte("v"), 0)
	c.Delete("k")
	if c.Has("k") {
		t.Fatal("deleted key still present")
	}
	c.Delete("missing") // must not panic
	if got := c.Len(); got != 0 {
		t.Fatalf("len = %d, want 0", got)
	}
}
