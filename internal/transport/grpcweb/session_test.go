package grpcweb

import (
	"testing"

	"github.com/X1aSheng/shark-socket/internal/core"
)

func TestGRPCWeb_SessionID(t *testing.T) {
	sess := &session{id: 42}
	if got := sess.ID(); got != 42 {
		t.Errorf("ID() = %d, want 42", got)
	}
	if got := sess.Protocol(); got != core.ProtocolGRPCWeb {
		t.Errorf("Protocol() = %v, want %v", got, core.ProtocolGRPCWeb)
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

func TestGRPCWebWSSessionID(t *testing.T) {
	sess := &webSocketSession{id: 42}
	if got := sess.ID(); got != 42 {
		t.Errorf("ID() = %d, want 42", got)
	}
	if got := sess.Protocol(); got != core.ProtocolGRPCWeb {
		t.Errorf("Protocol() = %v, want %v", got, core.ProtocolGRPCWeb)
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
