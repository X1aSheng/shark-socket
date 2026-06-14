package udp

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

func TestUDP_SessionMethods(t *testing.T) {
	conn1, conn2 := net.Pipe()
	defer conn1.Close()
	defer conn2.Close()
	_ = conn2

	sess := newDTLSSession(42, conn1)

	checkProtocol(t, sess)
	checkRemoteAddr(t, sess)
	checkLocalAddr(t, sess)
	checkID(t, sess, 42)
	checkState(t, sess)
	checkCreatedAt(t, sess)
	checkLastActiveAt(t, sess)
	checkContext(t, sess)
	checkMeta(t, sess)
}

func checkProtocol(t *testing.T, sess interface{ Protocol() core.Protocol }) {
	if got := sess.Protocol(); got != core.ProtocolUDP {
		t.Errorf("Protocol() = %v, want %v", got, core.ProtocolUDP)
	}
}
func checkRemoteAddr(t *testing.T, sess interface{ RemoteAddr() net.Addr }) {
	if got := sess.RemoteAddr(); got == nil {
		t.Error("RemoteAddr() = nil")
	}
}
func checkLocalAddr(t *testing.T, sess interface{ LocalAddr() net.Addr }) {
	if got := sess.LocalAddr(); got == nil {
		t.Error("LocalAddr() = nil")
	}
}
func checkID(t *testing.T, sess interface{ ID() uint64 }, want uint64) {
	if got := sess.ID(); got != want {
		t.Errorf("ID() = %d, want %d", got, want)
	}
}
func checkState(t *testing.T, sess interface{ State() core.SessionState }) {
	if got := sess.State(); got != core.StateActive {
		t.Errorf("State() = %v, want %v", got, core.StateActive)
	}
}
func checkCreatedAt(t *testing.T, sess interface{ CreatedAt() time.Time }) {
	if got := sess.CreatedAt(); got.IsZero() {
		t.Error("CreatedAt() is zero")
	}
}
func checkLastActiveAt(t *testing.T, sess interface{ LastActiveAt() time.Time }) {
	if got := sess.LastActiveAt(); got.IsZero() {
		t.Error("LastActiveAt() is zero")
	}
}
func checkContext(t *testing.T, sess interface{ Context() context.Context }) {
	if got := sess.Context(); got == nil {
		t.Error("Context() = nil")
	}
}
func checkMeta(t *testing.T, sess interface {
	SetMeta(string, any)
	GetMeta(string) (any, bool)
	DelMeta(string)
}) {
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
