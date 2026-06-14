package udp

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type testAddr string

func (a testAddr) Network() string { return "test" }
func (a testAddr) String() string  { return string(a) }

func TestUDP_SessionMethods(t *testing.T) {
	now := time.Now()
	sess := &session{
		id:        42,
		createdAt: now,
		remote:    testAddr("192.168.1.1:12345"),
		local:     testAddr("0.0.0.0:18000"),
	}
	sess.state.Store(uint32(core.StateActive))
	sess.activeAt.Store(now.UnixNano())
	ctx, cancel := context.WithCancel(context.Background())
	sess.ctx = ctx
	sess.cancel = cancel
	defer cancel()

	if got := sess.Protocol(); got != core.ProtocolUDP {
		t.Errorf("Protocol() = %v, want %v", got, core.ProtocolUDP)
	}
	if got := sess.ID(); got != 42 {
		t.Errorf("ID() = %d, want 42", got)
	}
	if got := sess.State(); got != core.StateActive {
		t.Errorf("State() = %v", got)
	}
	if got := sess.CreatedAt(); got.IsZero() {
		t.Error("CreatedAt() is zero")
	}
	if got := sess.LastActiveAt(); got.IsZero() {
		t.Error("LastActiveAt() is zero")
	}
	if got := sess.Context(); got == nil {
		t.Error("Context() = nil")
	}
	if got := sess.RemoteAddr(); got == nil || got.String() != "192.168.1.1:12345" {
		t.Errorf("RemoteAddr() = %v", got)
	}
	if got := sess.LocalAddr(); got == nil || got.String() != "0.0.0.0:18000" {
		t.Errorf("LocalAddr() = %v", got)
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

func TestUDP_newDTLSSession(t *testing.T) {
	conn1, conn2 := net.Pipe()
	defer conn1.Close()
	defer conn2.Close()

	sess := newDTLSSession(42, conn1)

	if got := sess.ID(); got != 42 {
		t.Errorf("ID() = %d, want 42", got)
	}
	if got := sess.Protocol(); got != core.ProtocolUDP {
		t.Errorf("Protocol() = %v", got)
	}
	if got := sess.State(); got != core.StateActive {
		t.Errorf("State() = %v", got)
	}
	if got := sess.RemoteAddr(); got == nil {
		t.Error("RemoteAddr() = nil")
	}
	if got := sess.LocalAddr(); got == nil {
		t.Error("LocalAddr() = nil")
	}
	if got := sess.CreatedAt(); got.IsZero() {
		t.Error("CreatedAt() is zero")
	}
	if got := sess.LastActiveAt(); got.IsZero() {
		t.Error("LastActiveAt() is zero")
	}
	if got := sess.Context(); got == nil {
		t.Error("Context() = nil")
	}
}
