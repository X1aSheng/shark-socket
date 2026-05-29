package cache

import (
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
