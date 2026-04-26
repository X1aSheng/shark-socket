package types

import (
	"context"
	"net"
	"testing"
	"time"
)

// mockSession is a minimal RawSession implementation for testing plugins.
type mockSession struct {
	id       uint64
	protocol ProtocolType
	state    SessionState
	meta     map[string]any
	ctx      context.Context
}

func newMockSession(id uint64) *mockSession {
	return &mockSession{id: id, protocol: TCP, state: Active, meta: make(map[string]any), ctx: context.Background()}
}

func (m *mockSession) ID() uint64            { return m.id }
func (m *mockSession) Protocol() ProtocolType { return m.protocol }
func (m *mockSession) RemoteAddr() net.Addr   { return nil }
func (m *mockSession) LocalAddr() net.Addr    { return nil }
func (m *mockSession) CreatedAt() time.Time   { return time.Time{} }
func (m *mockSession) State() SessionState    { return m.state }
func (m *mockSession) IsAlive() bool          { return m.state == Active }
func (m *mockSession) LastActiveAt() time.Time { return time.Now() }
func (m *mockSession) Send(data []byte) error { return nil }
func (m *mockSession) SendTyped(msg []byte) error { return nil }
func (m *mockSession) Close() error           { m.state = Closed; return nil }
func (m *mockSession) Context() context.Context { return m.ctx }
func (m *mockSession) SetMeta(key string, val any)  { m.meta[key] = val }
func (m *mockSession) GetMeta(key string) (any, bool) { v, ok := m.meta[key]; return v, ok }
func (m *mockSession) DelMeta(key string)     { delete(m.meta, key) }

var _ RawSession = (*mockSession)(nil)

func TestBasePluginMethods(t *testing.T) {
	var p BasePlugin
	sess := newMockSession(1)

	if p.Name() != "base" {
		t.Errorf("Name() = %q, want %q", p.Name(), "base")
	}
	if p.Priority() != 100 {
		t.Errorf("Priority() = %d, want 100", p.Priority())
	}
	if err := p.OnAccept(sess); err != nil {
		t.Errorf("OnAccept() = %v, want nil", err)
	}
	data, err := p.OnMessage(sess, []byte("test"))
	if err != nil {
		t.Errorf("OnMessage() err = %v, want nil", err)
	}
	if string(data) != "test" {
		t.Errorf("OnMessage() data = %q, want %q", data, "test")
	}
	p.OnClose(sess) // should not panic
}

func TestPluginInterfaceSatisfaction(t *testing.T) {
	var _ Plugin = BasePlugin{}
	t.Log("BasePlugin satisfies Plugin interface")
}
