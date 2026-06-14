package websocket

import (
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

func TestWS_SessionID(t *testing.T) {
	// Create session with initialized fields to avoid zero defaults
	now := time.Now()
	sess := &session{
		id:        42,
		createdAt: now,
	}
	sess.state.Store(uint32(core.StateActive))
	sess.activeAt.Store(now.UnixNano())

	if got := sess.ID(); got != 42 {
		t.Errorf("ID() = %d, want 42", got)
	}
	if got := sess.Protocol(); got != core.ProtocolWS {
		t.Errorf("Protocol() = %v, want %v", got, core.ProtocolWS)
	}
	if got := sess.State(); got != core.StateActive {
		t.Errorf("State() = %v, want %v", got, core.StateActive)
	}
	if got := sess.CreatedAt(); got.IsZero() {
		t.Error("CreatedAt() is zero")
	}
	if got := sess.LastActiveAt(); got.IsZero() {
		t.Error("LastActiveAt() is zero")
	}
	sess.SetMeta("k", "v")
	got, ok := sess.GetMeta("k")
	if !ok || got != "v" {
		t.Errorf("GetMeta = (%v, %v)", got, ok)
	}
	sess.DelMeta("k")
	if _, ok := sess.GetMeta("k"); ok {
		t.Error("DelMeta failed")
	}
}
