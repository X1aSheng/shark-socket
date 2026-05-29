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
