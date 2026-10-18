package runtime

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// TestSessionManagerCapacityUnderConcurrency is the invariant that sharding
// put at risk. The cap is no longer checked under one global lock, so it is
// claimed with a CAS on the shared counter instead: no matter how many
// goroutines race, the number of admitted sessions must land exactly on max,
// and the count must agree with the number of sessions actually stored.
func TestSessionManagerCapacityUnderConcurrency(t *testing.T) {
	const (
		max      = 500
		attempts = 20000
	)
	m := NewSessionManager(WithMaxSessions(max))

	var admitted, rejected atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < attempts/16; i++ {
				sess := &fakeSession{id: m.NextID()}
				if err := m.Register(sess); err == nil {
					admitted.Add(1)
				} else if errors.Is(err, core.ErrSessionCapacity) {
					rejected.Add(1)
				} else {
					t.Errorf("Register err = %v, want nil or capacity", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if got := admitted.Load(); got != max {
		t.Errorf("admitted = %d, want exactly %d", got, max)
	}
	if got := m.Count(); got != max {
		t.Errorf("Count() = %d, want %d", got, max)
	}
	// Cross-check the counter against what the shards actually hold: a leak
	// in either direction would leave the cap permanently wrong.
	var stored int64
	for i := range m.shards {
		stored += int64(len(m.shards[i].sessions))
	}
	if stored != max {
		t.Errorf("sessions stored across shards = %d, want %d", stored, max)
	}
}

// TestSessionManagerDuplicateRegisterReleasesCapacity pins the rollback of the
// capacity reservation: a rejected duplicate must give its slot back, or a
// transport that retries an ID would silently erode the session ceiling.
func TestSessionManagerDuplicateRegisterReleasesCapacity(t *testing.T) {
	m := NewSessionManager(WithMaxSessions(1))
	id := m.NextID()
	if err := m.Register(&fakeSession{id: id}); err != nil {
		t.Fatal(err)
	}
	if err := m.Register(&fakeSession{id: id}); !errors.Is(err, core.ErrDuplicateSession) {
		t.Fatalf("duplicate register err = %v, want %v", err, core.ErrDuplicateSession)
	}
	if got := m.Count(); got != 1 {
		t.Fatalf("Count() = %d after a rejected duplicate, want 1 (the slot must be released)", got)
	}
	m.Unregister(id)
	if err := m.Register(&fakeSession{id: m.NextID()}); err != nil {
		t.Fatalf("register after unregister err = %v, want nil (capacity was leaked)", err)
	}
}

// TestSessionManagerUnregisterAbsentKeepsCount guards the counter against the
// double-unregister pattern transports use in their deferred cleanup: the
// count must only move when a session was really removed.
func TestSessionManagerUnregisterAbsentKeepsCount(t *testing.T) {
	m := NewSessionManager()
	id := m.NextID()
	if err := m.Register(&fakeSession{id: id}); err != nil {
		t.Fatal(err)
	}
	if m.Unregister(id) != true {
		t.Fatal("first Unregister = false, want true")
	}
	if m.Unregister(id) != false {
		t.Fatal("second Unregister = true, want false")
	}
	if got := m.Count(); got != 0 {
		t.Fatalf("Count() = %d, want 0 (a double unregister must not underflow it)", got)
	}
}

// TestSessionManagerShardsDistribute checks the assumption the whole change
// rests on: IDs from the global counter must spread over the shards. If they
// clustered, 64 locks would behave like one.
func TestSessionManagerShardsDistribute(t *testing.T) {
	m := NewSessionManager()
	const n = 6400
	seen := make(map[uint64]int)
	for i := 0; i < n; i++ {
		id := m.NextID()
		if err := m.Register(&fakeSession{id: id}); err != nil {
			t.Fatal(err)
		}
		seen[id%sessionShards]++
	}
	if len(seen) != sessionShards {
		t.Errorf("sessions spread over %d shards, want %d", len(seen), sessionShards)
	}
	for shard, count := range seen {
		// With n = 100 per shard and a deterministic modulo spread, anything
		// far from even means the ID counter no longer distributes.
		if count < n/sessionShards/2 || count > n/sessionShards*2 {
			t.Errorf("shard %d holds %d sessions, want about %d", shard, count, n/sessionShards)
		}
	}
}

// TestSessionManagerConcurrentRegisterGetUnregister is the race-detector
// companion: the shards are only correct if the counter, the per-shard maps
// and the sessions in them are all accessed under the right locks.
func TestSessionManagerConcurrentRegisterGetUnregister(t *testing.T) {
	m := NewSessionManager()
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				sess := &fakeSession{id: m.NextID()}
				if err := m.Register(sess); err != nil {
					t.Errorf("Register err = %v", err)
					return
				}
				if _, ok := m.Get(sess.ID()); !ok {
					t.Errorf("Get(%d) missed right after Register", sess.ID())
					return
				}
				m.Unregister(sess.ID())
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				m.Snapshot()
			}
		}()
	}
	wg.Wait()
	if got := m.Count(); got != 0 {
		t.Errorf("Count() = %d after all goroutines unregistered, want 0", got)
	}
}

// TestSessionManagerRangeVisitsEveryShard pins that Range and Snapshot walk
// all shards, not just one: with a single table a bug here was impossible.
func TestSessionManagerRangeVisitsEveryShard(t *testing.T) {
	m := NewSessionManager()
	const n = 500
	for i := 0; i < n; i++ {
		if err := m.Register(&fakeSession{id: m.NextID()}); err != nil {
			t.Fatal(err)
		}
	}
	var visited int
	m.Range(func(core.Session) bool {
		visited++
		return true
	})
	if visited != n {
		t.Errorf("Range visited %d sessions, want %d", visited, n)
	}
	if got := len(m.Snapshot()); got != n {
		t.Errorf("Snapshot length = %d, want %d", got, n)
	}
}
