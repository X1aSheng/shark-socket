package runtime

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/infra/observability"
)

type metricTestSession struct {
	id uint64
}

func (s *metricTestSession) ID() uint64                  { return s.id }
func (s *metricTestSession) Protocol() core.Protocol     { return core.ProtocolCustom }
func (s *metricTestSession) RemoteAddr() net.Addr        { return nil }
func (s *metricTestSession) LocalAddr() net.Addr         { return nil }
func (s *metricTestSession) CreatedAt() time.Time        { return time.Time{} }
func (s *metricTestSession) LastActiveAt() time.Time     { return time.Time{} }
func (s *metricTestSession) Context() context.Context    { return context.Background() }
func (s *metricTestSession) Send([]byte) error           { return nil }
func (s *metricTestSession) Close(context.Context) error { return nil }
func (s *metricTestSession) SetMeta(string, any)         {}
func (s *metricTestSession) GetMeta(string) (any, bool)  { return nil, false }
func (s *metricTestSession) DelMeta(string)              {}
func (s *metricTestSession) State() core.SessionState    { return core.StateActive }

// TestGatewaySessionMetrics verifies that session register/unregister flows
// into the configured metrics backend (regression: /metrics used to stay empty
// because production code never emitted metrics).
func TestGatewaySessionMetrics(t *testing.T) {
	mem := observability.NewMemoryMetrics()
	gw := NewGateway(WithMetrics(mem))
	sm := gw.Runtime().Sessions()

	s1 := &metricTestSession{id: sm.NextID()}
	s2 := &metricTestSession{id: sm.NextID()}
	if err := sm.Register(s1); err != nil {
		t.Fatal(err)
	}
	if err := sm.Register(s2); err != nil {
		t.Fatal(err)
	}
	if got := mem.Gauge("sessions_active"); got != 2 {
		t.Fatalf("sessions_active = %v, want 2", got)
	}
	if got := mem.Counter("sessions_accepted_total"); got != 2 {
		t.Fatalf("sessions_accepted_total = %v, want 2", got)
	}

	sm.Unregister(s1.ID())
	if got := mem.Gauge("sessions_active"); got != 1 {
		t.Fatalf("sessions_active after unregister = %v, want 1", got)
	}
	if got := mem.Counter("sessions_closed_total"); got != 1 {
		t.Fatalf("sessions_closed_total = %v, want 1", got)
	}

	if err := sm.CloseAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := mem.Gauge("sessions_active"); got != 0 {
		t.Fatalf("sessions_active after CloseAll = %v, want 0", got)
	}
	if got := mem.Counter("sessions_closed_total"); got != 2 {
		t.Fatalf("sessions_closed_total after CloseAll = %v, want 2", got)
	}
}
