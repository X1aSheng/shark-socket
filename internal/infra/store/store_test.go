package store

import (
	"context"
	"sync"
	"testing"
)

func TestMemoryStore_SaveLoadDelete(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	key := "user:1"
	val := []byte(`{"name":"alice"}`)

	// Save
	if err := s.Save(ctx, key, val); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Load
	got, err := s.Load(ctx, key)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(got) != string(val) {
		t.Errorf("Load(%q) = %q, want %q", key, got, val)
	}

	// Delete
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Load after delete
	_, err = s.Load(ctx, key)
	if err != ErrNotFound {
		t.Errorf("Load after Delete: err=%v, want ErrNotFound", err)
	}
}

func TestMemoryStore_LoadNonexistent(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	_, err := s.Load(ctx, "does-not-exist")
	if err != ErrNotFound {
		t.Errorf("Load(nonexistent): err=%v, want ErrNotFound", err)
	}
}

func TestMemoryStore_DeleteNonexistent(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	// Deleting a nonexistent key should not error.
	if err := s.Delete(ctx, "ghost"); err != nil {
		t.Errorf("Delete(nonexistent): err=%v, want nil", err)
	}
}

func TestMemoryStore_SaveOverwrite(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	_ = s.Save(ctx, "k", []byte("v1"))
	_ = s.Save(ctx, "k", []byte("v2"))

	got, err := s.Load(ctx, "k")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(got) != "v2" {
		t.Errorf("Load after overwrite = %q, want %q", got, "v2")
	}
}

func TestMemoryStore_QueryPrefixMatch(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	_ = s.Save(ctx, "user:1", []byte("alice"))
	_ = s.Save(ctx, "user:2", []byte("bob"))
	_ = s.Save(ctx, "user:3", []byte("carol"))
	_ = s.Save(ctx, "order:1", []byte("order-data"))
	_ = s.Save(ctx, "product:1", []byte("widget"))

	results, err := s.Query(ctx, "user:")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("Query(user:) returned %d results, want 3", len(results))
	}

	// Verify all returned values are present (order not guaranteed).
	found := map[string]bool{}
	for _, v := range results {
		found[string(v)] = true
	}
	for _, want := range []string{"alice", "bob", "carol"} {
		if !found[want] {
			t.Errorf("Query(user:) missing value %q", want)
		}
	}
}

func TestMemoryStore_QueryNoMatch(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	_ = s.Save(ctx, "user:1", []byte("alice"))

	results, err := s.Query(ctx, "order:")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("Query(order:) returned %d results, want 0", len(results))
	}
}

func TestMemoryStore_QueryEmptyPrefix(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	_ = s.Save(ctx, "a", []byte("1"))
	_ = s.Save(ctx, "b", []byte("2"))

	results, err := s.Query(ctx, "")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Query('') returned %d results, want 2", len(results))
	}
}

func TestMemoryStore_QueryReturnsCopy(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	original := []byte("original")
	_ = s.Save(ctx, "k", original)

	results, _ := s.Query(ctx, "k")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	// Mutate returned slice; original store data should be unaffected.
	results[0][0] = 'X'

	got, _ := s.Load(ctx, "k")
	if string(got) != "original" {
		t.Errorf("Query did not return a copy; store data mutated to %q", got)
	}
}

func TestMemoryStore_ConcurrentAccess(t *testing.T) {
	s := NewMemoryStore()
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
				switch i % 4 {
				case 0:
					_ = s.Save(ctx, key, []byte{byte(id)})
				case 1:
					_, _ = s.Load(ctx, key)
				case 2:
					_ = s.Delete(ctx, key)
				case 3:
					_, _ = s.Query(ctx, "k")
				}
			}
		}(g)
	}

	wg.Wait()
}
