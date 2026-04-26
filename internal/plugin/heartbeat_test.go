package plugin

import (
	"iter"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/types"
)

func TestTimeWheel_AddAndTimeout(t *testing.T) {
	var triggered atomic.Int64
	tw := NewTimeWheel(10*time.Millisecond, 10, func(id uint64) {
		triggered.Add(1)
	})
	defer tw.Stop()

	tw.Add(42, 30*time.Millisecond)

	// Wait enough time for the time wheel to advance and detect expiry
	time.Sleep(150 * time.Millisecond)

	if triggered.Load() == 0 {
		t.Fatal("expected timeout callback to fire for session 42")
	}
}

func TestTimeWheel_Remove(t *testing.T) {
	var triggered atomic.Int64
	tw := NewTimeWheel(10*time.Millisecond, 10, func(id uint64) {
		triggered.Add(1)
	})
	defer tw.Stop()

	tw.Add(99, 30*time.Millisecond)
	tw.Remove(99)

	time.Sleep(150 * time.Millisecond)

	if triggered.Load() != 0 {
		t.Fatal("expected no timeout callback after Remove")
	}
}

func TestTimeWheel_ResetDelaysTimeout(t *testing.T) {
	var triggered atomic.Int64
	tw := NewTimeWheel(20*time.Millisecond, 50, func(id uint64) {
		triggered.Add(1)
	})
	defer tw.Stop()

	// Add with 200ms timeout
	tw.Add(7, 200*time.Millisecond)

	// Wait 60ms, then reset with a fresh 400ms timeout
	time.Sleep(60 * time.Millisecond)
	tw.Reset(7, 400*time.Millisecond)

	// At ~260ms mark (past original 200ms timeout), should NOT have fired
	time.Sleep(200 * time.Millisecond)
	firedEarly := triggered.Load()

	// Wait well past the reset timeout (60+400=460ms + generous buffer)
	time.Sleep(400 * time.Millisecond)
	firedLate := triggered.Load()

	if firedEarly != 0 {
		t.Fatal("timeout fired before reset expiry -- Reset did not delay")
	}
	if firedLate == 0 {
		t.Fatal("timeout did not fire after reset expiry")
	}
}

func TestTimeWheel_Stop(t *testing.T) {
	tw := NewTimeWheel(10*time.Millisecond, 10, nil)
	tw.Stop()
	// Should not panic after Stop
	time.Sleep(50 * time.Millisecond)
}

func TestHeartbeatPlugin_OnAcceptAddsToTimeWheel(t *testing.T) {
	var closedIDs []uint64
	var mu sync.Mutex

	mgr := func() types.SessionManager {
		return &mockSessionManager{
			getFn: func(id uint64) (types.RawSession, bool) {
				mu.Lock()
				defer mu.Unlock()
				closedIDs = append(closedIDs, id)
				s := newMockSession(id, "127.0.0.1")
				_ = s.Close()
				return s, true
			},
		}
	}

	p := NewHeartbeatPlugin(10*time.Millisecond, 30*time.Millisecond, mgr)
	defer p.Close()

	sess := newMockSession(1, "127.0.0.1")
	if err := p.OnAccept(sess); err != nil {
		t.Fatalf("OnAccept: %v", err)
	}

	// Wait for the timeout to fire
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	found := len(closedIDs) > 0
	mu.Unlock()

	if !found {
		t.Fatal("expected session to be closed by heartbeat timeout")
	}
}

func TestHeartbeatPlugin_OnMessageResetsTimeout(t *testing.T) {
	timeoutFired := atomic.Int32{}

	mgr := func() types.SessionManager {
		return &mockSessionManager{
			getFn: func(id uint64) (types.RawSession, bool) {
				timeoutFired.Store(1)
				s := newMockSession(id, "127.0.0.1")
				_ = s.Close()
				return s, true
			},
		}
	}

	p := NewHeartbeatPlugin(10*time.Millisecond, 40*time.Millisecond, mgr)
	defer p.Close()

	sess := newMockSession(1, "127.0.0.1")
	_ = p.OnAccept(sess)

	// Rapidly send messages to keep resetting the timeout
	for i := 0; i < 5; i++ {
		time.Sleep(15 * time.Millisecond)
		_, _ = p.OnMessage(sess, []byte("ping"))
	}

	// After ~75ms of activity, the timeout should not have fired
	if timeoutFired.Load() == 1 {
		t.Fatal("heartbeat timeout fired despite active messages")
	}
}

func TestHeartbeatPlugin_OnCloseRemovesFromTimeWheel(t *testing.T) {
	timeoutFired := atomic.Int32{}

	mgr := func() types.SessionManager {
		return &mockSessionManager{
			getFn: func(id uint64) (types.RawSession, bool) {
				timeoutFired.Store(1)
				s := newMockSession(id, "127.0.0.1")
				_ = s.Close()
				return s, true
			},
		}
	}

	p := NewHeartbeatPlugin(10*time.Millisecond, 30*time.Millisecond, mgr)
	defer p.Close()

	sess := newMockSession(1, "127.0.0.1")
	_ = p.OnAccept(sess)
	p.OnClose(sess)

	time.Sleep(200 * time.Millisecond)

	if timeoutFired.Load() == 1 {
		t.Fatal("timeout fired after OnClose removed session from time wheel")
	}
}

func TestHeartbeatPlugin_NamePriority(t *testing.T) {
	p := NewHeartbeatPlugin(time.Second, 5*time.Second, nil)
	defer p.Close()

	if p.Name() != "heartbeat" {
		t.Fatalf("expected name 'heartbeat', got %q", p.Name())
	}
	if p.Priority() != 30 {
		t.Fatalf("expected priority 30, got %d", p.Priority())
	}
}

// ---------------------------------------------------------------------------
// mockSessionManager
// ---------------------------------------------------------------------------

type mockSessionManager struct {
	getFn func(id uint64) (types.RawSession, bool)
}

func (m *mockSessionManager) Register(types.RawSession) error        { return nil }
func (m *mockSessionManager) Unregister(uint64)                      {}
func (m *mockSessionManager) Get(id uint64) (types.RawSession, bool) { return m.getFn(id) }
func (m *mockSessionManager) Count() int64                           { return 0 }
func (m *mockSessionManager) All() iter.Seq[types.RawSession]        { return nil }
func (m *mockSessionManager) Range(func(types.RawSession) bool)      {}
func (m *mockSessionManager) Broadcast([]byte)                       {}
func (m *mockSessionManager) Close() error                           { return nil }
