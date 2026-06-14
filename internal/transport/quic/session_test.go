package quic

import (
	"testing"

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
