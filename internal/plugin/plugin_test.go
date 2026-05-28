package plugin

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket-new/internal/core"
)

type fakeSession struct {
	addr net.Addr
}

func (s fakeSession) ID() uint64                  { return 1 }
func (s fakeSession) Protocol() core.Protocol     { return core.ProtocolCustom }
func (s fakeSession) RemoteAddr() net.Addr        { return s.addr }
func (s fakeSession) LocalAddr() net.Addr         { return nil }
func (s fakeSession) State() core.SessionState    { return core.StateActive }
func (s fakeSession) CreatedAt() time.Time        { return time.Now() }
func (s fakeSession) LastActiveAt() time.Time     { return time.Now() }
func (s fakeSession) Context() context.Context    { return context.Background() }
func (s fakeSession) Send([]byte) error           { return nil }
func (s fakeSession) Close(context.Context) error { return nil }
func (s fakeSession) SetMeta(string, any)         {}
func (s fakeSession) GetMeta(string) (any, bool)  { return nil, false }
func (s fakeSession) DelMeta(string)              {}

func TestBlacklistBlocksExactIP(t *testing.T) {
	p := NewBlacklist("127.0.0.1")
	sess := fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}}
	if err := p.OnAccept(sess); err != core.ErrPluginBlock {
		t.Fatalf("OnAccept error = %v, want %v", err, core.ErrPluginBlock)
	}
}

func TestRateLimitDropsOverLimit(t *testing.T) {
	p := NewRateLimit(1, time.Second)
	sess := fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}}
	if _, err := p.OnMessage(sess, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.OnMessage(sess, []byte("two")); err != core.ErrPluginDrop {
		t.Fatalf("OnMessage error = %v, want %v", err, core.ErrPluginDrop)
	}
}
