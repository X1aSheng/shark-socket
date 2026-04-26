package utils

import (
	"sync"
	"testing"
)

func TestNewShardedMap(t *testing.T) {
	sm := NewShardedMap[string, int](16)
	if sm == nil {
		t.Fatal("NewShardedMap returned nil")
	}
	if sm.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", sm.Len())
	}
}

func TestShardedMap_SingleShard(t *testing.T) {
	sm := NewShardedMap[string, int](1)
	sm.Set("a", 1)
	sm.Set("b", 2)
	if sm.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", sm.Len())
	}
	v, ok := sm.Get("a")
	if !ok || v != 1 {
		t.Fatalf("Get(a) = %d, %v; want 1, true", v, ok)
	}
}

func TestShardedMap_SetGet(t *testing.T) {
	sm := NewShardedMap[string, int](8)
	sm.Set("hello", 42)
	v, ok := sm.Get("hello")
	if !ok {
		t.Fatal("Get(hello) not found")
	}
	if v != 42 {
		t.Fatalf("Get(hello) = %d, want 42", v)
	}
}

func TestShardedMap_GetMissing(t *testing.T) {
	sm := NewShardedMap[string, int](4)
	_, ok := sm.Get("nonexistent")
	if ok {
		t.Fatal("Get(nonexistent) should return false")
	}
}

func TestShardedMap_Delete(t *testing.T) {
	sm := NewShardedMap[string, int](4)
	sm.Set("key", 10)
	sm.Delete("key")
	_, ok := sm.Get("key")
	if ok {
		t.Fatal("Get(key) should return false after Delete")
	}
	if sm.Len() != 0 {
		t.Fatalf("Len() = %d, want 0 after Delete", sm.Len())
	}
}

func TestShardedMap_DeleteMissing(t *testing.T) {
	sm := NewShardedMap[string, int](4)
	sm.Delete("nope") // should not panic
	if sm.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", sm.Len())
	}
}

func TestShardedMap_Overwrite(t *testing.T) {
	sm := NewShardedMap[string, int](4)
	sm.Set("x", 1)
	sm.Set("x", 2)
	v, ok := sm.Get("x")
	if !ok || v != 2 {
		t.Fatalf("Get(x) = %d, %v; want 2, true", v, ok)
	}
	if sm.Len() != 1 {
		t.Fatalf("Len() = %d, want 1 after overwrite", sm.Len())
	}
}

func TestShardedMap_IntKeys(t *testing.T) {
	sm := NewShardedMap[int, string](8)
	sm.Set(1, "one")
	sm.Set(100, "hundred")
	v, ok := sm.Get(1)
	if !ok || v != "one" {
		t.Fatalf("Get(1) = %q, %v; want 'one', true", v, ok)
	}
	v, ok = sm.Get(100)
	if !ok || v != "hundred" {
		t.Fatalf("Get(100) = %q, %v; want 'hundred', true", v, ok)
	}
}

func TestShardedMap_Uint64Keys(t *testing.T) {
	sm := NewShardedMap[uint64, bool](8)
	sm.Set(0xDEAD, true)
	v, ok := sm.Get(0xDEAD)
	if !ok || !v {
		t.Fatalf("Get(0xDEAD) = %v, %v; want true, true", v, ok)
	}
}

func TestShardedMap_Len(t *testing.T) {
	sm := NewShardedMap[string, int](4)
	for i := 0; i < 100; i++ {
		sm.Set(string(rune(i)), i)
	}
	if sm.Len() != 100 {
		t.Fatalf("Len() = %d, want 100", sm.Len())
	}
}

func TestShardedMap_Concurrent(t *testing.T) {
	sm := NewShardedMap[int, int](16)
	var wg sync.WaitGroup
	const goroutines = 50
	const opsPer = 100

	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(base int) {
			defer wg.Done()
			for i := 0; i < opsPer; i++ {
				key := base + i
				sm.Set(key, key*10)
				sm.Get(key)
				if i%3 == 0 {
					sm.Delete(key)
				}
			}
		}(g * opsPer)
	}
	wg.Wait()
}
