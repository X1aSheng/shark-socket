package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/yourname/shark-socket/internal/errs"
	"github.com/yourname/shark-socket/internal/types"
)

// mockServer satisfies types.Server for testing.
type mockServer struct {
	proto   types.ProtocolType
	started bool
}

func (m *mockServer) Start() error              { m.started = true; return nil }
func (m *mockServer) Stop(_ context.Context) error { m.started = false; return nil }
func (m *mockServer) Protocol() types.ProtocolType { return m.proto }

func TestNew(t *testing.T) {
	gw := New(WithMetricsEnabled(false))
	if gw == nil {
		t.Fatal("expected non-nil gateway")
	}
}

func TestRegister(t *testing.T) {
	gw := New(WithMetricsEnabled(false))

	srv := &mockServer{proto: types.TCP}
	if err := gw.Register(srv); err != nil {
		t.Fatalf("Register returned unexpected error: %v", err)
	}

	// Duplicate protocol should fail.
	dup := &mockServer{proto: types.TCP}
	if err := gw.Register(dup); err != errs.ErrDuplicateProtocol {
		t.Fatalf("expected ErrDuplicateProtocol, got: %v", err)
	}
}

func TestStart_NoServers(t *testing.T) {
	gw := New(WithMetricsEnabled(false))
	if err := gw.Start(); err != errs.ErrNoServerRegistered {
		t.Fatalf("expected ErrNoServerRegistered, got: %v", err)
	}
}

func TestStartStopLifecycle(t *testing.T) {
	gw := New(
		WithMetricsEnabled(false),
		WithShutdownTimeout(2*time.Second),
	)

	srv := &mockServer{proto: types.TCP}
	if err := gw.Register(srv); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !srv.started {
		t.Fatal("expected server to be started")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := gw.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if srv.started {
		t.Fatal("expected server to be stopped")
	}
}

func TestProtocol(t *testing.T) {
	gw := New(WithMetricsEnabled(false))
	if pt := gw.Protocol(); pt != types.Custom {
		t.Fatalf("expected Custom protocol, got: %v", pt)
	}
}

func TestManager_BeforeStart(t *testing.T) {
	gw := New(WithMetricsEnabled(false))
	if mgr := gw.Manager(); mgr != nil {
		t.Fatal("expected nil Manager before Start")
	}
}
