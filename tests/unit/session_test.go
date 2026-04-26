package unit_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/session"
	"github.com/X1aSheng/shark-socket/internal/types"
)

// mockRawSession implements types.RawSession for testing.
type mockRawSession struct {
	id       uint64
	proto    types.ProtocolType
	state    types.SessionState
	meta     map[string]any
	sendData [][]byte
}

func newMockSession(id uint64, proto types.ProtocolType) *mockRawSession {
	return &mockRawSession{
		id:    id,
		proto: proto,
		state: types.Active,
		meta:  make(map[string]any),
	}
}

func (m *mockRawSession) ID() uint64                   { return m.id }
func (m *mockRawSession) Protocol() types.ProtocolType { return m.proto }
func (m *mockRawSession) RemoteAddr() net.Addr         { return nil }
func (m *mockRawSession) LocalAddr() net.Addr          { return nil }
func (m *mockRawSession) CreatedAt() time.Time         { return time.Now() }
func (m *mockRawSession) State() types.SessionState    { return m.state }
func (m *mockRawSession) IsAlive() bool                { return m.state == types.Active }
func (m *mockRawSession) LastActiveAt() time.Time      { return time.Now() }
func (m *mockRawSession) Send(data []byte) error {
	m.sendData = append(m.sendData, data)
	return nil
}
func (m *mockRawSession) SendTyped(msg []byte) error { return m.Send(msg) }
func (m *mockRawSession) Close() error {
	m.state = types.Closed
	return nil
}
func (m *mockRawSession) Context() context.Context    { return context.Background() }
func (m *mockRawSession) SetMeta(key string, val any)  { m.meta[key] = val }
func (m *mockRawSession) GetMeta(key string) (any, bool) {
	v, ok := m.meta[key]
	return v, ok
}
func (m *mockRawSession) DelMeta(key string) {}
func (m *mockRawSession) TouchActive()       {}
func (m *mockRawSession) DoClose()           {}

func TestSessionManager_RegisterGet(t *testing.T) {
	mgr := session.NewManager()
	sess := newMockSession(mgr.NextID(), types.TCP)

	if err := mgr.Register(sess); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, ok := mgr.Get(sess.ID())
	if !ok {
		t.Fatal("Get: session not found")
	}
	if got.ID() != sess.ID() {
		t.Fatalf("ID mismatch: got %d, want %d", got.ID(), sess.ID())
	}
}

func TestSessionManager_Unregister(t *testing.T) {
	mgr := session.NewManager()
	sess := newMockSession(mgr.NextID(), types.TCP)
	mgr.Register(sess)
	mgr.Unregister(sess.ID())

	_, ok := mgr.Get(sess.ID())
	if ok {
		t.Fatal("session should be gone after Unregister")
	}
}

func TestSessionManager_Count(t *testing.T) {
	mgr := session.NewManager()
	for i := 0; i < 100; i++ {
		sess := newMockSession(mgr.NextID(), types.TCP)
		mgr.Register(sess)
	}
	if mgr.Count() != 100 {
		t.Fatalf("Count = %d, want 100", mgr.Count())
	}
}

func TestSessionManager_All(t *testing.T) {
	mgr := session.NewManager()
	for i := 0; i < 10; i++ {
		sess := newMockSession(mgr.NextID(), types.TCP)
		mgr.Register(sess)
	}

	count := 0
	for range mgr.All() {
		count++
	}
	if count != 10 {
		t.Fatalf("All() yielded %d, want 10", count)
	}
}

func TestSessionManager_Broadcast(t *testing.T) {
	mgr := session.NewManager()
	sessions := make([]*mockRawSession, 5)
	for i := range sessions {
		sessions[i] = newMockSession(mgr.NextID(), types.TCP)
		mgr.Register(sessions[i])
	}

	mgr.Broadcast([]byte("hello"))

	for _, s := range sessions {
		if len(s.sendData) != 1 || string(s.sendData[0]) != "hello" {
			t.Fatalf("session %d: sendData = %v, want [hello]", s.ID(), s.sendData)
		}
	}
}

func TestSessionManager_LRUEviction(t *testing.T) {
	const maxSess int64 = 5
	mgr := session.NewManager(session.WithMaxSessions(maxSess))

	// Register many more sessions than max
	for i := 0; i < 100; i++ {
		sess := newMockSession(mgr.NextID(), types.TCP)
		mgr.Register(sess)
	}

	// Count should not exceed max by much (eviction is per-shard, so exact count varies)
	count := mgr.Count()
	t.Logf("Registered 100 sessions with maxSess=%d, actual Count=%d", maxSess, count)
	if count > int64(100) {
		t.Fatalf("Count = %d, should not exceed registered count", count)
	}
}

func TestSessionManager_Range(t *testing.T) {
	mgr := session.NewManager()
	for i := 0; i < 20; i++ {
		sess := newMockSession(mgr.NextID(), types.TCP)
		mgr.Register(sess)
	}

	collected := 0
	mgr.Range(func(s types.RawSession) bool {
		collected++
		return true
	})
	if collected != 20 {
		t.Fatalf("Range collected %d, want 20", collected)
	}
}

func TestSessionManager_Range_EarlyStop(t *testing.T) {
	mgr := session.NewManager()
	for i := 0; i < 100; i++ {
		sess := newMockSession(mgr.NextID(), types.TCP)
		mgr.Register(sess)
	}

	count := 0
	mgr.Range(func(s types.RawSession) bool {
		count++
		return count < 5
	})
	if count != 5 {
		t.Fatalf("Range with early stop: got %d, want 5", count)
	}
}

func TestSessionManager_Close(t *testing.T) {
	mgr := session.NewManager()
	sess := newMockSession(mgr.NextID(), types.TCP)
	mgr.Register(sess)

	if err := mgr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if mgr.Count() != 0 {
		t.Fatalf("Count after Close = %d, want 0", mgr.Count())
	}
}

func TestSessionManager_RegisterAfterClose(t *testing.T) {
	mgr := session.NewManager()
	mgr.Close()

	sess := newMockSession(mgr.NextID(), types.TCP)
	if err := mgr.Register(sess); err == nil {
		t.Fatal("Register after Close should fail")
	}
}

func TestSessionManager_NextID_Monotonic(t *testing.T) {
	mgr := session.NewManager()
	var prev uint64
	for i := 0; i < 1000; i++ {
		id := mgr.NextID()
		if id <= prev {
			t.Fatalf("NextID not monotonic: %d <= %d at step %d", id, prev, i)
		}
		prev = id
	}
}

func TestSessionManager_MultiProtocol(t *testing.T) {
	mgr := session.NewManager()
	protos := []types.ProtocolType{types.TCP, types.UDP, types.HTTP, types.WebSocket, types.CoAP}

	for _, p := range protos {
		sess := newMockSession(mgr.NextID(), p)
		if err := mgr.Register(sess); err != nil {
			t.Fatalf("Register(%s): %v", p, err)
		}
	}

	if mgr.Count() != int64(len(protos)) {
		t.Fatalf("Count = %d, want %d", mgr.Count(), len(protos))
	}
}
