package session

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/types"
)

func newTestBase() *BaseSession {
	return NewBase(1, types.TCP,
		&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234},
		&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080},
	)
}

// --- NewBase starts in Connecting state ---

func TestNewBase_InitialState(t *testing.T) {
	b := newTestBase()

	if b.State() != types.Connecting {
		t.Errorf("initial state = %v, want Connecting", b.State())
	}
	if b.IsAlive() {
		t.Error("IsAlive() = true for Connecting state, want false")
	}
	if b.ID() != 1 {
		t.Errorf("ID() = %d, want 1", b.ID())
	}
	if b.Protocol() != types.TCP {
		t.Errorf("Protocol() = %v, want TCP", b.Protocol())
	}
	if b.RemoteAddr() == nil {
		t.Error("RemoteAddr() is nil")
	}
	if b.LocalAddr() == nil {
		t.Error("LocalAddr() is nil")
	}
	if b.CreatedAt().IsZero() {
		t.Error("CreatedAt() is zero")
	}
	if b.Context() == nil {
		t.Error("Context() is nil")
	}
}

// --- SetState CAS transitions ---

func TestSetState_CAS_Transitions(t *testing.T) {
	b := newTestBase()

	// Connecting -> Active
	if !b.SetState(types.Active) {
		t.Fatal("SetState(Active) from Connecting returned false")
	}
	if b.State() != types.Active {
		t.Fatalf("state = %v, want Active", b.State())
	}

	// Active -> Closing
	if !b.SetState(types.Closing) {
		t.Fatal("SetState(Closing) from Active returned false")
	}
	if b.State() != types.Closing {
		t.Fatalf("state = %v, want Closing", b.State())
	}

	// Closing -> Closed
	if !b.SetState(types.Closed) {
		t.Fatal("SetState(Closed) from Closing returned false")
	}
	if b.State() != types.Closed {
		t.Fatalf("state = %v, want Closed", b.State())
	}
}

// --- SetState same state returns false ---

func TestSetState_SameState_ReturnsFalse(t *testing.T) {
	b := newTestBase()

	// Already in Connecting
	if b.SetState(types.Connecting) {
		t.Fatal("SetState(Connecting) on Connecting returned true, want false")
	}

	// Transition to Active, then try again
	b.SetState(types.Active)
	if b.SetState(types.Active) {
		t.Fatal("SetState(Active) on Active returned true, want false")
	}
}

func TestSetState_InvalidTransitions(t *testing.T) {
	// Closed → Active is invalid
	b := newTestBase()
	b.SetState(types.Active)
	b.SetState(types.Closed)

	if b.SetState(types.Active) {
		t.Fatal("SetState(Active) from Closed should be rejected")
	}
	if b.State() != types.Closed {
		t.Fatalf("state should remain Closed, got %v", b.State())
	}

	// Connecting → Closing is invalid
	b2 := newTestBase()
	if b2.SetState(types.Closing) {
		t.Fatal("SetState(Closing) from Connecting should be rejected")
	}
	if b2.State() != types.Connecting {
		t.Fatalf("state should remain Connecting, got %v", b2.State())
	}

	// Active → Connecting is invalid
	b3 := newTestBase()
	b3.SetState(types.Active)
	if b3.SetState(types.Connecting) {
		t.Fatal("SetState(Connecting) from Active should be rejected")
	}
}

// --- IsAlive only true when Active ---

func TestIsAlive_OnlyActive(t *testing.T) {
	states := []types.SessionState{types.Connecting, types.Active, types.Closing, types.Closed}
	want := map[types.SessionState]bool{
		types.Connecting: false,
		types.Active:     true,
		types.Closing:    false,
		types.Closed:     false,
	}

	for _, s := range states {
		bb := newTestBase()
		if s != types.Connecting {
			bb.SetState(s)
		}
		got := bb.IsAlive()
		if got != want[s] {
			t.Errorf("IsAlive() in state %v = %v, want %v", s, got, want[s])
		}
	}
}

// --- TouchActive updates LastActiveAt ---

func TestTouchActive_UpdatesLastActiveAt(t *testing.T) {
	b := newTestBase()

	before := b.LastActiveAt()
	time.Sleep(10 * time.Millisecond)

	b.TouchActive()
	after := b.LastActiveAt()

	if !after.After(before) {
		t.Errorf("LastActiveAt after TouchActive = %v, not after %v", after, before)
	}
}

// --- DoClose transitions to Closed and cancels context ---

func TestDoClose_TransitionsAndCancels(t *testing.T) {
	b := newTestBase()
	b.SetState(types.Active)

	ctx := b.Context()
	if ctx.Err() != nil {
		t.Fatal("context already cancelled before DoClose")
	}

	b.DoClose()

	if b.State() != types.Closed {
		t.Errorf("state after DoClose = %v, want Closed", b.State())
	}
	if ctx.Err() == nil {
		t.Error("context not cancelled after DoClose")
	}
}

// --- Metadata SetMeta/GetMeta/DelMeta ---

func TestMetadata_Operations(t *testing.T) {
	b := newTestBase()

	// Get nonexistent key
	if _, ok := b.GetMeta("foo"); ok {
		t.Error("GetMeta(nonexistent) returned true, want false")
	}

	// Set and Get
	b.SetMeta("foo", "bar")
	val, ok := b.GetMeta("foo")
	if !ok {
		t.Fatal("GetMeta(foo) returned false after SetMeta")
	}
	if val != "bar" {
		t.Errorf("GetMeta(foo) = %v, want bar", val)
	}

	// Overwrite
	b.SetMeta("foo", 42)
	val, ok = b.GetMeta("foo")
	if !ok {
		t.Fatal("GetMeta(foo) returned false after overwrite")
	}
	if val != 42 {
		t.Errorf("GetMeta(foo) = %v, want 42", val)
	}

	// Delete
	b.DelMeta("foo")
	if _, ok := b.GetMeta("foo"); ok {
		t.Error("GetMeta(foo) returned true after DelMeta")
	}
}

// --- Concurrent metadata access ---

func TestMetadata_Concurrent(t *testing.T) {
	b := newTestBase()
	const goroutines = 100
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(goroutines * 3)

	// Concurrent writers
	for i := range goroutines {
		go func(i int) {
			defer wg.Done()
			for j := range iterations {
				b.SetMeta("key", i*iterations+j)
			}
		}(i)
	}

	// Concurrent readers
	for range goroutines {
		go func() {
			defer wg.Done()
			for range iterations {
				b.GetMeta("key")
			}
		}()
	}

	// Concurrent deleters
	for i := range goroutines {
		go func(i int) {
			defer wg.Done()
			for j := range iterations {
				key := "del-" + string(rune(i*iterations+j))
				b.SetMeta(key, i)
				b.DelMeta(key)
			}
		}(i)
	}

	wg.Wait()
}
