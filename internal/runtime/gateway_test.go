package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type fakeServer struct {
	proto       core.Protocol
	startErr    error
	started     bool
	stopped     bool
	stopAccepts int
	drains      int
	closes      int
}

func (s *fakeServer) Protocol() core.Protocol { return s.proto }

func (s *fakeServer) Start(context.Context) error {
	if s.startErr != nil {
		return s.startErr
	}
	s.started = true
	return nil
}

func (s *fakeServer) Stop(context.Context) error {
	s.stopped = true
	return nil
}

func (s *fakeServer) StopAccept(context.Context) error {
	s.stopAccepts++
	return nil
}

func (s *fakeServer) Drain(context.Context) error {
	s.drains++
	return nil
}

func (s *fakeServer) CloseSessions(context.Context) error {
	s.closes++
	return nil
}

func TestGatewayRejectsDuplicateProtocol(t *testing.T) {
	g := NewGateway()
	if err := g.Register(&fakeServer{proto: core.ProtocolTCP}); err != nil {
		t.Fatal(err)
	}
	if err := g.Register(&fakeServer{proto: core.ProtocolTCP}); !errors.Is(err, core.ErrDuplicateProtocol) {
		t.Fatalf("Register duplicate error = %v, want %v", err, core.ErrDuplicateProtocol)
	}
}

func TestGatewayStartRollback(t *testing.T) {
	g := NewGateway()
	first := &fakeServer{proto: core.ProtocolTCP}
	second := &fakeServer{proto: core.ProtocolUDP, startErr: errors.New("boom")}
	if err := g.Register(first); err != nil {
		t.Fatal(err)
	}
	if err := g.Register(second); err != nil {
		t.Fatal(err)
	}
	if err := g.Start(context.Background()); err == nil {
		t.Fatal("Start succeeded, want error")
	}
	if !first.stopped {
		t.Fatal("started server was not rolled back")
	}
	if g.Ready() {
		t.Fatal("gateway is ready after failed start")
	}
}

func TestGatewayStagedStop(t *testing.T) {
	g := NewGateway()
	s := &fakeServer{proto: core.ProtocolTCP}
	if err := g.Register(s); err != nil {
		t.Fatal(err)
	}
	if err := g.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !g.Ready() {
		t.Fatal("gateway is not ready after start")
	}
	if err := g.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.stopAccepts != 1 || s.drains != 1 || s.closes != 1 {
		t.Fatalf("stages = stopAccept:%d drain:%d close:%d", s.stopAccepts, s.drains, s.closes)
	}
	if g.Ready() {
		t.Fatal("gateway is ready after stop")
	}
}
