package cache

import (
	"testing"
	"time"
)

// TestNegativeTTLIsImmediatelyExpired verifies that a negative TTL means
// "already expired", not "never expires". Callers computing a TTL from a
// deadline (time.Until) legitimately produce negative values for a deadline
// that has already passed; treating those as immortal entries leaks memory
// and serves stale data.
func TestNegativeTTLIsImmediatelyExpired(t *testing.T) {
	c := NewMemory()
	c.Set("stale", []byte("v"), -time.Second)
	if _, ok := c.Get("stale"); ok {
		t.Fatal("an entry written with a negative TTL must not be readable")
	}
	if c.Len() != 0 {
		t.Fatalf("len = %d, want 0 (negative-TTL entry retained)", c.Len())
	}
}

// TestZeroTTLIsImmortal verifies that TTL 0 keeps meaning "no expiry": the
// fix for negative TTLs must not turn 0 into an immediate expiry.
func TestZeroTTLIsImmortal(t *testing.T) {
	c := NewMemory()
	c.Set("forever", []byte("v"), 0)
	if _, ok := c.Get("forever"); !ok {
		t.Fatal("an entry written with TTL 0 must be readable")
	}
}
