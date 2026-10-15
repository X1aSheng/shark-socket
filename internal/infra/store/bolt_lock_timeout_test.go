package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// TestBoltOpenFailsFastWhenLocked verifies that opening the same BoltDB file
// twice fails within a bounded time instead of hanging forever. bbolt's
// zero-value Options.Timeout means "wait for the file lock indefinitely", so a
// rolling restart that briefly overlaps two processes (or an accidentally
// started second instance) would block the new process before it can even log
// or report unhealthy.
func TestBoltOpenFailsFastWhenLocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locked.db")

	first, err := NewBoltStore(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	defer func() { _ = first.Close() }()

	type result struct {
		store *BoltStore
		err   error
	}
	done := make(chan result, 1)
	go func() {
		s, err := NewBoltStore(path)
		done <- result{store: s, err: err}
	}()

	select {
	case got := <-done:
		if got.err == nil {
			_ = got.store.Close()
			t.Fatal("second open of a locked database succeeded; want an error")
		}
		if !errors.Is(got.err, bolt.ErrTimeout) {
			t.Fatalf("second open error = %v, want bolt.ErrTimeout", got.err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("second open of a locked database blocked indefinitely (no lock timeout)")
	}
}
