package quic

import (
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

func TestQUIC_SessionID(t *testing.T) {
	// QUIC session requires a real quic-go Conn; test only ID/stub methods
	sess := &session{id: 42}
	if got := sess.ID(); got != 42 {
		t.Errorf("ID() = %d, want 42", got)
	}
	if got := sess.Protocol(); got != core.ProtocolQUIC {
		t.Errorf("Protocol() = %v, want %v", got, core.ProtocolQUIC)
	}
}

func TestQUIC_SessionMeta(t *testing.T) {
	sess := &session{id: 1}
	// Verify that the sync.Map-based meta store works correctly.
	sess.SetMeta("key1", "value1")
	v, ok := sess.GetMeta("key1")
	if !ok || v != "value1" {
		t.Errorf("GetMeta(key1) = (%v, %v), want (value1, true)", v, ok)
	}
	// Delete and verify.
	sess.DelMeta("key1")
	v, ok = sess.GetMeta("key1")
	if ok {
		t.Errorf("GetMeta(key1) after DelMeta = (%v, %v), want (_, false)", v, ok)
	}
	// Missing key.
	v, ok = sess.GetMeta("nonexistent")
	if ok {
		t.Errorf("GetMeta(nonexistent) = (%v, %v), want (_, false)", v, ok)
	}
}

func TestQUIC_SessionTimestamps(t *testing.T) {
	sess := &session{id: 1, createdAt: time.Now()}
	if sess.CreatedAt().IsZero() {
		t.Error("CreatedAt() is zero")
	}
	sess.activeAt.Store(time.Now().UnixNano())
	la := sess.LastActiveAt()
	if la.IsZero() {
		t.Error("LastActiveAt() is zero")
	}
	// touch should update the active timestamp.
	time.Sleep(time.Millisecond)
	sess.touch()
	if !sess.LastActiveAt().After(la) {
		t.Error("LastActiveAt() did not advance after touch()")
	}
}
