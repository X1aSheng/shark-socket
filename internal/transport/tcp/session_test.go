package tcp

import (
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

func TestSessionMethods(t *testing.T) {
	conn1, conn2 := net.Pipe()
	defer conn1.Close()
	defer conn2.Close()
	_ = conn2 // keep reference to prevent GC

	sess := newSession(42, conn1, LengthPrefixFramer{MaxFrameBytes: 1024}, 128, 30*time.Second, 0.8)

	// Protocol
	if got := sess.Protocol(); got != core.ProtocolTCP {
		t.Errorf("Protocol() = %v, want %v", got, core.ProtocolTCP)
	}

	// RemoteAddr / LocalAddr
	if got := sess.RemoteAddr(); got == nil {
		t.Error("RemoteAddr() = nil")
	}
	if got := sess.LocalAddr(); got == nil {
		t.Error("LocalAddr() = nil")
	}

	// ID
	if got := sess.ID(); got != 42 {
		t.Errorf("ID() = %d, want 42", got)
	}

	// State
	if got := sess.State(); got != core.StateActive {
		t.Errorf("State() = %v, want %v", got, core.StateActive)
	}

	// CreatedAt
	if got := sess.CreatedAt(); got.IsZero() {
		t.Error("CreatedAt() is zero")
	}

	// LastActiveAt
	if got := sess.LastActiveAt(); got.IsZero() {
		t.Error("LastActiveAt() is zero")
	}

	// Context
	if got := sess.Context(); got == nil {
		t.Error("Context() = nil")
	}

	// SetMeta / GetMeta / DelMeta
	sess.SetMeta("key1", "value1")
	got, ok := sess.GetMeta("key1")
	if !ok || got != "value1" {
		t.Errorf("GetMeta after SetMeta = (%v, %v), want (value1, true)", got, ok)
	}
	sess.DelMeta("key1")
	if _, ok := sess.GetMeta("key1"); ok {
		t.Error("GetMeta after DelMeta returned ok=true")
	}
}
